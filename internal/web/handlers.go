package web

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"foyer/internal/auth"
	"foyer/internal/health"
	"foyer/internal/store"
)

var subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedSlugs 是门户自己占用的第一段路径，不能给应用使用。
var reservedSlugs = map[string]bool{
	"login": true, "logout": true, "account": true, "admin": true,
	"static": true, "healthz": true, "api": true, "favicon.ico": true,
}

// ---------- 门户 ----------

func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	apps, err := s.visibleApps(u)
	if err != nil {
		http.Error(w, "读取应用列表失败", http.StatusInternalServerError)
		return
	}

	d := s.page("应用", u)
	cards := make([]appCard, 0, len(apps))
	for _, a := range apps {
		st := s.hc.Get(a.ID)
		cards = append(cards, appCard{
			App:   a,
			URL:   "/" + a.Subdomain + "/",
			Up:    st.Up,
			Known: st.Known,
		})
	}
	d.Apps = cards
	s.render(w, "portal", d)
}

func (s *Server) visibleApps(u *store.User) ([]store.App, error) {
	if u.Role != "admin" {
		return s.st.AppsForUser(u.ID)
	}
	all, err := s.st.ListApps()
	if err != nil {
		return nil, err
	}
	out := make([]store.App, 0, len(all))
	for _, a := range all {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out, nil
}

// ---------- 登录 / 登出 ----------

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := s.currentUser(r); u != nil {
		http.Redirect(w, r, s.safeRedirect(r.URL.Query().Get("rd")), http.StatusSeeOther)
		return
	}
	d := s.page("登录", nil)
	d.Redirect = s.safeRedirect(r.URL.Query().Get("rd"))
	s.render(w, "login", d)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()

	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	rd := s.safeRedirect(r.PostFormValue("rd"))

	renderErr := func(msg string) {
		d := s.page("登录", nil)
		d.Error = msg
		d.Redirect = rd
		d.Username = username
		s.render(w, "login", d)
	}

	key := "login|" + username + "|" + clientIP(r)
	if s.loginBlocked(key) {
		renderErr("尝试次数过多，请 10 分钟后再试")
		return
	}

	u, err := s.st.GetUserByUsername(username)
	if err != nil || !u.Enabled || !auth.CheckPassword(u.PasswordHash, password) {
		s.loginFail(key)
		renderErr("用户名或密码错误")
		return
	}

	// 密码正确但开启了 2FA：先不发会话，弹窗让用户单独输入验证码。
	if u.TOTPSecret != "" {
		s.loginReset(key)
		d := s.page("登录", nil)
		d.Redirect = rd
		d.Username = u.Username
		d.OTPChallenge = true
		d.PendingToken = s.newPendingLogin(u)
		d.OpenModal = "login-otp"
		s.render(w, "login", d)
		return
	}

	s.finishLogin(w, r, u, rd)
}

// handleLoginOTP 校验第二步的动态验证码，通过后才真正建立会话。
func (s *Server) handleLoginOTP(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()

	token := r.PostFormValue("token")
	code := strings.TrimSpace(r.PostFormValue("code"))
	rd := s.safeRedirect(r.PostFormValue("rd"))

	pl, ok := s.getPendingLogin(token)
	if !ok {
		d := s.page("登录", nil)
		d.Error = "验证已过期，请重新登录"
		s.render(w, "login", d)
		return
	}

	renderOTP := func(msg string) {
		d := s.page("登录", nil)
		d.Error = msg
		d.Redirect = rd
		d.Username = pl.username
		d.OTPChallenge = true
		d.PendingToken = token
		d.OpenModal = "login-otp"
		s.render(w, "login", d)
	}

	key := "otp|" + pl.username + "|" + clientIP(r)
	if s.loginBlocked(key) {
		renderOTP("尝试次数过多，请 10 分钟后再试")
		return
	}

	u, err := s.st.GetUser(pl.userID)
	if err != nil || !u.Enabled || u.TOTPSecret == "" {
		renderOTP("验证已失效，请重新登录")
		return
	}
	if !auth.ValidateTOTP(u.TOTPSecret, code) {
		s.loginFail(key)
		renderOTP("动态验证码错误")
		return
	}

	s.loginReset(key)
	s.clearPendingLogin(token)
	s.finishLogin(w, r, u, rd)
}

func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *store.User, rd string) {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		http.Error(w, "生成会话失败", http.StatusInternalServerError)
		return
	}
	if err := s.st.CreateSession(hash, u.ID, clientIP(r), r.UserAgent(), s.cfg.SessionTTL); err != nil {
		http.Error(w, "创建会话失败", http.StatusInternalServerError)
		return
	}
	_ = s.st.PurgeExpiredSessions()
	auth.SetSessionCookie(w, s.cfg.CookieDomain, s.cfg.SecureCookie, token, s.cfg.SessionTTL)
	_ = s.st.AddAudit(u.ID, u.Username, "", hostOnly(r.Host), r.URL.RequestURI(), clientIP(r), r.UserAgent(), 200)

	http.Redirect(w, r, rd, http.StatusSeeOther)
}

// ---------- 登录中间态：密码已通过，等待 2FA ----------

const pendingLoginTTL = 5 * time.Minute

func (s *Server) newPendingLogin(u *store.User) string {
	token, _, err := auth.NewSessionToken()
	if err != nil {
		return ""
	}
	now := time.Now()
	s.pendMu.Lock()
	for k, v := range s.pend {
		if now.After(v.expires) {
			delete(s.pend, k)
		}
	}
	s.pend[token] = pendingLogin{userID: u.ID, username: u.Username, expires: now.Add(pendingLoginTTL)}
	s.pendMu.Unlock()
	return token
}

func (s *Server) getPendingLogin(token string) (pendingLogin, bool) {
	if token == "" {
		return pendingLogin{}, false
	}
	s.pendMu.Lock()
	defer s.pendMu.Unlock()
	pl, ok := s.pend[token]
	if !ok || time.Now().After(pl.expires) {
		delete(s.pend, token)
		return pendingLogin{}, false
	}
	return pl, true
}

func (s *Server) clearPendingLogin(token string) {
	s.pendMu.Lock()
	delete(s.pend, token)
	s.pendMu.Unlock()
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	if token := auth.Token(r); token != "" {
		_ = s.st.DeleteSession(auth.HashToken(token))
	}
	auth.ClearSessionCookie(w, s.cfg.CookieDomain, s.cfg.SecureCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------- 路径式反向代理 ----------

// handleAppProxy 处理 /{应用路径}/... 的请求：先校验登录与应用权限，
// 再把去掉了路径前缀的请求转发给内网应用。
func (s *Server) handleAppProxy(w http.ResponseWriter, r *http.Request) {
	slug, rest := splitAppPath(r.URL.Path)
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	u := s.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?rd="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}

	app, err := s.st.GetAppBySubdomain(slug)
	if err != nil || !app.Enabled {
		http.NotFound(w, r)
		return
	}

	if u.Role != "admin" {
		ok, err := s.st.UserCanAccess(u.ID, app.ID)
		if err != nil || !ok {
			s.auditOnce(r, u, app.Subdomain, http.StatusForbidden)
			s.renderStatus(w, http.StatusForbidden, "denied", s.page("无权限", u))
			return
		}
	}

	// 补结尾斜杠，避免应用返回的相对链接解析到门户根路径。
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	s.auditOnce(r, u, app.Subdomain, http.StatusOK)
	s.serveProxy(w, r, u, app, rest)
}

// splitAppPath 把 /jellyfin/abc/def 拆成 ("jellyfin", "abc/def")。
func splitAppPath(p string) (slug, rest string) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", ""
	}
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

func (s *Server) serveProxy(w http.ResponseWriter, r *http.Request, u *store.User, app *store.App, rest string) {
	target, err := url.Parse(health.ProbeURL(app.Upstream, "/"))
	if err != nil {
		http.Error(w, "上游地址无效", http.StatusInternalServerError)
		return
	}
	base := strings.TrimSuffix(target.Path, "/")

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = base + "/" + rest
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			pr.SetXForwarded()
			// 入口会话 cookie 不下发给上游应用。
			stripCookie(pr.Out.Header, auth.CookieName)
			// 让上游返回未压缩内容，便于改写响应里的绝对路径。
			pr.Out.Header.Del("Accept-Encoding")
		},
		ErrorHandler: func(ew http.ResponseWriter, _ *http.Request, err error) {
			d := s.page("应用不可达", u)
			d.AppName = app.Name
			d.Upstream = app.Upstream
			d.Retry = r.URL.RequestURI()
			// 绕过路径改写，否则页面里的 "/" 会被补成 "/{应用}/"。
			s.renderStatus(unwrapWriter(ew), http.StatusBadGateway, "unreachable", d)
		},
	}

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	rw := &pathRewriteWriter{
		ResponseWriter: w,
		prefix:         "/" + app.Subdomain,
		secure:         secure,
		safe:           app.SafeRewrite,
	}
	proxy.ServeHTTP(rw, r)
	rw.finish()
}

func stripCookie(h http.Header, name string) {
	raw := h.Get("Cookie")
	if raw == "" {
		return
	}
	parts := strings.Split(raw, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.HasPrefix(strings.TrimSpace(p), name+"=") {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		h.Del("Cookie")
		return
	}
	h.Set("Cookie", strings.Join(kept, ";"))
}

// ---------- 账号 ----------

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	s.renderAccount(w, u, "", "")
}

func (s *Server) renderAccount(w http.ResponseWriter, u *store.User, notice, errMsg string) {
	d := s.page("账号", u)
	d.Notice = notice
	d.Error = errMsg
	d.HasTOTP = u.TOTPSecret != ""
	s.render(w, "account", d)
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	current := r.PostFormValue("current")
	next := r.PostFormValue("password")

	if !auth.CheckPassword(u.PasswordHash, current) {
		s.renderAccount(w, u, "", "当前密码不正确")
		return
	}
	if len([]rune(next)) < 8 {
		s.renderAccount(w, u, "", "新密码至少需要 8 个字符")
		return
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		s.renderAccount(w, u, "", "密码处理失败")
		return
	}
	if err := s.st.UpdatePassword(u.ID, hash); err != nil {
		s.renderAccount(w, u, "", "保存密码失败")
		return
	}
	s.renderAccount(w, u, "密码已更新", "")
}

func (s *Server) handleUsernameChange(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	newName := strings.TrimSpace(r.PostFormValue("username"))

	if newName == "" {
		s.renderAccount(w, u, "", "用户名不能为空")
		return
	}
	if len(newName) > 64 {
		s.renderAccount(w, u, "", "用户名太长，请控制在 64 个字符以内")
		return
	}
	if newName == u.Username {
		s.renderAccount(w, u, "", "新用户名和当前一样")
		return
	}
	if existing, err := s.st.GetUserByUsername(newName); err == nil && existing.ID != u.ID {
		s.renderAccount(w, u, "", "这个用户名已被占用")
		return
	}
	if err := s.st.UpdateUsername(u.ID, newName); err != nil {
		s.renderAccount(w, u, "", "保存失败，请重试")
		return
	}
	updated, _ := s.st.GetUser(u.ID)
	if updated != nil {
		u = updated
	}
	s.renderAccount(w, u, "用户名已更新", "")
}

func (s *Server) handleTOTPStart(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	secret, otpauth, err := auth.NewTOTP("Foyer", u.Username)
	if err != nil {
		s.renderAccount(w, u, "", "生成密钥失败")
		return
	}
	d := s.page("账号", u)
	d.HasTOTP = u.TOTPSecret != ""
	d.SecretBox = secret
	d.TOTPURL = otpauth
	s.render(w, "account", d)
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	secret := strings.TrimSpace(r.PostFormValue("secret"))
	code := strings.TrimSpace(r.PostFormValue("code"))

	if !auth.ValidateTOTP(secret, code) {
		d := s.page("账号", u)
		d.HasTOTP = u.TOTPSecret != ""
		d.SecretBox = secret
		d.Error = "验证码不正确，请确认手机时间已同步后重试"
		s.render(w, "account", d)
		return
	}
	if err := s.st.SetTOTP(u.ID, secret); err != nil {
		s.renderAccount(w, u, "", "保存 2FA 密钥失败")
		return
	}
	updated, _ := s.st.GetUser(u.ID)
	if updated != nil {
		u = updated
	}
	s.renderAccount(w, u, "已启用动态验证码（2FA）", "")
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	password := r.PostFormValue("password")
	code := strings.TrimSpace(r.PostFormValue("code"))

	if !auth.CheckPassword(u.PasswordHash, password) {
		s.renderAccount(w, u, "", "密码不正确")
		return
	}
	if !auth.ValidateTOTP(u.TOTPSecret, code) {
		s.renderAccount(w, u, "", "动态验证码不正确")
		return
	}
	if err := s.st.SetTOTP(u.ID, ""); err != nil {
		s.renderAccount(w, u, "", "关闭 2FA 失败")
		return
	}
	updated, _ := s.st.GetUser(u.ID)
	if updated != nil {
		u = updated
	}
	s.renderAccount(w, u, "已关闭动态验证码", "")
}

// ---------- 管理后台：应用 ----------

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/apps", http.StatusSeeOther)
}

func (s *Server) handleAdminApps(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	s.renderAdminApps(w, u, s.takeFlash(r), "", "")
}

// redirectOK 用 303 跳回列表页，提示暂存为一次性 flash，
// 由目标页取出展示，避免把文案写进 URL。
func (s *Server) redirectOK(w http.ResponseWriter, r *http.Request, path, msg string) {
	s.setFlash(r, msg)
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) renderAdminApps(w http.ResponseWriter, u *store.User, notice, errMsg, openModal string) {
	apps, err := s.st.ListApps()
	if err != nil {
		http.Error(w, "读取应用失败", http.StatusInternalServerError)
		return
	}
	users, err := s.st.ListUsers()
	if err != nil {
		http.Error(w, "读取用户失败", http.StatusInternalServerError)
		return
	}

	d := s.page("应用管理", u)
	d.Notice = notice
	d.Error = errMsg
	d.OpenModal = openModal
	for _, x := range users {
		if x.Role != "admin" {
			d.Users = append(d.Users, x)
		}
	}
	for _, a := range apps {
		ids, err := s.st.AppAccessUsers(a.ID)
		if err != nil {
			http.Error(w, "读取授权失败", http.StatusInternalServerError)
			return
		}
		access := make(map[int64]bool, len(ids))
		for _, id := range ids {
			access[id] = true
		}
		d.Rows = append(d.Rows, appAdminRow{App: a, Access: access})
	}
	s.render(w, "admin_apps", d)
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()

	a, msg := appFromForm(r)
	if msg != "" {
		s.renderAdminApps(w, u, "", msg, "app-new")
		return
	}
	if _, err := s.st.CreateApp(a); err != nil {
		s.renderAdminApps(w, u, "", "创建失败：路径可能已被占用", "app-new")
		return
	}
	s.redirectOK(w, r, "/admin/apps", "应用已创建")
}

func appFromForm(r *http.Request) (*store.App, string) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	sub := strings.ToLower(strings.TrimSpace(r.PostFormValue("subdomain")))
	upstream := strings.TrimSpace(r.PostFormValue("upstream"))
	icon := strings.TrimSpace(r.PostFormValue("icon"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	healthPath := strings.TrimSpace(r.PostFormValue("health_path"))
	sortStr := strings.TrimSpace(r.PostFormValue("sort"))

	if name == "" {
		return nil, "应用名称不能为空"
	}
	if !subdomainRe.MatchString(sub) {
		return nil, "路径只能包含小写字母、数字和短横线"
	}
	if reservedSlugs[sub] {
		return nil, "这个路径被门户占用，请换一个"
	}
	if upstream == "" {
		return nil, "上游地址不能为空，例如 127.0.0.1:8096"
	}
	if healthPath == "" {
		healthPath = "/"
	}
	sortOrder := 100
	if sortStr != "" {
		if v, err := strconv.Atoi(sortStr); err == nil {
			sortOrder = v
		}
	}
	return &store.App{
		Name:        name,
		Subdomain:   sub,
		Upstream:    upstream,
		Icon:        icon,
		Description: desc,
		HealthPath:  healthPath,
		Sort:        sortOrder,
		Enabled:     r.PostFormValue("enabled") != "",
		SafeRewrite: r.PostFormValue("safe_rewrite") != "",
	}, ""
}

func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	id := pathID(r)
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	if _, err := s.st.GetApp(id); err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()

	form, msg := appFromForm(r)
	if msg != "" {
		s.renderAdminApps(w, u, "", msg, fmt.Sprintf("app-edit-%d", id))
		return
	}
	form.ID = id
	if err := s.st.UpdateApp(form); err != nil {
		s.renderAdminApps(w, u, "", "保存失败：路径可能已被占用", fmt.Sprintf("app-edit-%d", id))
		return
	}
	if err := s.st.SetAppAccess(id, formIDs(r.Form["users"])); err != nil {
		s.renderAdminApps(w, u, "", "保存用户授权失败", fmt.Sprintf("app-edit-%d", id))
		return
	}
	s.redirectOK(w, r, "/admin/apps", "应用已保存")
}

func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	id := pathID(r)
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	if err := s.st.DeleteApp(id); err != nil {
		s.renderAdminApps(w, u, "", "删除失败", "")
		return
	}
	s.redirectOK(w, r, "/admin/apps", "应用已删除")
}

// ---------- 管理后台：用户 ----------

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	s.renderAdminUsers(w, u, s.takeFlash(r), "", "")
}

func (s *Server) renderAdminUsers(w http.ResponseWriter, u *store.User, notice, errMsg, openModal string) {
	users, err := s.st.ListUsers()
	if err != nil {
		http.Error(w, "读取用户失败", http.StatusInternalServerError)
		return
	}
	apps, err := s.st.ListApps()
	if err != nil {
		http.Error(w, "读取应用失败", http.StatusInternalServerError)
		return
	}

	// 拼好每个用户的应用授权集合
	accessMap := make(map[int64]map[int64]bool, len(users))
	for _, ux := range users {
		ids, err := s.st.UserAppAccess(ux.ID)
		if err != nil {
			http.Error(w, "读取用户授权失败", http.StatusInternalServerError)
			return
		}
		m := make(map[int64]bool, len(ids))
		for _, aid := range ids {
			m[aid] = true
		}
		accessMap[ux.ID] = m
	}

	d := s.page("用户管理", u)
	d.Users = users
	d.AllApps = apps
	d.UserAppAccess = accessMap
	d.Notice = notice
	d.Error = errMsg
	d.OpenModal = openModal
	s.render(w, "admin_users", d)
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()

	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	role := r.PostFormValue("role")
	if role != "admin" {
		role = "user"
	}
	if username == "" {
		s.renderAdminUsers(w, u, "", "用户名不能为空", "user-new")
		return
	}
	if len([]rune(password)) < 8 {
		s.renderAdminUsers(w, u, "", "密码至少需要 8 个字符", "user-new")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.renderAdminUsers(w, u, "", "密码处理失败", "user-new")
		return
	}
	if _, err := s.st.CreateUser(username, hash, role); err != nil {
		s.renderAdminUsers(w, u, "", "创建失败：用户名可能已存在", "user-new")
		return
	}
	s.redirectOK(w, r, "/admin/users", "用户已创建")
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	id := pathID(r)
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	target, err := s.st.GetUser(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()

	newRole := r.PostFormValue("role")
	if newRole != "admin" {
		newRole = "user"
	}
	newUsername := strings.TrimSpace(r.PostFormValue("username"))
	if newUsername == "" {
		s.renderAdminUsers(w, u, "", "用户名不能为空", fmt.Sprintf("user-edit-%d", id))
		return
	}

	// 新密码留空表示不修改，填了才重置。
	password := r.PostFormValue("password")
	if password != "" && len([]rune(password)) < 8 {
		s.renderAdminUsers(w, u, "", "密码至少需要 8 个字符", fmt.Sprintf("user-edit-%d", id))
		return
	}

	// 用户名查重
	if newUsername != target.Username {
		if existing, err := s.st.GetUserByUsername(newUsername); err == nil && existing.ID != id {
			s.renderAdminUsers(w, u, "", "这个用户名已被占用", fmt.Sprintf("user-edit-%d", id))
			return
		}
		if err := s.st.UpdateUsername(id, newUsername); err != nil {
			s.renderAdminUsers(w, u, "", "保存用户名失败", fmt.Sprintf("user-edit-%d", id))
			return
		}
	}

	// 降级为 admin → user：检查是不是最后一个 admin
	if target.Role == "admin" && newRole != "admin" {
		n, err := s.st.CountAdmins()
		if err == nil && n <= 1 {
			s.renderAdminUsers(w, u, "", "至少需要保留一个管理员", fmt.Sprintf("user-edit-%d", id))
			return
		}
	}
	if err := s.st.UpdateUserRole(id, newRole); err != nil {
		s.renderAdminUsers(w, u, "", "保存角色失败", fmt.Sprintf("user-edit-%d", id))
		return
	}

	// admin 自动拥有所有应用权限，清除 app_access 里的旧条目
	if newRole == "admin" {
		if err := s.st.SetUserAccess(id, nil); err != nil {
			s.renderAdminUsers(w, u, "", "清除授权失败", fmt.Sprintf("user-edit-%d", id))
			return
		}
	} else {
		// user → 保存勾选的应用
		appIDs := formIDs(r.Form["apps"])
		if err := s.st.SetUserAccess(id, appIDs); err != nil {
			s.renderAdminUsers(w, u, "", "保存应用权限失败", fmt.Sprintf("user-edit-%d", id))
			return
		}
	}

	// 填了新密码就一并重置，并让该用户已有的会话全部失效。
	if password != "" {
		hash, err := auth.HashPassword(password)
		if err != nil {
			s.renderAdminUsers(w, u, "", "密码处理失败", fmt.Sprintf("user-edit-%d", id))
			return
		}
		if err := s.st.UpdatePassword(id, hash); err != nil {
			s.renderAdminUsers(w, u, "", "重置密码失败", fmt.Sprintf("user-edit-%d", id))
			return
		}
		_ = s.st.DeleteUserSessions(id)
		s.redirectOK(w, r, "/admin/users", "已更新，密码已重置，该用户需要重新登录")
		return
	}

	s.redirectOK(w, r, "/admin/users", "已更新")
}

func (s *Server) handleUserTOTPReset(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	if err := s.st.SetTOTP(pathID(r), ""); err != nil {
		s.renderAdminUsers(w, u, "", "重置 2FA 失败", "")
		return
	}
	s.redirectOK(w, r, "/admin/users", "已清除该用户的 2FA，下次登录后需重新绑定")
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "来源校验失败", http.StatusForbidden)
		return
	}
	id := pathID(r)
	if id == u.ID {
		s.renderAdminUsers(w, u, "", "不能删除当前登录的账号", "")
		return
	}
	target, err := s.st.GetUser(id)
	if err != nil {
		s.renderAdminUsers(w, u, "", "用户不存在", "")
		return
	}
	if target.Role == "admin" {
		n, err := s.st.CountAdmins()
		if err == nil && n <= 1 {
			s.renderAdminUsers(w, u, "", "至少需要保留一个管理员", "")
			return
		}
	}
	if err := s.st.DeleteUser(id); err != nil {
		s.renderAdminUsers(w, u, "", "删除失败", "")
		return
	}
	_ = s.st.DeleteUserSessions(id)
	s.redirectOK(w, r, "/admin/users", "用户已删除")
}

// ---------- 管理后台：审计 ----------

// auditPageSizes 是审计列表可选的每页条数，默认第一条。
var auditPageSizes = []int{10, 30, 50}

// auditPageSize 取请求里的 size，不在白名单内就回落到默认值。
func auditPageSize(r *http.Request) int {
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	for _, s := range auditPageSizes {
		if s == size {
			return size
		}
	}
	return auditPageSizes[0]
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	u := s.requireAdmin(w, r)
	if u == nil {
		return
	}

	size := auditPageSize(r)
	total, err := s.st.CountAudit()
	if err != nil {
		http.Error(w, "读取审计日志失败", http.StatusInternalServerError)
		return
	}
	pages := (total + size - 1) / size
	if pages == 0 {
		pages = 1
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}

	entries, err := s.st.ListAudit(size, (page-1)*size)
	if err != nil {
		http.Error(w, "读取审计日志失败", http.StatusInternalServerError)
		return
	}

	d := s.page("访问审计", u)
	d.Audit = entries
	d.AuditPage = page
	d.AuditPages = pages
	d.AuditTotal = total
	d.AuditSize = size
	if page > 1 {
		d.PrevPage = page - 1
	}
	if page < pages {
		d.NextPage = page + 1
	}
	s.render(w, "admin_audit", d)
}

// ---------- 小工具 ----------

func pathID(r *http.Request) int64 {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func formIDs(values []string) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}
