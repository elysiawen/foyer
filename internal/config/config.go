package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是 Foyer 的全部运行时配置，全部通过环境变量注入。
type Config struct {
	Addr          string        // 监听地址，例如 :8080
	DBPath        string        // SQLite 文件路径
	BaseDomain    string        // 门户域名，例如 abc.com
	CookieDomain  string        // 会话 cookie 域，例如 .abc.com
	SecureCookie  bool          // 是否只通过 HTTPS 下发 cookie
	AdminUser     string        // 首次启动时创建的管理员用户名
	AdminPassword string        // 首次启动时的管理员密码，留空则随机生成
	ProbeInterval time.Duration // 应用健康探测间隔
	SessionTTL    time.Duration // 会话有效期
}

func Load() Config {
	base := strings.TrimPrefix(strings.TrimSpace(env("FOYER_BASE_DOMAIN", "example.com")), ".")

	cfg := Config{
		Addr:          env("FOYER_ADDR", ":8080"),
		DBPath:        env("FOYER_DB", "foyer.db"),
		BaseDomain:    base,
		SecureCookie:  envBool("FOYER_SECURE_COOKIE", true),
		AdminUser:     env("FOYER_ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("FOYER_ADMIN_PASSWORD"),
		ProbeInterval: envDuration("FOYER_PROBE_INTERVAL", 30*time.Second),
		SessionTTL:    envDuration("FOYER_SESSION_TTL", 7*24*time.Hour),
	}

	cfg.CookieDomain = normalizeCookieDomain(env("FOYER_COOKIE_DOMAIN", "."+base))
	return cfg
}

// normalizeCookieDomain 处理本地调试场景：localhost、IP、以及不带点的主机名
// 一律返回空串，表示下发 host-only cookie。浏览器会拒绝 Domain=localhost 的 cookie，
// 表现为“登录成功后马上又回到登录页”。
func normalizeCookieDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	d := strings.TrimPrefix(domain, ".")
	if d == "" || strings.EqualFold(d, "localhost") || net.ParseIP(d) != nil || !strings.Contains(d, ".") {
		return ""
	}
	return domain
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
