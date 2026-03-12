package middleware

import (
	"net/http"

	"github.com/go-chi/cors"
	"github.com/kubecenter/kubecenter/internal/config"
)

// CORS returns a configured CORS middleware.
// In dev mode, allows localhost:8000 (Fresh dev server).
// In production, allows only explicitly configured origins.
func CORS(cfg *config.Config) func(http.Handler) http.Handler {
	// Copy to avoid mutating the config's backing array
	origins := make([]string, len(cfg.CORS.AllowedOrigins))
	copy(origins, cfg.CORS.AllowedOrigins)
	if cfg.Dev {
		origins = append(origins, "http://localhost:8000", "http://127.0.0.1:8000")
	}

	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With", "X-Cluster-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	})
}
