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
	"time"

	"github.com/kubecenter/kubecenter/internal/alerting"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/server"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/storage"
	"github.com/kubecenter/kubecenter/internal/websocket"
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

	// Create informer manager and WebSocket hub
	baseCS := k8sClient.BaseClientset()
	informerMgr := k8s.NewInformerManager(baseCS, logger)
	accessChecker := resources.NewAccessChecker(k8sClient, logger)
	accessChecker.StartCacheSweeper(ctx)
	hub := websocket.NewHub(logger, accessChecker)

	// Register informer event handlers BEFORE starting informers
	informerMgr.RegisterEventHandlers(hub.HandleEvent)

	// Start WebSocket hub goroutine
	go hub.Run(ctx)

	// Start informers
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
	oidcStateStore := auth.NewOIDCStateStore()
	oidcStateStore.StartCleanup(ctx, time.Minute)

	// Create auth provider registry
	authRegistry := auth.NewProviderRegistry()
	authRegistry.RegisterCredential("local", "Local Accounts", localAuth)

	// Register configured OIDC providers
	for _, oidcCfg := range cfg.Auth.OIDC {
		oidcProviderCfg := auth.OIDCProviderConfig{
			ID:             oidcCfg.ID,
			DisplayName:    oidcCfg.DisplayName,
			IssuerURL:      oidcCfg.IssuerURL,
			ClientID:       oidcCfg.ClientID,
			ClientSecret:   oidcCfg.ClientSecret,
			RedirectURL:    oidcCfg.RedirectURL,
			Scopes:         oidcCfg.Scopes,
			UsernameClaim:  oidcCfg.UsernameClaim,
			GroupsClaim:    oidcCfg.GroupsClaim,
			GroupsPrefix:   oidcCfg.GroupsPrefix,
			AllowedDomains: oidcCfg.AllowedDomains,
			TLSInsecure:    oidcCfg.TLSInsecure,
			CACertPath:     oidcCfg.CACertPath,
		}
		oidcProvider, err := auth.NewOIDCProvider(ctx, oidcProviderCfg, oidcStateStore, logger)
		if err != nil {
			logger.Error("failed to initialize OIDC provider", "id", oidcCfg.ID, "error", err)
			continue // skip this provider but don't prevent startup
		}
		authRegistry.RegisterOIDC(oidcCfg.ID, oidcProvider)
		logger.Info("registered OIDC provider", "id", oidcCfg.ID, "issuer", oidcCfg.IssuerURL)
	}

	// Register configured LDAP providers
	for _, ldapCfg := range cfg.Auth.LDAP {
		ldapProviderCfg := auth.LDAPProviderConfig{
			ID:              ldapCfg.ID,
			DisplayName:     ldapCfg.DisplayName,
			URL:             ldapCfg.URL,
			BindDN:          ldapCfg.BindDN,
			BindPassword:    ldapCfg.BindPassword,
			StartTLS:        ldapCfg.StartTLS,
			TLSInsecure:     ldapCfg.TLSInsecure,
			CACertPath:      ldapCfg.CACertPath,
			UserBaseDN:      ldapCfg.UserBaseDN,
			UserFilter:      ldapCfg.UserFilter,
			UserAttributes:  ldapCfg.UserAttributes,
			GroupBaseDN:     ldapCfg.GroupBaseDN,
			GroupFilter:     ldapCfg.GroupFilter,
			GroupNameAttr:   ldapCfg.GroupNameAttr,
			UsernameMapAttr: ldapCfg.UsernameMapAttr,
			GroupsPrefix:    ldapCfg.GroupsPrefix,
		}
		ldapProvider := auth.NewLDAPProvider(ldapProviderCfg, logger)
		authRegistry.RegisterCredential(ldapCfg.ID, ldapCfg.DisplayName, ldapProvider)
		logger.Info("registered LDAP provider", "id", ldapCfg.ID, "url", ldapCfg.URL)
	}
	auditLogger := audit.NewSlogLogger(logger)
	rateLimiter := middleware.NewRateLimiter()
	rateLimiter.StartCleanup(ctx)
	yamlRateLimiter := middleware.NewRateLimiterWithRate(30, time.Minute)
	yamlRateLimiter.StartCleanup(ctx)

	// Initialize monitoring discoverer and start background discovery
	monDiscoverer := monitoring.NewDiscoverer(k8sClient, cfg.Monitoring, logger)
	go monDiscoverer.RunDiscoveryLoop(ctx)

	monHandler := &monitoring.Handler{
		Discoverer: monDiscoverer,
		Logger:     logger,
	}

	// Initialize CNI detector and run initial detection
	cniDetector := networking.NewDetector(k8sClient, informerMgr, logger)
	cniDetector.Detect(ctx)

	storageHandler := &storage.Handler{
		K8sClient: k8sClient,
		Informers: informerMgr,
		Logger:    logger,
	}

	networkingHandler := &networking.Handler{
		K8sClient:   k8sClient,
		Detector:    cniDetector,
		AuditLogger: auditLogger,
		Logger:      logger,
		ClusterID:   cfg.ClusterID,
	}

	// Initialize alerting
	alertStore := alerting.NewMemoryStore()
	go alertStore.RunPruner(ctx, cfg.Alerting.RetentionDays, logger)

	var alertNotifier *alerting.Notifier
	if cfg.Alerting.SMTP.Host != "" {
		alertNotifier = alerting.NewNotifier(cfg.Alerting.SMTP, cfg.Alerting.SMTP.From, cfg.Alerting.Recipients, cfg.Alerting.RateLimit, logger)
		go alertNotifier.Run(ctx)
	}

	alertRules := alerting.NewRulesManager(k8sClient, logger)

	alertHandler := &alerting.Handler{
		Store:        alertStore,
		Notifier:     alertNotifier,
		Rules:        alertRules,
		Hub:          hub,
		AuditLogger:  auditLogger,
		Logger:       logger,
		ClusterID:    cfg.ClusterID,
		WebhookToken: cfg.Alerting.WebhookToken,
	}
	alertHandler.SetEnabled(cfg.Alerting.Enabled)
	alertHandler.SetConfig(cfg.Alerting)

	webhookRateLimiter := middleware.NewRateLimiterWithRate(300, time.Minute)
	webhookRateLimiter.StartCleanup(ctx)

	// Ready state: true after informer sync, false during shutdown
	var ready atomic.Bool
	ready.Store(true)

	// Create HTTP server
	srv := server.New(server.Deps{
		Config:        cfg,
		K8sClient:     k8sClient,
		Informers:     informerMgr,
		Logger:        logger,
		TokenManager:  tokenManager,
		LocalAuth:     localAuth,
		AuthRegistry:   authRegistry,
		OIDCStateStore: oidcStateStore,
		Sessions:      sessions,
		RBACChecker:   rbacChecker,
		AuditLogger:   auditLogger,
		RateLimiter:     rateLimiter,
		YAMLRateLimiter: yamlRateLimiter,
		Hub:               hub,
		MonitoringHandler:  monHandler,
		StorageHandler:     storageHandler,
		NetworkingHandler:  networkingHandler,
		AlertingHandler:    alertHandler,
		WebhookRateLimiter: webhookRateLimiter,
		AccessChecker:      accessChecker,
		ReadyFn:            ready.Load,
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
