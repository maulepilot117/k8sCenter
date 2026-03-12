package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/version"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Set up structured logging
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: cfg.SlogLevel()}
	if cfg.Log.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	v := version.Get()
	logger.Info("starting kubecenter",
		"version", v.Version,
		"commit", v.Commit,
		"go", v.GoVersion,
	)

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClientFactory(cfg.ClusterID, cfg.Dev, logger)
	if err != nil {
		logger.Error("failed to initialize kubernetes client", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Start cache sweeper for impersonating clients
	k8sClient.StartCacheSweeper(ctx)

	// Start informers
	baseCS := k8sClient.BaseClientset()
	informerMgr := k8s.NewInformerManager(baseCS, logger)
	informerMgr.Start(ctx)

	if err := informerMgr.WaitForSync(ctx); err != nil {
		logger.Error("informer sync failed", "error", err)
		os.Exit(1)
	}

	// Initialize JWT signing key
	jwtSecret := []byte(cfg.Auth.JWTSecret)
	if len(jwtSecret) == 0 {
		// Generate a random key if not configured (development mode)
		jwtSecret = make([]byte, 32)
		if _, err := rand.Read(jwtSecret); err != nil {
			logger.Error("failed to generate JWT secret", "error", err)
			os.Exit(1)
		}
		logger.Warn("no JWT secret configured, using random key (tokens will not survive restarts)")
	}

	// Initialize auth components
	tokenManager := auth.NewTokenManager(jwtSecret)
	localAuth := auth.NewLocalProvider(logger)
	sessions := auth.NewSessionStore()
	sessions.StartCleanup(ctx, auth.RefreshTokenLifetime/2)
	rbacChecker := auth.NewRBACChecker(k8sClient, logger)
	auditLogger := audit.NewSlogLogger(logger)
	rateLimiter := middleware.NewRateLimiter()
	rateLimiter.StartCleanup(ctx)

	// Ready state: true after informer sync, false during shutdown
	var ready atomic.Bool
	ready.Store(true)

	// Create HTTP server
	srv := server.New(server.Deps{
		Config:       cfg,
		K8sClient:    k8sClient,
		Informers:    informerMgr,
		Logger:       logger,
		TokenManager: tokenManager,
		LocalAuth:    localAuth,
		Sessions:     sessions,
		RBACChecker:  rbacChecker,
		AuditLogger:  auditLogger,
		RateLimiter:  rateLimiter,
		ReadyFn:      ready.Load,
	})
	httpServer := srv.HTTPServer()

	// Start HTTP server — use errCh instead of os.Exit in goroutine
	// to avoid bypassing defers
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "port", cfg.Server.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("http server error", "error", err)
		stop()
	}

	ready.Store(false)

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}

	logger.Info("kubecenter stopped")
}
