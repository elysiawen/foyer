package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound 表示查询的记录不存在。
var ErrNotFound = errors.New("记录不存在")

type Store struct {
	db *sql.DB
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT    NOT NULL UNIQUE,
		password_hash TEXT    NOT NULL,
		totp_secret   TEXT    NOT NULL DEFAULT '',
		role          TEXT    NOT NULL DEFAULT 'user',
		enabled       INTEGER NOT NULL DEFAULT 1,
		created_at    INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS apps (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT    NOT NULL,
		subdomain   TEXT    NOT NULL UNIQUE,
		upstream    TEXT    NOT NULL,
		icon        TEXT    NOT NULL DEFAULT '',
		description TEXT    NOT NULL DEFAULT '',
		health_path TEXT    NOT NULL DEFAULT '/',
		sort_order  INTEGER NOT NULL DEFAULT 100,
		enabled     INTEGER NOT NULL DEFAULT 1,
		safe_rewrite INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS app_access (
		app_id  INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		PRIMARY KEY (app_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT    PRIMARY KEY,
		user_id    INTEGER NOT NULL,
		ip         TEXT    NOT NULL DEFAULT '',
		ua         TEXT    NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL DEFAULT 0,
		username      TEXT    NOT NULL DEFAULT '',
		app_subdomain TEXT    NOT NULL DEFAULT '',
		host          TEXT    NOT NULL DEFAULT '',
		path          TEXT    NOT NULL DEFAULT '',
		ip            TEXT    NOT NULL DEFAULT '',
		ua            TEXT    NOT NULL DEFAULT '',
		status        INTEGER NOT NULL DEFAULT 0,
		created_at    INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC)`,
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单写多读，这里收敛到单连接，避免 family 规模下的锁竞争。
	db.SetMaxOpenConns(1)
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化数据表: %w", err)
		}
	}
	// 旧库补列，失败说明已经存在。
	_, _ = db.Exec(`ALTER TABLE apps ADD COLUMN safe_rewrite INTEGER NOT NULL DEFAULT 0`)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type rowScanner interface {
	Scan(dest ...any) error
}

// ---------- 用户 ----------

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPSecret   string
	Role         string
	Enabled      bool
	CreatedAt    int64
}

func scanUser(r rowScanner) (*User, error) {
	var u User
	var enabled int
	err := r.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.Role, &enabled, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	return &u, nil
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE role = 'admin' AND enabled = 1`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, passwordHash, role string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, role, enabled, created_at) VALUES(?,?,?,1,?)`,
		username, passwordHash, role, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUser(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT id, username, password_hash, totp_secret, role, enabled, created_at FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT id, username, password_hash, totp_secret, role, enabled, created_at FROM users WHERE username = ?`, username))
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash, totp_secret, role, enabled, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePassword(id int64, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	return err
}

func (s *Store) UpdateUsername(id int64, newName string) error {
	_, err := s.db.Exec(`UPDATE users SET username = ? WHERE id = ?`, newName, id)
	return err
}

func (s *Store) UpdateUserRole(id int64, role string) error {
	_, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

func (s *Store) SetTOTP(id int64, secret string) error {
	_, err := s.db.Exec(`UPDATE users SET totp_secret = ? WHERE id = ?`, secret, id)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// ---------- 应用 ----------

type App struct {
	ID          int64
	Name        string
	Subdomain   string
	Upstream    string
	Icon        string
	Description string
	HealthPath  string
	Sort        int
	Enabled     bool
	SafeRewrite bool
}

func scanApp(r rowScanner) (*App, error) {
	var a App
	var enabled, safe int
	err := r.Scan(&a.ID, &a.Name, &a.Subdomain, &a.Upstream, &a.Icon, &a.Description, &a.HealthPath, &a.Sort, &enabled, &safe)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Enabled = enabled == 1
	a.SafeRewrite = safe == 1
	return &a, nil
}

const appColumns = `id, name, subdomain, upstream, icon, description, health_path, sort_order, enabled, safe_rewrite`

func (s *Store) ListApps() ([]App, error) {
	rows, err := s.db.Query(`SELECT ` + appColumns + ` FROM apps ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) GetApp(id int64) (*App, error) {
	return scanApp(s.db.QueryRow(`SELECT `+appColumns+` FROM apps WHERE id = ?`, id))
}

func (s *Store) GetAppBySubdomain(subdomain string) (*App, error) {
	return scanApp(s.db.QueryRow(`SELECT `+appColumns+` FROM apps WHERE subdomain = ?`, subdomain))
}

func (s *Store) CreateApp(a *App) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO apps(name, subdomain, upstream, icon, description, health_path, sort_order, enabled, safe_rewrite)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Subdomain, a.Upstream, a.Icon, a.Description, a.HealthPath, a.Sort, boolInt(a.Enabled), boolInt(a.SafeRewrite),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateApp(a *App) error {
	_, err := s.db.Exec(
		`UPDATE apps SET name=?, subdomain=?, upstream=?, icon=?, description=?, health_path=?, sort_order=?, enabled=?, safe_rewrite=?
		 WHERE id=?`,
		a.Name, a.Subdomain, a.Upstream, a.Icon, a.Description, a.HealthPath, a.Sort, boolInt(a.Enabled), boolInt(a.SafeRewrite), a.ID,
	)
	return err
}

func (s *Store) DeleteApp(id int64) error {
	_, err := s.db.Exec(`DELETE FROM apps WHERE id = ?`, id)
	return err
}

func (s *Store) AppsForUser(userID int64) ([]App, error) {
	rows, err := s.db.Query(
		`SELECT a.id, a.name, a.subdomain, a.upstream, a.icon, a.description, a.health_path, a.sort_order, a.enabled, a.safe_rewrite
		 FROM apps a JOIN app_access x ON x.app_id = a.id
		 WHERE x.user_id = ? AND a.enabled = 1
		 ORDER BY a.sort_order, a.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ---------- 授权 ----------

func (s *Store) SetAppAccess(appID int64, userIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM app_access WHERE app_id = ?`, appID); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO app_access(app_id, user_id) VALUES(?,?)`, appID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AppAccessUsers(appID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT user_id FROM app_access WHERE app_id = ?`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) UserCanAccess(userID, appID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM app_access WHERE app_id = ? AND user_id = ?`, appID, userID).Scan(&n)
	return n > 0, err
}

func (s *Store) UserAppAccess(userID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT app_id FROM app_access WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) SetUserAccess(userID int64, appIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM app_access WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, aid := range appIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO app_access(app_id, user_id) VALUES(?,?)`, aid, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- 会话 ----------

func (s *Store) CreateSession(tokenHash string, userID int64, ip, ua string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.Exec(
		`INSERT INTO sessions(token_hash, user_id, ip, ua, created_at, expires_at) VALUES(?,?,?,?,?,?)`,
		tokenHash, userID, ip, ua, now.Unix(), now.Add(ttl).Unix(),
	)
	return err
}

func (s *Store) SessionUser(tokenHash string) (*User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT u.id, u.username, u.password_hash, u.totp_secret, u.role, u.enabled, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ? AND u.enabled = 1`,
		tokenHash, time.Now().Unix()))
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) PurgeExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// ---------- 审计 ----------

type AuditEntry struct {
	ID           int64
	Username     string
	AppSubdomain string
	Host         string
	Path         string
	IP           string
	UA           string
	Status       int
	CreatedAt    int64
}

func (s *Store) AddAudit(userID int64, username, appSubdomain, host, path, ip, ua string, status int) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_log(user_id, username, app_subdomain, host, path, ip, ua, status, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		userID, username, appSubdomain, host, path, ip, ua, status, time.Now().Unix(),
	)
	return err
}

func (s *Store) ListAudit(limit, offset int) ([]AuditEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, username, app_subdomain, host, path, ip, ua, status, created_at
		 FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Username, &e.AppSubdomain, &e.Host, &e.Path, &e.IP, &e.UA, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CountAudit() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM audit_log`).Scan(&n)
	return n, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
