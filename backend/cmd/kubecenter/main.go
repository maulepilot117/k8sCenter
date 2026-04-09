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

	_ "go.uber.org/automaxprocs" // Automatically set GOMAXPROCS from cgroup CPU quota

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/alerting"
	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/topology"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/gitprovider"
	"github.com/kubecenter/kubecenter/internal/notification"
	"github.com/kubecenter/kubecenter/internal/scanning"
	"github.com/kubecenter/kubecenter/internal/policy"
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
	informerMgr := k8s.NewInformerManager(baseCS, k8sClient.BaseDynamicClient(), logger)
	accessChecker := resources.NewAccessChecker(k8sClient, logger)
	accessChecker.StartCacheSweeper(ctx)
	hub := websocket.NewHub(logger, accessChecker)

	// Cilium CRD watch — probe once at startup via discovery, use WatchCRD for lifecycle
	if _, err := k8sClient.DiscoveryClient().ServerResourcesForGroupVersion("cilium.io/v2"); err == nil {
		websocket.RegisterAllowedKind("ciliumnetworkpolicies", "cilium.io")
		informerMgr.WatchCRD(ctx, k8s.CiliumPolicyGVR, "ciliumnetworkpolicies", func(obj *unstructured.Unstructured) (any, error) {
			return obj.Object, nil // Cilium policies don't need normalization for the existing ResourceTable consumer
		}, hub.HandleEvent)
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
	var complianceStore *appstore.ComplianceStore
	var dbPing func(context.Context) error
	var dbPool *pgxpool.Pool
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
			dbPool = db.Pool
			userStore = appstore.NewUserStore(db.Pool)
			encKey := cfg.Database.EncryptionKey
			if encKey == "" {
				encKey = cfg.Auth.JWTSecret // fall back to JWT secret as encryption key
			}
			settingsService = appstore.NewSettingsService(db.Pool, encKey)
			clusterStore = appstore.NewClusterStore(db.Pool, encKey)
			complianceStore = appstore.NewComplianceStore(db.Pool)

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

	// Initialize Loki discoverer and start background discovery
	lokiDiscoverer := loki.NewDiscoverer(k8sClient, cfg.Loki, logger)
	go lokiDiscoverer.RunDiscoveryLoop(ctx)

	lokiHandler := &loki.Handler{
		Discoverer:    lokiDiscoverer,
		AccessChecker: accessChecker,
		Logger:        logger,
	}

	logQueryLimiter := middleware.NewRateLimiterWithRate(30, time.Minute)
	logQueryLimiter.StartCleanup(ctx)

	// Initialize topology graph builder
	topoLister := topology.NewInformerLister(informerMgr)
	topoBuilder := topology.NewBuilder(topoLister, logger)
	topoHandler := &topology.Handler{
		Builder:       topoBuilder,
		AccessChecker: accessChecker,
		Logger:        logger,
	}

	// Initialize diagnostics handler
	diagHandler := &diagnostics.Handler{
		Lister:        topoLister,
		TopoBuilder:   topoBuilder,
		AccessChecker: accessChecker,
		Logger:        logger,
	}

	// Initialize CNI detector and run initial detection
	cniDetector := networking.NewDetector(k8sClient, informerMgr, logger)
	cniDetector.Detect(ctx)

	storageHandler := &storage.Handler{
		K8sClient:   k8sClient,
		Informers:   informerMgr,
		AuditLogger: auditLogger,
		Logger:      logger,
		ClusterID:   cfg.ClusterID,
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
		Informers:    informerMgr,
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

	// Multi-cluster routing — always construct (nil store = local-only fallback)
	dbEncKey := cfg.Database.EncryptionKey
	if dbEncKey == "" {
		dbEncKey = cfg.Auth.JWTSecret
	}
	clusterRouter := k8s.NewClusterRouter(k8sClient, clusterStore, dbEncKey, logger)
	clusterRouter.StartCacheSweeper(ctx)

	// Cluster health probing — background goroutine
	var clusterProber *k8s.ClusterProber
	if clusterStore != nil {
		clusterProber = k8s.NewClusterProber(clusterStore, dbEncKey, logger)
		go clusterProber.Run(ctx)
	}

	// CRD Discovery and Handler
	var crdHandler *resources.GenericCRDHandler
	crdDiscovery, err := k8s.NewCRDDiscovery(k8sClient.BaseConfig(), k8sClient.BaseDynamicClient(), logger)
	if err != nil {
		logger.Warn("CRD discovery unavailable", "error", err)
	} else {
		crdDiscovery.Start(ctx)
		crdHandler = &resources.GenericCRDHandler{
			Discovery:     crdDiscovery,
			ClusterRouter: clusterRouter,
			AuditLogger:   auditLogger,
			Logger:        logger,
		}
	}

	// Policy engine discovery and handler
	var policyHandler *policy.Handler
	if crdDiscovery != nil {
		policyDiscoverer := policy.NewDiscoverer(k8sClient, crdDiscovery, logger)

		policyHandler = &policy.Handler{
			K8sClient:     k8sClient,
			Discoverer:    policyDiscoverer,
			ClusterRouter: clusterRouter,
			CRDDiscovery:  crdDiscovery,
			AccessChecker: accessChecker,
			Logger:        logger,
		}

		// Wire Policy CRD watches — Kyverno policies and reports, Gatekeeper constraint templates
		policyDiscoverer.SetOnChange(func(kyvernoAvailable, gatekeeperAvailable bool) {
			if kyvernoAvailable {
				websocket.RegisterAllowedKind("clusterpolicies", "kyverno.io")
				informerMgr.WatchCRD(ctx, policy.KyvernoClusterPolicyGVR, "clusterpolicies", func(obj *unstructured.Unstructured) (any, error) {
					return policy.NormalizeKyvernoPolicy(obj, true), nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					policyHandler.InvalidateCache()
				})

				websocket.RegisterAllowedKind("policies", "kyverno.io")
				informerMgr.WatchCRD(ctx, policy.KyvernoPolicyGVR, "policies", func(obj *unstructured.Unstructured) (any, error) {
					return policy.NormalizeKyvernoPolicy(obj, false), nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					policyHandler.InvalidateCache()
				})

				websocket.RegisterAllowedKind("policyreports", "wgpolicyk8s.io")
				// PolicyReports are watched but not normalized individually — the frontend
				// re-fetches the full violation list on any report change.
				// Send a minimal sentinel (name+namespace) instead of the full CRD object
				// to avoid broadcasting managedFields and cross-namespace refs over WebSocket.
				informerMgr.WatchCRD(ctx, policy.PolicyReportGVR, "policyreports", func(obj *unstructured.Unstructured) (any, error) {
					return map[string]string{"name": obj.GetName(), "namespace": obj.GetNamespace()}, nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					policyHandler.InvalidateCache()
				})

				websocket.RegisterAllowedKind("clusterpolicyreports", "wgpolicyk8s.io")
				informerMgr.WatchCRD(ctx, policy.ClusterPolicyReportGVR, "clusterpolicyreports", func(obj *unstructured.Unstructured) (any, error) {
					return map[string]string{"name": obj.GetName(), "namespace": obj.GetNamespace()}, nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					policyHandler.InvalidateCache()
				})
			} else {
				informerMgr.StopCRD(policy.KyvernoClusterPolicyGVR)
				websocket.UnregisterAllowedKind("clusterpolicies")
				informerMgr.StopCRD(policy.KyvernoPolicyGVR)
				websocket.UnregisterAllowedKind("policies")
				informerMgr.StopCRD(policy.PolicyReportGVR)
				websocket.UnregisterAllowedKind("policyreports")
				informerMgr.StopCRD(policy.ClusterPolicyReportGVR)
				websocket.UnregisterAllowedKind("clusterpolicyreports")
			}
			if gatekeeperAvailable {
				websocket.RegisterAllowedKind("constrainttemplates", "templates.gatekeeper.sh")
				gkTemplateGVR := schema.GroupVersionResource{
					Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates",
				}
				informerMgr.WatchCRD(ctx, gkTemplateGVR, "constrainttemplates", func(obj *unstructured.Unstructured) (any, error) {
					return map[string]string{"name": obj.GetName(), "namespace": obj.GetNamespace()}, nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					policyHandler.InvalidateCache()
				})
			} else {
				gkTemplateGVR := schema.GroupVersionResource{Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates"}
				informerMgr.StopCRD(gkTemplateGVR)
				websocket.UnregisterAllowedKind("constrainttemplates")
			}
		})
		go policyDiscoverer.RunDiscoveryLoop(ctx)

		// Compliance trend snapshotter — records daily scores to PostgreSQL
		if complianceStore != nil {
			complianceRecorder := &policy.ComplianceRecorder{
				Store:     complianceStore,
				Fetcher:   policyHandler,
				ClusterID: cfg.ClusterID,
				Logger:    logger,
			}
			go complianceRecorder.Run(ctx)

			policyHandler.ComplianceStore = complianceStore
		}
	}

	// GitOps discovery and handler
	gitopsDiscoverer := gitops.NewDiscoverer(k8sClient, logger)

	gitopsHandler := &gitops.Handler{
		K8sClient:     k8sClient,
		Discoverer:    gitopsDiscoverer,
		AccessChecker: accessChecker,
		Logger:        logger,
		AuditLogger:   auditLogger,
	}

	// Wire git commit enrichment — always create cache, optionally set GitHub client
	if dbPool != nil {
		commitCache := gitprovider.NewCommitCache(dbPool, nil, logger)
		gitopsHandler.CommitCache = commitCache

		if settingsService != nil {
			if settings, err := settingsService.Get(ctx); err == nil && settings.GitHubToken != nil && *settings.GitHubToken != "" {
				ghClient, err := gitprovider.NewGitHubClient(*settings.GitHubToken, "", logger)
				if err != nil {
					logger.Warn("failed to create github client for commit enrichment", "error", err)
				} else {
					commitCache.SetGitHubClient(ghClient)
					logger.Info("git commit enrichment enabled")
				}
			}
		}
	}

	notificationHandler := &notification.Handler{
		K8sClient:     k8sClient,
		AccessChecker: accessChecker,
		Logger:        logger,
		AuditLogger:   auditLogger,
	}

	// Wire GitOps CRD watches — when tools are discovered, start dynamic informers
	// and register kinds for WebSocket subscriptions. Events invalidate the REST cache.
	gitopsDiscoverer.SetOnChange(func(argoAvailable, fluxAvailable bool) {
		if argoAvailable {
			websocket.RegisterAllowedKind("applications", "argoproj.io")
			informerMgr.WatchCRD(ctx, gitops.ArgoApplicationGVR, "applications", func(obj *unstructured.Unstructured) (any, error) {
				return gitops.NormalizeArgoApp(obj), nil
			}, func(eventType, kind, ns, name string, obj any) {
				hub.HandleEvent(eventType, kind, ns, name, obj)
				gitopsHandler.InvalidateCache()
			})

			websocket.RegisterAllowedKind("applicationsets", "argoproj.io")
			informerMgr.WatchCRD(ctx, gitops.ArgoApplicationSetGVR, "applicationsets", func(obj *unstructured.Unstructured) (any, error) {
				return gitops.NormalizeArgoAppSet(obj), nil
			}, func(eventType, kind, ns, name string, obj any) {
				hub.HandleEvent(eventType, kind, ns, name, obj)
				gitopsHandler.InvalidateAppSetCache()
			})
		} else {
			informerMgr.StopCRD(gitops.ArgoApplicationGVR)
			websocket.UnregisterAllowedKind("applications")
			informerMgr.StopCRD(gitops.ArgoApplicationSetGVR)
			websocket.UnregisterAllowedKind("applicationsets")
		}
		if fluxAvailable {
			websocket.RegisterAllowedKind("kustomizations", "kustomize.toolkit.fluxcd.io")
			informerMgr.WatchCRD(ctx, gitops.FluxKustomizationGVR, "kustomizations", func(obj *unstructured.Unstructured) (any, error) {
				return gitops.NormalizeFluxKustomization(obj), nil
			}, func(eventType, kind, ns, name string, obj any) {
				hub.HandleEvent(eventType, kind, ns, name, obj)
				gitopsHandler.InvalidateCache()
			})

			websocket.RegisterAllowedKind("helmreleases", "helm.toolkit.fluxcd.io")
			informerMgr.WatchCRD(ctx, gitops.FluxHelmReleaseGVR, "helmreleases", func(obj *unstructured.Unstructured) (any, error) {
				return gitops.NormalizeFluxHelmRelease(obj), nil
			}, func(eventType, kind, ns, name string, obj any) {
				hub.HandleEvent(eventType, kind, ns, name, obj)
				gitopsHandler.InvalidateCache()
			})

			// Notification CRD watches — check status directly since callback doesn't carry notification flag
			status := gitopsDiscoverer.Status()
			if status.FluxCD != nil && status.FluxCD.NotificationAvailable {
				websocket.RegisterAllowedKind("flux-providers", "notification.toolkit.fluxcd.io")
				informerMgr.WatchCRD(ctx, notification.FluxProviderGVR, "flux-providers", func(obj *unstructured.Unstructured) (any, error) {
					return notification.NormalizeProvider(obj), nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					notificationHandler.InvalidateProviders()
				})

				websocket.RegisterAllowedKind("flux-alerts", "notification.toolkit.fluxcd.io")
				informerMgr.WatchCRD(ctx, notification.FluxAlertGVR, "flux-alerts", func(obj *unstructured.Unstructured) (any, error) {
					return notification.NormalizeAlert(obj), nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					notificationHandler.InvalidateAlerts()
				})

				websocket.RegisterAllowedKind("flux-receivers", "notification.toolkit.fluxcd.io")
				informerMgr.WatchCRD(ctx, notification.FluxReceiverGVR, "flux-receivers", func(obj *unstructured.Unstructured) (any, error) {
					return notification.NormalizeReceiver(obj), nil
				}, func(eventType, kind, ns, name string, obj any) {
					hub.HandleEvent(eventType, kind, ns, name, obj)
					notificationHandler.InvalidateReceivers()
				})
			} else {
				informerMgr.StopCRD(notification.FluxProviderGVR)
				websocket.UnregisterAllowedKind("flux-providers")
				informerMgr.StopCRD(notification.FluxAlertGVR)
				websocket.UnregisterAllowedKind("flux-alerts")
				informerMgr.StopCRD(notification.FluxReceiverGVR)
				websocket.UnregisterAllowedKind("flux-receivers")
			}
		} else {
			informerMgr.StopCRD(gitops.FluxKustomizationGVR)
			websocket.UnregisterAllowedKind("kustomizations")
			informerMgr.StopCRD(gitops.FluxHelmReleaseGVR)
			websocket.UnregisterAllowedKind("helmreleases")
		}
	})
	go gitopsDiscoverer.RunDiscoveryLoop(ctx)

	// Security scanning discovery and handler
	scanDiscoverer := scanning.NewDiscoverer(k8sClient, logger)
	go scanDiscoverer.RunDiscoveryLoop(ctx)

	scanHandler := &scanning.Handler{
		K8sClient:     k8sClient,
		Discoverer:    scanDiscoverer,
		AccessChecker: accessChecker,
		Logger:        logger,
	}
	scanHandler.InitCache()

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
		ClusterRouter:   clusterRouter,
		ClusterProber:   clusterProber,
		SettingsService: settingsService,
		RateLimiter:     rateLimiter,
		YAMLRateLimiter: yamlRateLimiter,
		Hub:               hub,
		MonitoringHandler:  monHandler,
		LokiHandler:        lokiHandler,
		TopologyHandler:    topoHandler,
		StorageHandler:     storageHandler,
		NetworkingHandler:  networkingHandler,
		AlertingHandler:      alertHandler,
		DiagnosticsHandler:   diagHandler,
		PolicyHandler:        policyHandler,
		GitOpsHandler:        gitopsHandler,
		NotificationHandler:    notificationHandler,
		ScanningHandler:      scanHandler,
		CRDHandler:           crdHandler,
		LogQueryLimiter:    logQueryLimiter,
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
