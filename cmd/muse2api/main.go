package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"muse2api/internal/config"
	"muse2api/internal/muse/browser"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/muse/transport"
	"muse2api/internal/server"
	"muse2api/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load("")
	if err != nil {
		logger.Error("配置加载失败", "error", err)
		os.Exit(1)
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if err := cfg.EnsureDirs(); err != nil {
		logger.Error("数据目录创建失败", "error", err)
		os.Exit(1)
	}
	store, err := storage.OpenWithOptions(cfg.DataDir(), storage.Options{
		CredentialKey:       cfg.CredentialKey,
		AllowInsecure:       cfg.AllowInsecureCredentials,
		KeepLegacyPlaintext: cfg.KeepLegacyPlaintext,
	})
	if err != nil {
		logger.Error("存储初始化失败", "error", err)
		os.Exit(1)
	}
	if stored, keyErr := store.LoadAPIKey(); keyErr != nil {
		logger.Warn("已忽略无法解密的持久化 API Key", "error", keyErr)
	} else if stored != "" {
		cfg.APIKey = stored
	}
	tr, err := transport.NewHTTP(cfg.SiteURL, cfg.HTTPTimeout, cfg.ProxyURL)
	if err != nil {
		logger.Error("上游传输初始化失败", "error", err)
		os.Exit(1)
	}
	var driver protocol.Driver = protocol.NewHTTPDriver(tr)
	generationProtocol := "native-http-unavailable"
	if cfg.BrowserEnabled {
		driver = browser.NewDriver(cfg, store, tr)
		generationProtocol = "go-chromium-cdp"
	}
	app := server.New(cfg, store, driver, logger)
	defer app.Close()
	httpServer := &http.Server{Addr: cfg.Address(), Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("HTTP 优雅退出超时，强制关闭连接", "error", err)
			if closeErr := httpServer.Close(); closeErr != nil {
				logger.Error("HTTP 强制关闭失败", "error", closeErr)
			}
		}
	}()
	logger.Info("Muse2API 已启动", "address", cfg.Address(), "version", "2.0.0", "generation_protocol", generationProtocol, "browser_enabled", cfg.BrowserEnabled)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("服务退出", "error", err)
		os.Exit(1)
	}
	// ListenAndServe returns when Shutdown starts, not when in-flight handlers
	// finish. Wait for graceful shutdown before closing storage and the driver.
	if ctx.Err() != nil {
		<-shutdownDone
	}
}
