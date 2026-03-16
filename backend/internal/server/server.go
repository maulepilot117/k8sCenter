package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/alerting"
	"github.com/kubecenter/kubecenter/internal/server/middleware" // used by Deps type
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/internal/storage"
	"github.com/kubecenter/kubecenter/internal/websocket"
	"github.com/kubecenter/kubecenter/internal/wizard"
	yamlpkg "github.com/kubecenter/kubecenter/internal/yaml"
)

// Server holds all dependencies needed by HTTP handlers.
type Server struct {
	Router          *chi.Mux
	Config          *config.Config
	K8sClient       *k8s.ClientFactory
	Informers       *k8s.InformerManager
	Logger          *slog.Logger
	TokenManager    *auth.TokenManager
	LocalAuth       *auth.LocalProvider
	AuthRegistry    *auth.ProviderRegistry
	OIDCStateStore  *auth.OIDCStateStore
	Sessions        *auth.SessionStore
	RBACChecker     *auth.RBACChecker
	AuditLogger     audit.Logger
	ClusterStore    *store.ClusterStore
	SettingsService *store.SettingsService
	RateLimiter     *middleware.RateLimiter
	YAMLRateLimiter *middleware.RateLimiter
	ResourceHandler *resources.Handler
	YAMLHandler     *yamlpkg.Handler
	WizardHandler     *wizard.Handler
	MonitoringHandler  *monitoring.Handler
	StorageHandler     *storage.Handler
	NetworkingHandler  *networking.Handler
	AlertingHandler    *alerting.Handler
	Hub                *websocket.Hub
	WebhookRateLimiter *middleware.RateLimiter
	ready              func() bool
}

// Deps holds all dependencies needed to create a Server.
type Deps struct {
	Config        *config.Config
	K8sClient     *k8s.ClientFactory
	Informers     *k8s.InformerManager
	Logger        *slog.Logger
	TokenManager  *auth.TokenManager
	LocalAuth     *auth.LocalProvider
	AuthRegistry    *auth.ProviderRegistry
	OIDCStateStore  *auth.OIDCStateStore
	Sessions      *auth.SessionStore
	RBACChecker   *auth.RBACChecker
	AuditLogger     audit.Logger
	ClusterStore    *store.ClusterStore
	SettingsService *store.SettingsService
	RateLimiter     *middleware.RateLimiter
	YAMLRateLimiter *middleware.RateLimiter
	Hub               *websocket.Hub
	MonitoringHandler  *monitoring.Handler
	StorageHandler     *storage.Handler
	NetworkingHandler  *networking.Handler
	AlertingHandler    *alerting.Handler
	WebhookRateLimiter *middleware.RateLimiter
	AccessChecker      *resources.AccessChecker
	ReadyFn            func() bool
}

// New creates a configured HTTP server with middleware and routes.
func New(deps Deps) *Server {
	s := &Server{
		Router:       chi.NewRouter(),
		Config:       deps.Config,
		K8sClient:    deps.K8sClient,
		Informers:    deps.Informers,
		Logger:       deps.Logger,
		TokenManager: deps.TokenManager,
		LocalAuth:    deps.LocalAuth,
		AuthRegistry:   deps.AuthRegistry,
		OIDCStateStore: deps.OIDCStateStore,
		Sessions:     deps.Sessions,
		RBACChecker:  deps.RBACChecker,
		AuditLogger:     deps.AuditLogger,
		ClusterStore:    deps.ClusterStore,
		SettingsService: deps.SettingsService,
		RateLimiter:     deps.RateLimiter,
		YAMLRateLimiter: deps.YAMLRateLimiter,
		Hub:             deps.Hub,
		ready:        deps.ReadyFn,
	}

	// Build resource handler if k8s dependencies are available (not in auth-only tests)
	if deps.K8sClient != nil && deps.Informers != nil {
		ac := deps.AccessChecker
		if ac == nil {
			// Fallback for tests that don't provide an AccessChecker
			ac = resources.NewAccessChecker(deps.K8sClient, deps.Logger)
		}
		s.ResourceHandler = &resources.Handler{
			K8sClient:     deps.K8sClient,
			Informers:     deps.Informers,
			AccessChecker: ac,
			AuditLogger:   deps.AuditLogger,
			Logger:        deps.Logger,
			TaskManager:   resources.NewTaskManager(),
			ClusterID:     deps.Config.ClusterID,
		}
		s.YAMLHandler = &yamlpkg.Handler{
			K8sClient:   deps.K8sClient,
			AuditLogger: deps.AuditLogger,
			Logger:      deps.Logger,
			ClusterID:   deps.Config.ClusterID,
		}
		s.WizardHandler = &wizard.Handler{
			Logger: deps.Logger,
		}
	}

	// Monitoring handler — wired separately since it doesn't depend on k8s
	// informers (it uses its own discovery goroutine)
	if deps.MonitoringHandler != nil {
		s.MonitoringHandler = deps.MonitoringHandler
	}

	// Storage handler
	if deps.StorageHandler != nil {
		s.StorageHandler = deps.StorageHandler
	}

	// Networking handler
	if deps.NetworkingHandler != nil {
		s.NetworkingHandler = deps.NetworkingHandler
	}

	// Alerting handler
	if deps.AlertingHandler != nil {
		s.AlertingHandler = deps.AlertingHandler
		s.WebhookRateLimiter = deps.WebhookRateLimiter
	}

	// Global middleware chain — order matters.
	// Auth and CSRF are applied per-route-group in registerRoutes(),
	// not globally, so public endpoints don't need a skip list.
	s.Router.Use(chimw.RequestID)
	s.Router.Use(chimw.RealIP)
	s.Router.Use(slogMiddleware(deps.Logger))
	s.Router.Use(chimw.Recoverer)
	s.Router.Use(chimw.CleanPath)
	// Note: Timeout is applied per-route-group (not globally) so that
	// long-lived WebSocket connections are not terminated.
	s.Router.Use(middleware.CORS(deps.Config))

	s.registerRoutes()

	return s
}

// HTTPServer returns a configured *http.Server ready to ListenAndServe.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Config.Server.Port),
		Handler:      s.Router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// slogMiddleware returns a chi-compatible request logging middleware using slog.
func slogMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start),
				"requestID", chimw.GetReqID(r.Context()),
				"remoteAddr", r.RemoteAddr,
			)
		})
	}
}
