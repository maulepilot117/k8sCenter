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
	"github.com/kubecenter/kubecenter/internal/server/middleware" // used by Deps type
	"github.com/kubecenter/kubecenter/internal/websocket"
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
	Sessions        *auth.SessionStore
	RBACChecker     *auth.RBACChecker
	AuditLogger     audit.Logger
	RateLimiter     *middleware.RateLimiter
	ResourceHandler *resources.Handler
	Hub             *websocket.Hub
	ready           func() bool
}

// Deps holds all dependencies needed to create a Server.
type Deps struct {
	Config        *config.Config
	K8sClient     *k8s.ClientFactory
	Informers     *k8s.InformerManager
	Logger        *slog.Logger
	TokenManager  *auth.TokenManager
	LocalAuth     *auth.LocalProvider
	Sessions      *auth.SessionStore
	RBACChecker   *auth.RBACChecker
	AuditLogger   audit.Logger
	RateLimiter   *middleware.RateLimiter
	Hub           *websocket.Hub
	AccessChecker *resources.AccessChecker
	ReadyFn       func() bool
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
		Sessions:     deps.Sessions,
		RBACChecker:  deps.RBACChecker,
		AuditLogger:  deps.AuditLogger,
		RateLimiter:  deps.RateLimiter,
		Hub:          deps.Hub,
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
