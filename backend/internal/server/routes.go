package server

import (
	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

func (s *Server) registerRoutes() {
	// Public routes — no auth, no CSRF
	s.Router.Get("/healthz", s.handleHealthz)
	s.Router.Get("/readyz", s.handleReadyz)

	s.Router.Route("/api/v1", func(r chi.Router) {
		// Public auth routes — rate limited where needed, no auth required
		r.Route("/auth", func(ar chi.Router) {
			ar.With(middleware.RateLimit(s.RateLimiter)).Post("/login", s.handleLogin)
			ar.With(middleware.RateLimit(s.RateLimiter)).Post("/refresh", s.handleRefresh)
			ar.Post("/logout", s.handleLogout)
			ar.Get("/providers", s.handleAuthProviders)
		})

		// Setup — rate limited, no auth
		r.With(middleware.RateLimit(s.RateLimiter)).Post("/setup/init", s.handleSetupInit)

		// Authenticated routes — auth + CSRF enforced at the group level
		r.Group(func(ar chi.Router) {
			ar.Use(middleware.Auth(s.TokenManager))
			ar.Use(middleware.CSRF)

			ar.Get("/auth/me", s.handleAuthMe)
			ar.Get("/cluster/info", s.handleClusterInfo)
		})
	})
}
