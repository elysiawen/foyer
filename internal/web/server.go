package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"foyer/internal/auth"
	"foyer/internal/config"
	"foyer/internal/health"
	"foyer/internal/store"
)

//go:embed templates/*.html static
var assets embed.FS

type Server struct {
	cfg config.Config
	st  *store.Store
	hc  *health.Checker
	mux *http.ServeMux
	tpl map[string]*template.Template

	limMu sync.Mutex
	lim   map[string]*limitEntry

	audMu   sync.Mutex
	audSeen map[string]time.Time

	pendMu sync.Mutex
	pend   map[string]pendingLogin

	flashMu sync.Mutex
	flash   map[string]flashEntry
}

// pendingLogin 是密码已通过、等待 2FA 验证的中间态。
type pendingLogin struct {
	userID   int64
	username string
	expires  time.Time
}

// flashEntry 是留给下一次页面加载展示的一次性提示，取出后即删除。
type flashEntry struct {
	message string
	expires time.Time
}

type limitEntry struct {
	fails int
	until time.Time
}

func New(cfg config.Config, st *store.Store) (*Server, error) {
	tpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		st:      st,
		hc:      health.New(),
		tpl:     tpl,
		lim:     make(map[string]*limitEntry),
		audSeen: make(map[string]time.Time),
		pend:    make(map[string]pendingLogin),
		flash:   make(map[string]flashEntry),
	}
	s.mux = s.routes()
	return s, nil
}

func (s *Server) Health() *health.Checker { return s.hc }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handlePortal)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /login/otp", s.handleLoginOTP)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("POST /account/username", s.handleUsernameChange)
	mux.HandleFunc("POST /account/password", s.handlePasswordChange)
	mux.HandleFunc("POST /account/totp/start", s.handleTOTPStart)
	mux.HandleFunc("POST /account/totp/enable", s.handleTOTPEnable)
	mux.HandleFunc("POST /account/totp/disable", s.handleTOTPDisable)

	mux.HandleFunc("GET /admin", s.handleAdminIndex)
	mux.HandleFunc("GET /admin/apps", s.handleAdminApps)
	mux.HandleFunc("POST /admin/apps", s.handleAppCreate)
	mux.HandleFunc("POST /admin/apps/{id}", s.handleAppUpdate)
	mux.HandleFunc("POST /admin/apps/{id}/delete", s.handleAppDelete)
	mux.HandleFunc("GET /admin/users", s.handleAdminUsers)
	mux.HandleFunc("POST /admin/users", s.handleUserCreate)
	mux.HandleFunc("POST /admin/users/{id}", s.handleUserUpdate)
	mux.HandleFunc("POST /admin/users/{id}/totp", s.handleUserTOTPReset)
	mux.HandleFunc("POST /admin/users/{id}/delete", s.handleUserDelete)
	mux.HandleFunc("GET /admin/audit", s.handleAudit)

	staticFS, err := fs.Sub(assets, "static")
	if err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	// 兜底路由：/{应用路径}/... 由门户反代到对应上游
	mux.HandleFunc("/", s.handleAppProxy)

	return mux
}

// pageData 是所有页面模板共用的数据容器。
type pageData struct {
	Title      string
	BaseDomain string
	User       *store.User
	IsAdmin    bool
	Error      string
	Notice     string

	Redirect   string
	Username   string
	Apps       []appCard
	Rows       []appAdminRow
	Users      []store.User
	Audit      []store.AuditEntry
	AuditPage  int
	AuditPages int
	AuditTotal int
	AuditSize  int
	PrevPage   int
	NextPage   int
	OpenModal  string
	TOTPURL    string
	HasTOTP    bool
	SecretBox  string

	// 应用不可达页
	AppName  string
	Upstream string
	Retry    string

	OTPChallenge bool
	PendingToken string

	// 应用管理页/用户管理页共用的应用列表
	AllApps []store.App
	// 用户管理页：userID → appID → 是否授权
	UserAppAccess map[int64]map[int64]bool
}

// appAdminRow 是应用管理列表的一行，带上该应用的可见用户集合。
type appAdminRow struct {
	App    store.App
	Access map[int64]bool
}

type appCard struct {
	store.App
	URL   string
	Up    bool
	Known bool
}

func (s *Server) page(title string, u *store.User) pageData {
	return pageData{
		Title:      title,
		BaseDomain: s.cfg.BaseDomain,
		User:       u,
		IsAdmin:    u != nil && u.Role == "admin",
	}
}

var tmplFuncs = template.FuncMap{
	"timefmt": func(ts int64) string {
		if ts == 0 {
			return "-"
		}
		return time.Unix(ts, 0).Format("2006-01-02 15:04")
	},
}

// loadTemplates 为每个页面单独解析 layout + 页面文件，避免不同页面的
// {{define "content"}} 互相覆盖。
func loadTemplates() (map[string]*template.Template, error) {
	pages := []string{"login", "portal", "account", "admin_apps", "admin_users", "admin_audit", "denied", "unreachable"}
	out := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.New("layout").Funcs(tmplFuncs).ParseFS(
			assets, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("解析模板 %s: %w", p, err)
		}
		out[p] = t
	}
	return out, nil
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	t, ok := s.tpl[name]
	if !ok {
		http.Error(w, "模板不存在", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
	}
}

// renderStatus 用于必须返回非 200 状态码的页面（例如 forward_auth 的 403）。
func (s *Server) renderStatus(w http.ResponseWriter, status int, name string, data pageData) {
	t, ok := s.tpl[name]
	if !ok {
		http.Error(w, "模板不存在", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = t.ExecuteTemplate(w, "layout", data)
}

// ---------- 请求辅助 ----------

func (s *Server) currentUser(r *http.Request) *store.User {
	token := auth.Token(r)
	if token == "" {
		return nil
	}
	u, err := s.st.SessionUser(auth.HashToken(token))
	if err != nil {
		return nil
	}
	return u
}

func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) *store.User {
	u := s.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?rd="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return nil
	}
	return u
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) *store.User {
	u := s.requireLogin(w, r)
	if u == nil {
		return nil
	}
	if u.Role != "admin" {
		http.Error(w, "需要管理员权限", http.StatusForbidden)
		return nil
	}
	return u
}

func (s *Server) hostOnlyHost(r *http.Request) string {
	return hostOnly(r.Host)
}

func hostOnly(h string) string {
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.Trim(h, "[]")
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	return hostOnly(r.RemoteAddr)
}

// sameOrigin 用于所有 POST 的轻量 CSRF 防护：来源必须与当前 Host 一致。
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(hostOnly(u.Host), hostOnly(r.Host))
}

// safeRedirect 只允许跳回本域及其子域，防止开放重定向。
func (s *Server) safeRedirect(rd string) string {
	if rd == "" {
		return "/"
	}
	u, err := url.Parse(rd)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "/"
	}
	host := strings.ToLower(hostOnly(u.Host))
	base := strings.ToLower(s.cfg.BaseDomain)
	if host != base && !strings.HasSuffix(host, "."+base) {
		return "/"
	}
	return u.String()
}

func (s *Server) scheme() string {
	if s.cfg.SecureCookie {
		return "https"
	}
	return "http"
}

// ---------- 登录限流 ----------

func (s *Server) loginBlocked(key string) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	e, ok := s.lim[key]
	if !ok {
		return false
	}
	return time.Now().Before(e.until)
}

func (s *Server) loginFail(key string) {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	e, ok := s.lim[key]
	if !ok {
		e = &limitEntry{}
		s.lim[key] = e
	}
	e.fails++
	if e.fails >= 5 {
		e.fails = 0
		e.until = time.Now().Add(10 * time.Minute)
	}
	// 简单的容量控制，避免内存无限增长。
	if len(s.lim) > 5000 {
		s.lim = make(map[string]*limitEntry)
	}
}

func (s *Server) loginReset(key string) {
	s.limMu.Lock()
	delete(s.lim, key)
	s.limMu.Unlock()
}

// ---------- 审计去重 ----------

// auditOnce 对同一用户访问同一应用做 10 分钟去重，避免每个请求都写库。
func (s *Server) auditOnce(r *http.Request, u *store.User, appSub string, status int) {
	key := fmt.Sprintf("%d|%s", u.ID, appSub)
	now := time.Now()

	s.audMu.Lock()
	if last, ok := s.audSeen[key]; ok && now.Sub(last) < 10*time.Minute {
		s.audMu.Unlock()
		return
	}
	if len(s.audSeen) > 5000 {
		s.audSeen = make(map[string]time.Time)
	}
	s.audSeen[key] = now
	s.audMu.Unlock()

	_ = s.st.AddAudit(u.ID, u.Username, appSub, hostOnly(r.Host), r.URL.RequestURI(), clientIP(r), r.UserAgent(), status)
}

// ---------- 一次性提示（flash） ----------

// flashTTL 限制提示的存活时间：用户收到 303 后一般会立刻加载目标页，
// 超过这个时间还没取走就说明会话已经离开，直接丢弃。
const flashTTL = 5 * time.Minute

// flashKey 按会话区分提示，避免同一台机器上不同账号互相串消息。
func flashKey(r *http.Request) string {
	if token := auth.Token(r); token != "" {
		return auth.HashToken(token)
	}
	return ""
}

func (s *Server) setFlash(r *http.Request, msg string) {
	key := flashKey(r)
	if key == "" || msg == "" {
		return
	}
	now := time.Now()
	s.flashMu.Lock()
	for k, v := range s.flash {
		if now.After(v.expires) {
			delete(s.flash, k)
		}
	}
	s.flash[key] = flashEntry{message: msg, expires: now.Add(flashTTL)}
	s.flashMu.Unlock()
}

// takeFlash 取出并删除当前会话的提示，保证刷新页面不会重复弹出。
func (s *Server) takeFlash(r *http.Request) string {
	key := flashKey(r)
	if key == "" {
		return ""
	}
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	e, ok := s.flash[key]
	if !ok {
		return ""
	}
	delete(s.flash, key)
	if time.Now().After(e.expires) {
		return ""
	}
	return e.message
}
