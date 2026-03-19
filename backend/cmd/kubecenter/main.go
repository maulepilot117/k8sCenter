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
	appstore "github.com/kubecenter/kubecenter/internal/store"
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
	informerMgr := k8s.NewInformerManager(baseCS, k8sClient.BaseDynamicClient(), k8sClient.DiscoveryClient(), logger)
	accessChecker := resources.NewAccessChecker(k8sClient, logger)
	accessChecker.StartCacheSweeper(ctx)
	hub := websocket.NewHub(logger, accessChecker)

	// Register dynamic CRD kinds for WebSocket subscriptions (only if CRD detected)
	if informerMgr.CiliumNetworkPolicies() != nil {
		websocket.RegisterAllowedKind("ciliumnetworkpolicies")
	}

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

	// Initialize database, audit logger, settings, and cluster store
	var auditLogger audit.Logger
	var clusterStore *appstore.ClusterStore
	var settingsService *appstore.SettingsService
	var userStore *appstore.UserStore
	var dbPing func(context.Context) error
	if cfg.Database.URL != "" {
		db, err := appstore.New(ctx, cfg.Database.URL, int32(cfg.Database.MaxConns), int32(cfg.Database.MinConns), logger)
		if err != nil {
			logger.Error("failed to connect to database, falling back to slog audit", "error", err)
			auditLogger = audit.NewSlogLogger(logger)
		} else {
			auditStore := audit.NewPostgresStore(db.Pool)
			pgLogger := audit.NewPostgresLogger(auditStore, logger)
			auditLogger = pgLogger
			logger.Info("audit logging to PostgreSQL", "retentionDays", cfg.Audit.RetentionDays)

			// Initialize settings, user, and cluster stores
			dbPing = db.Ping
			userStore = appstore.NewUserStore(db.Pool)
			settingsService = appstore.NewSettingsService(db.Pool)
			encKey := cfg.Database.EncryptionKey
			if encKey == "" {
				encKey = cfg.Auth.JWTSecret // fall back to JWT secret as encryption key
			}
			clusterStore = appstore.NewClusterStore(db.Pool, encKey)

			// Register local cluster
			apiServerHost := "in-cluster"
			if cfg.Dev {
				apiServerHost = "kubeconfig"
			}
			if err := clusterStore.EnsureLocal(ctx, cfg.ClusterID, apiServerHost); err != nil {
				logger.Error("failed to register local cluster", "error", err)
			}

			// Start retention cleanup goroutine
			go func() {
				ticker := time.NewTicker(24 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						deleted, err := pgLogger.Cleanup(ctx, cfg.Audit.RetentionDays)
						if err != nil {
							logger.Error("audit cleanup failed", "error", err)
						} else if deleted > 0 {
							logger.Info("audit cleanup completed", "deleted", deleted)
						}
					}
				}
			}()

			// Close database on shutdown
			defer db.Close()
		}
	} else {
		auditLogger = audit.NewSlogLogger(logger)
	}

	// Initialize auth components (after DB so userStore is available)
	tokenManager := auth.NewTokenManager(jwtSecret)
	if userStore == nil {
		logger.Error("database is required for local user accounts — cannot start without PostgreSQL")
		os.Exit(1)
	}
	localAuth := auth.NewLocalProvider(userStore, logger)
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
		oidcProvider, err := auth.NewOIDCProvider(ctx, oidcCfg, oidcStateStore, logger)
		if err != nil {
			logger.Error("failed to initialize OIDC provider", "id", oidcCfg.ID, "error", err)
			continue
		}
		authRegistry.RegisterOIDC(oidcCfg.ID, oidcProvider)
		logger.Info("registered OIDC provider", "id", oidcCfg.ID, "issuer", oidcCfg.IssuerURL)
	}

	// Register configured LDAP providers
	for _, ldapCfg := range cfg.Auth.LDAP {
		ldapProvider := auth.NewLDAPProvider(ldapCfg, logger)
		authRegistry.RegisterCredential(ldapCfg.ID, ldapCfg.DisplayName, ldapProvider)
		logger.Info("registered LDAP provider", "id", ldapCfg.ID, "url", ldapCfg.URL)
	}

	var rateLimiter *middleware.RateLimiter
	if cfg.Dev {
		rateLimiter = middleware.NewRateLimiterWithRate(60, time.Minute) // relaxed for dev
	} else {
		rateLimiter = middleware.NewRateLimiter() // 5 req/min for production
	}
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

	// Connect to Hubble Relay if detected
	var hubbleClient *networking.HubbleClient
	if cniInfo := cniDetector.CachedInfo(); cniInfo != nil && cniInfo.Features.HubbleRelayAddr != "" {
		hc, err := networking.NewHubbleClient(cniInfo.Features.HubbleRelayAddr)
		if err != nil {
			logger.Warn("failed to connect to hubble relay", "addr", cniInfo.Features.HubbleRelayAddr, "error", err)
		} else {
			hubbleClient = hc
			logger.Info("hubble relay connected", "addr", cniInfo.Features.HubbleRelayAddr)
		}
	}

	networkingHandler := &networking.Handler{
		K8sClient:    k8sClient,
		Detector:     cniDetector,
		HubbleClient: hubbleClient,
		AuditLogger:  auditLogger,
		Logger:       logger,
		ClusterID:    cfg.ClusterID,
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
		AuditLogger:     auditLogger,
		ClusterStore:    clusterStore,
		SettingsService: settingsService,
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
		DBPing:             dbPing,
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
