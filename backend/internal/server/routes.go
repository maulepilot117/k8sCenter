package server

import (
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

func (s *Server) registerRoutes() {
	// Public routes — no auth, no CSRF
	s.Router.Get("/healthz", s.handleHealthz)
	s.Router.Get("/readyz", s.handleReadyz)

	// WebSocket route — no timeout middleware (long-lived connections).
	// Auth is handled in-band via the first message, not via middleware.
	if s.Hub != nil {
		s.Router.Get("/api/v1/ws/resources", s.handleWSResources)
	}

	s.Router.Route("/api/v1", func(r chi.Router) {
		// Apply timeout to REST routes (not globally, to avoid killing WebSocket connections)
		r.Use(chimw.Timeout(s.Config.Server.RequestTimeout))

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

			// Resource routes — only registered if k8s dependencies are available
			if s.ResourceHandler != nil {
				s.registerResourceRoutes(ar)
			}

			// YAML routes — only registered if k8s dependencies are available
			if s.YAMLHandler != nil {
				s.registerYAMLRoutes(ar)
			}

			// Wizard routes — only registered if wizard handler is available
			if s.WizardHandler != nil {
				s.registerWizardRoutes(ar)
			}

			// Storage routes — only registered if storage handler is available
			if s.StorageHandler != nil {
				s.registerStorageRoutes(ar)
			}

			// Networking routes — only registered if networking handler is available
			if s.NetworkingHandler != nil {
				s.registerNetworkingRoutes(ar)
			}

			// Monitoring routes — only registered if monitoring handler is available
			if s.MonitoringHandler != nil {
				s.registerMonitoringRoutes(ar)
			}
		})
	})
}

func (s *Server) registerResourceRoutes(ar chi.Router) {
	h := s.ResourceHandler

	// Task polling endpoint (no name/namespace params to validate)
	ar.Get("/tasks/{taskID}", h.HandleGetTask)

	// All resource routes validate {name}/{namespace} URL params
	ar.Group(func(rr chi.Router) {
		rr.Use(resources.ValidateURLParams)
		s.registerResourceEndpoints(rr, h)
	})
}

func (s *Server) registerYAMLRoutes(ar chi.Router) {
	h := s.YAMLHandler
	ar.Route("/yaml", func(yr chi.Router) {
		// Rate limit YAML operations with a dedicated, higher-limit bucket
		// (30 req/min) so that validate → diff → apply workflows don't
		// exhaust the stricter auth rate limit (5 req/min).
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		yr.Use(middleware.RateLimit(yamlRL))

		yr.Post("/validate", h.HandleValidate)
		yr.Post("/apply", h.HandleApply)
		yr.Post("/diff", h.HandleDiff)
		yr.Get("/export/{kind}/{namespace}/{name}", h.HandleExport)
	})
}

func (s *Server) registerWizardRoutes(ar chi.Router) {
	h := s.WizardHandler
	ar.Route("/wizards", func(wr chi.Router) {
		// Share YAML rate limiter (30 req/min) for wizard preview endpoints
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		wr.Use(middleware.RateLimit(yamlRL))

		wr.Post("/deployment/preview", h.HandleDeploymentPreview)
		wr.Post("/service/preview", h.HandleServicePreview)
		wr.Post("/storageclass/preview", h.HandleStorageClassPreview)
	})
}

func (s *Server) registerMonitoringRoutes(ar chi.Router) {
	h := s.MonitoringHandler
	ar.Route("/monitoring", func(mr chi.Router) {
		// Share YAML rate limiter (30 req/min) for monitoring endpoints
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		mr.Use(middleware.RateLimit(yamlRL))

		mr.Get("/status", h.HandleStatus)
		mr.Post("/rediscover", h.HandleRediscover)
		mr.Get("/query", h.HandleQuery)
		mr.Get("/query_range", h.HandleQueryRange)
		mr.Get("/dashboards", h.HandleDashboards)
		mr.Get("/templates", h.HandleTemplates)
		mr.Get("/templates/query", h.HandleTemplateQuery)
		mr.Get("/resource-dashboard", h.HandleResourceDashboard)
		mr.HandleFunc("/grafana/proxy/*", h.GrafanaProxy)
	})
}

func (s *Server) registerStorageRoutes(ar chi.Router) {
	h := s.StorageHandler
	ar.Route("/storage", func(sr chi.Router) {
		// Share YAML rate limiter (30 req/min) for storage endpoints
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		sr.Use(middleware.RateLimit(yamlRL))

		sr.Get("/drivers", h.HandleListDrivers)
		sr.Get("/classes", h.HandleListClasses)
		sr.Get("/snapshots", h.HandleListSnapshots)
		sr.Get("/snapshots/{namespace}", h.HandleListSnapshots)
		sr.Get("/presets", h.HandleListPresets)
	})
}

func (s *Server) registerNetworkingRoutes(ar chi.Router) {
	h := s.NetworkingHandler
	ar.Route("/networking", func(nr chi.Router) {
		// Share YAML rate limiter (30 req/min) for networking endpoints
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		nr.Use(middleware.RateLimit(yamlRL))

		nr.Get("/cni", h.HandleCNIStatus)
		nr.Get("/cni/config", h.HandleCNIConfig)
		nr.Put("/cni/config", h.HandleUpdateCNIConfig)
	})
}

func (s *Server) registerResourceEndpoints(ar chi.Router, h *resources.Handler) {
	// Deployments
	ar.Get("/resources/deployments", h.HandleListDeployments)
	ar.Get("/resources/deployments/{namespace}", h.HandleListDeployments)
	ar.Get("/resources/deployments/{namespace}/{name}", h.HandleGetDeployment)
	ar.Post("/resources/deployments/{namespace}", h.HandleCreateDeployment)
	ar.Put("/resources/deployments/{namespace}/{name}", h.HandleUpdateDeployment)
	ar.Delete("/resources/deployments/{namespace}/{name}", h.HandleDeleteDeployment)
	ar.Post("/resources/deployments/{namespace}/{name}/scale", h.HandleScaleDeployment)
	ar.Post("/resources/deployments/{namespace}/{name}/rollback", h.HandleRollbackDeployment)
	ar.Post("/resources/deployments/{namespace}/{name}/restart", h.HandleRestartDeployment)

	// StatefulSets
	ar.Get("/resources/statefulsets", h.HandleListStatefulSets)
	ar.Get("/resources/statefulsets/{namespace}", h.HandleListStatefulSets)
	ar.Get("/resources/statefulsets/{namespace}/{name}", h.HandleGetStatefulSet)
	ar.Post("/resources/statefulsets/{namespace}", h.HandleCreateStatefulSet)
	ar.Put("/resources/statefulsets/{namespace}/{name}", h.HandleUpdateStatefulSet)
	ar.Delete("/resources/statefulsets/{namespace}/{name}", h.HandleDeleteStatefulSet)
	ar.Post("/resources/statefulsets/{namespace}/{name}/scale", h.HandleScaleStatefulSet)

	// DaemonSets
	ar.Get("/resources/daemonsets", h.HandleListDaemonSets)
	ar.Get("/resources/daemonsets/{namespace}", h.HandleListDaemonSets)
	ar.Get("/resources/daemonsets/{namespace}/{name}", h.HandleGetDaemonSet)
	ar.Post("/resources/daemonsets/{namespace}", h.HandleCreateDaemonSet)
	ar.Put("/resources/daemonsets/{namespace}/{name}", h.HandleUpdateDaemonSet)
	ar.Delete("/resources/daemonsets/{namespace}/{name}", h.HandleDeleteDaemonSet)

	// Pods
	ar.Get("/resources/pods", h.HandleListPods)
	ar.Get("/resources/pods/{namespace}", h.HandleListPods)
	ar.Get("/resources/pods/{namespace}/{name}", h.HandleGetPod)
	ar.Delete("/resources/pods/{namespace}/{name}", h.HandleDeletePod)

	// Services
	ar.Get("/resources/services", h.HandleListServices)
	ar.Get("/resources/services/{namespace}", h.HandleListServices)
	ar.Get("/resources/services/{namespace}/{name}", h.HandleGetService)
	ar.Post("/resources/services/{namespace}", h.HandleCreateService)
	ar.Put("/resources/services/{namespace}/{name}", h.HandleUpdateService)
	ar.Delete("/resources/services/{namespace}/{name}", h.HandleDeleteService)

	// Ingresses
	ar.Get("/resources/ingresses", h.HandleListIngresses)
	ar.Get("/resources/ingresses/{namespace}", h.HandleListIngresses)
	ar.Get("/resources/ingresses/{namespace}/{name}", h.HandleGetIngress)
	ar.Post("/resources/ingresses/{namespace}", h.HandleCreateIngress)
	ar.Put("/resources/ingresses/{namespace}/{name}", h.HandleUpdateIngress)
	ar.Delete("/resources/ingresses/{namespace}/{name}", h.HandleDeleteIngress)

	// Namespaces (cluster-scoped)
	ar.Get("/resources/namespaces", h.HandleListNamespaces)
	ar.Get("/resources/namespaces/{name}", h.HandleGetNamespace)
	ar.Post("/resources/namespaces", h.HandleCreateNamespace)
	ar.Delete("/resources/namespaces/{name}", h.HandleDeleteNamespace)

	// Nodes (cluster-scoped)
	ar.Get("/resources/nodes", h.HandleListNodes)
	ar.Get("/resources/nodes/{name}", h.HandleGetNode)
	ar.Post("/resources/nodes/{name}/cordon", h.HandleCordonNode)
	ar.Post("/resources/nodes/{name}/uncordon", h.HandleUncordonNode)
	ar.Post("/resources/nodes/{name}/drain", h.HandleDrainNode)

	// ConfigMaps
	ar.Get("/resources/configmaps", h.HandleListConfigMaps)
	ar.Get("/resources/configmaps/{namespace}", h.HandleListConfigMaps)
	ar.Get("/resources/configmaps/{namespace}/{name}", h.HandleGetConfigMap)
	ar.Post("/resources/configmaps/{namespace}", h.HandleCreateConfigMap)
	ar.Put("/resources/configmaps/{namespace}/{name}", h.HandleUpdateConfigMap)
	ar.Delete("/resources/configmaps/{namespace}/{name}", h.HandleDeleteConfigMap)

	// Secrets
	ar.Get("/resources/secrets", h.HandleListSecrets)
	ar.Get("/resources/secrets/{namespace}", h.HandleListSecrets)
	ar.Get("/resources/secrets/{namespace}/{name}", h.HandleGetSecret)
	ar.Get("/resources/secrets/{namespace}/{name}/reveal/{key}", h.HandleRevealSecret)
	ar.Post("/resources/secrets/{namespace}", h.HandleCreateSecret)
	ar.Put("/resources/secrets/{namespace}/{name}", h.HandleUpdateSecret)
	ar.Delete("/resources/secrets/{namespace}/{name}", h.HandleDeleteSecret)

	// PVCs
	ar.Get("/resources/pvcs", h.HandleListPVCs)
	ar.Get("/resources/pvcs/{namespace}", h.HandleListPVCs)
	ar.Get("/resources/pvcs/{namespace}/{name}", h.HandleGetPVC)
	ar.Post("/resources/pvcs/{namespace}", h.HandleCreatePVC)
	ar.Delete("/resources/pvcs/{namespace}/{name}", h.HandleDeletePVC)

	// Jobs
	ar.Get("/resources/jobs", h.HandleListJobs)
	ar.Get("/resources/jobs/{namespace}", h.HandleListJobs)
	ar.Get("/resources/jobs/{namespace}/{name}", h.HandleGetJob)
	ar.Post("/resources/jobs/{namespace}", h.HandleCreateJob)
	ar.Delete("/resources/jobs/{namespace}/{name}", h.HandleDeleteJob)

	// CronJobs
	ar.Get("/resources/cronjobs", h.HandleListCronJobs)
	ar.Get("/resources/cronjobs/{namespace}", h.HandleListCronJobs)
	ar.Get("/resources/cronjobs/{namespace}/{name}", h.HandleGetCronJob)
	ar.Post("/resources/cronjobs/{namespace}", h.HandleCreateCronJob)
	ar.Delete("/resources/cronjobs/{namespace}/{name}", h.HandleDeleteCronJob)

	// NetworkPolicies
	ar.Get("/resources/networkpolicies", h.HandleListNetworkPolicies)
	ar.Get("/resources/networkpolicies/{namespace}", h.HandleListNetworkPolicies)
	ar.Get("/resources/networkpolicies/{namespace}/{name}", h.HandleGetNetworkPolicy)
	ar.Post("/resources/networkpolicies/{namespace}", h.HandleCreateNetworkPolicy)
	ar.Put("/resources/networkpolicies/{namespace}/{name}", h.HandleUpdateNetworkPolicy)
	ar.Delete("/resources/networkpolicies/{namespace}/{name}", h.HandleDeleteNetworkPolicy)

	// Events (read-only)
	ar.Get("/resources/events", h.HandleListEvents)
	ar.Get("/resources/events/{namespace}", h.HandleListEvents)
	ar.Get("/resources/events/{namespace}/{name}", h.HandleGetEvent)

	// RBAC Viewer (read-only)
	ar.Get("/resources/roles", h.HandleListRoles)
	ar.Get("/resources/roles/{namespace}", h.HandleListRoles)
	ar.Get("/resources/roles/{namespace}/{name}", h.HandleGetRole)
	ar.Get("/resources/clusterroles", h.HandleListClusterRoles)
	ar.Get("/resources/clusterroles/{name}", h.HandleGetClusterRole)
	ar.Get("/resources/rolebindings", h.HandleListRoleBindings)
	ar.Get("/resources/rolebindings/{namespace}", h.HandleListRoleBindings)
	ar.Get("/resources/rolebindings/{namespace}/{name}", h.HandleGetRoleBinding)
	ar.Get("/resources/clusterrolebindings", h.HandleListClusterRoleBindings)
	ar.Get("/resources/clusterrolebindings/{name}", h.HandleGetClusterRoleBinding)
}
