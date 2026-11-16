package health

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"foyer/internal/store"
)

// Status 表示某个应用的最近一次探测结果。
type Status struct {
	Up      bool
	Known   bool
	Checked time.Time
}

// AppSource 只暴露探测需要的应用列表，避免 health 反向依赖 web。
type AppSource interface {
	ListApps() ([]store.App, error)
}

type Checker struct {
	mu sync.RWMutex
	m  map[int64]Status
}

func New() *Checker {
	return &Checker{m: make(map[int64]Status)}
}

func (c *Checker) Set(id int64, up bool) {
	c.mu.Lock()
	c.m[id] = Status{Up: up, Known: true, Checked: time.Now()}
	c.mu.Unlock()
}

func (c *Checker) Get(id int64) Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[id]
}

// Run 周期性探测所有启用的应用，直到 ctx 结束。
func (c *Checker) Run(ctx context.Context, src AppSource, interval time.Duration) {
	c.checkAll(src)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(src)
		}
	}
}

func (c *Checker) checkAll(src AppSource) {
	apps, err := src.ListApps()
	if err != nil {
		return
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		// 应用自己跳转到登录页也算存活，不跟随跳转。
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, a := range apps {
		if !a.Enabled {
			continue
		}
		go func(a store.App) {
			resp, err := client.Get(ProbeURL(a.Upstream, a.HealthPath))
			up := err == nil && resp.StatusCode < 500
			if resp != nil {
				resp.Body.Close()
			}
			c.Set(a.ID, up)
		}(a)
	}
}

func ProbeURL(upstream, path string) string {
	if !strings.Contains(upstream, "://") {
		upstream = "http://" + upstream
	}
	upstream = strings.TrimSuffix(upstream, "/")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return upstream + path
}
