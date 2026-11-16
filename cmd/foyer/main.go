package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"foyer/internal/auth"
	"foyer/internal/config"
	"foyer/internal/store"
	"foyer/internal/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[foyer] ")

	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	if err := bootstrapAdmin(cfg, st); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}

	srv, err := web.New(cfg, st)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go srv.Health().Run(ctx, st, cfg.ProbeInterval)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("监听 %s，门户 %s，数据库 %s，cookie 域 %s", cfg.Addr, cfg.BaseDomain, cfg.DBPath, cookieDesc(cfg.CookieDomain))
	if cfg.BaseDomain == "example.com" && os.Getenv("FOYER_BASE_DOMAIN") == "" {
		log.Printf("警告: 未设置 FOYER_BASE_DOMAIN，正在使用默认 example.com，仅适合本地试跑")
	}
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务异常退出: %v", err)
	}
}

func cookieDesc(domain string) string {
	if domain == "" {
		return "host-only"
	}
	return domain
}

func bootstrapAdmin(cfg config.Config, st *store.Store) error {
	n, err := st.CountUsers()
	if err != nil || n > 0 {
		return err
	}

	password := cfg.AdminPassword
	generated := false
	if password == "" {
		password, err = auth.RandomPassword(16)
		if err != nil {
			return err
		}
		generated = true
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(cfg.AdminUser, hash, "admin"); err != nil {
		return err
	}

	if generated {
		log.Printf("已创建管理员 %s，初始密码: %s（登录后请立即修改，并开启 2FA）", cfg.AdminUser, password)
	} else {
		log.Printf("已创建管理员 %s", cfg.AdminUser)
	}
	return nil
}
