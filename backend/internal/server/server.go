package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// Server holds all dependencies needed by HTTP handlers.
type Server struct {
	Router    *chi.Mux
	Config    *config.Config
	K8sClient *k8s.ClientFactory
	Informers *k8s.InformerManager
	Logger    *slog.Logger
	ready     func() bool
}

// New creates a configured HTTP server with middleware and routes.
func New(cfg *config.Config, k8sClient *k8s.ClientFactory, informers *k8s.InformerManager, logger *slog.Logger, readyFn func() bool) *Server {
	s := &Server{
		Router:    chi.NewRouter(),
		Config:    cfg,
		K8sClient: k8sClient,
		Informers: informers,
		Logger:    logger,
		ready:     readyFn,
	}

	// Middleware chain — order matters
	s.Router.Use(chimw.RequestID)
	s.Router.Use(chimw.RealIP)
	s.Router.Use(slogMiddleware(logger))
	s.Router.Use(chimw.Recoverer)
	s.Router.Use(chimw.CleanPath)
	s.Router.Use(chimw.Timeout(cfg.Server.RequestTimeout))
	s.Router.Use(middleware.CORS(cfg))

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
