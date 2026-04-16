package server

import (
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/wizard"
)

func (s *Server) registerRoutes() {
	// Public routes — no auth, no CSRF
	s.Router.Get("/healthz", s.handleHealthz)
	s.Router.Get("/readyz", s.handleReadyz)

	// WebSocket routes — no timeout middleware (long-lived connections).
	// Auth is handled in-band via the first message, not via middleware.
	if s.Hub != nil {
		s.Router.Get("/api/v1/ws/resources", s.handleWSResources)
	}

	// Hubble flow WebSocket — auth in-band (same as resource WS), no timeout
	if s.NetworkingHandler != nil && s.NetworkingHandler.HubbleClient != nil {
		s.Router.Get("/api/v1/ws/flows", s.handleWSFlows)
	}

	// Loki log search WebSocket — auth in-band, no timeout
	if s.LokiHandler != nil {
		s.Router.Get("/api/v1/ws/logs-search", s.handleWSLogsSearch)
	}

	// Pod log streaming WebSocket — auth in-band (same as resource WS), no timeout
	if s.ResourceHandler != nil {
		s.Router.Get("/api/v1/ws/logs/{namespace}/{pod}/{container}", s.handleWSLogs)
	}

	// Pod exec WebSocket — auth via middleware (not in-band), no timeout
	if s.ResourceHandler != nil {
		s.Router.Group(func(r chi.Router) {
			r.Use(middleware.Auth(s.TokenManager))
			r.Use(middleware.CSRF)
			r.Get("/api/v1/ws/exec/{namespace}/{name}/{container}", s.ResourceHandler.HandlePodExec)
		})
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

			// OIDC routes — redirect-based flow, rate limited
			ar.With(middleware.RateLimit(s.RateLimiter)).Get("/oidc/{providerID}/login", s.handleOIDCLogin)
			ar.With(middleware.RateLimit(s.RateLimiter)).Get("/oidc/{providerID}/callback", s.handleOIDCCallback)
		})

		// Setup — rate limited, no auth
		r.With(middleware.RateLimit(s.RateLimiter)).Post("/setup/init", s.handleSetupInit)
		r.Get("/setup/status", s.handleSetupStatus)

		// Alertmanager webhook — bearer token auth (not JWT), dedicated rate limiter
		if s.AlertingHandler != nil {
			webhookRL := s.WebhookRateLimiter
			if webhookRL == nil {
				webhookRL = s.RateLimiter
			}
			r.With(middleware.RateLimit(webhookRL)).Post("/alerts/webhook", s.AlertingHandler.HandleWebhook)
		}

		// Authenticated routes — auth + CSRF enforced at the group level
		r.Group(func(ar chi.Router) {
			ar.Use(middleware.Auth(s.TokenManager))
			ar.Use(middleware.CSRF)
			ar.Use(middleware.ClusterContext)

			ar.Get("/auth/me", s.handleAuthMe)
			ar.Get("/cluster/info", s.handleClusterInfo)

			// Dashboard summary — aggregated cluster health data
			if s.ResourceHandler != nil {
				ar.Get("/cluster/dashboard-summary", s.ResourceHandler.HandleDashboardSummary)
			}

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

			// Extension (CRD) routes
			if s.CRDHandler != nil {
				s.registerExtensionRoutes(ar)
			}

			// Monitoring routes — only registered if monitoring handler is available
			if s.MonitoringHandler != nil {
				s.registerMonitoringRoutes(ar)
			}

			// Log routes (Loki) — only registered if loki handler is available
			if s.LokiHandler != nil {
				s.registerLogRoutes(ar)
			}

			// Topology routes — only registered if topology handler is available
			if s.TopologyHandler != nil {
				s.registerTopologyRoutes(ar)
			}

			// Diagnostics routes — only registered if diagnostics handler is available
			if s.DiagnosticsHandler != nil {
				s.registerDiagnosticsRoutes(ar)
			}

			// Alerting routes (authenticated) — only registered if alerting handler is available
			if s.AlertingHandler != nil {
				s.registerAlertingRoutes(ar)
			}

			// Policy routes — only registered if policy handler is available
			if s.PolicyHandler != nil {
				s.registerPolicyRoutes(ar)
			}

			// GitOps routes — only registered if gitops handler is available
			if s.GitOpsHandler != nil {
				s.registerGitOpsRoutes(ar)
			}

			// Security scanning routes — only registered if scanning handler is available
			if s.ScanningHandler != nil {
				s.registerScanningRoutes(ar)
			}

			// Namespace limits routes — only registered if limits handler is available
			if s.LimitsHandler != nil {
				s.registerLimitsRoutes(ar)
			}

			// Velero backup/restore routes — only registered if velero handler is available
			if s.VeleroHandler != nil {
				s.registerVeleroRoutes(ar)
			}

			// Cert-Manager routes — only registered if cert-manager handler is available
			if s.CertManagerHandler != nil {
				s.registerCertManagerRoutes(ar)
			}

			// Gateway API routes — only registered if gateway handler is available
			if s.GatewayHandler != nil {
				s.registerGatewayRoutes(ar)
			}

			// Notification center routes
			if s.NotifCenterHandler != nil {
				s.registerNotifCenterRoutes(ar)
			}

			// User management — admin only
			ar.Route("/users", func(ur chi.Router) {
				ur.Use(middleware.RequireAdmin)
				ur.Get("/", s.handleListUsers)
				ur.With(middleware.RateLimit(s.RateLimiter)).Post("/", s.handleCreateUser)
				ur.Delete("/{id}", s.handleDeleteUser)
				ur.Put("/{id}/password", s.handleUpdateUserPassword)
			})

			// Audit log route — admin only
			ar.With(middleware.RequireAdmin).Get("/audit/logs", s.handleAuditLogs)

			// Application settings — admin only
			ar.Route("/settings", func(sr chi.Router) {
				sr.Use(middleware.RequireAdmin)
				sr.Get("/", s.handleGetAppSettings)
				sr.Put("/", s.handleUpdateAppSettings)
			})

			// Auth settings routes — admin only (prevents SSRF via test endpoints)
			ar.Route("/settings/auth", func(sr chi.Router) {
				sr.Use(middleware.RequireAdmin)
				sr.Get("/", s.handleGetAuthSettings)
				sr.Post("/test-oidc", s.handleTestOIDC)
				sr.Post("/test-ldap", s.handleTestLDAP)
			})

			// Cluster management — admin only
			ar.Route("/clusters", func(cr chi.Router) {
				cr.Use(middleware.RequireAdmin)
				cr.Get("/", s.handleListClusters)
				cr.Post("/", s.handleCreateCluster)
				cr.Get("/{clusterID}", s.handleGetCluster)
				cr.Delete("/{clusterID}", s.handleDeleteCluster)
				cr.Post("/{clusterID}/test", s.handleTestCluster)
			})
		})
	})
}

func (s *Server) registerResourceRoutes(ar chi.Router) {
	h := s.ResourceHandler

	// Task polling endpoint (no name/namespace params to validate)
	ar.Get("/tasks/{taskID}", h.HandleGetTask)

	// Batch resource counts (no name/namespace params to validate)
	ar.Get("/resources/counts", h.HandleResourceCounts)

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

		wr.Post("/deployment/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.DeploymentInput{} }))
		wr.Post("/service/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.ServiceInput{} }))
		wr.Post("/storageclass/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.StorageClassInput{} }))
		wr.Post("/rolebinding/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.RoleBindingInput{} }))
		wr.Post("/pvc/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.PVCInput{} }))
		wr.Post("/snapshot/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.SnapshotInput{} }))
		wr.Post("/scheduled-snapshot/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.ScheduledSnapshotInput{} }))
		wr.Post("/configmap/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.ConfigMapInput{} }))
		wr.Post("/secret/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.SecretInput{} }))
		wr.Post("/ingress/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.IngressInput{} }))
		wr.Post("/job/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.JobInput{} }))
		wr.Post("/cronjob/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.CronJobInput{} }))
		wr.Post("/daemonset/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.DaemonSetInput{} }))
		wr.Post("/statefulset/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.StatefulSetInput{} }))
		wr.Post("/networkpolicy/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.NetworkPolicyInput{} }))
		wr.Post("/hpa/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.HPAInput{} }))
		wr.Post("/pdb/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.PDBInput{} }))
		wr.Post("/policy/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.PolicyWizardInput{} }))
		wr.Post("/namespace-limits/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.NamespaceLimitsInput{} }))
		wr.Post("/velero-backup/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.VeleroBackupInput{} }))
		wr.Post("/velero-restore/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.VeleroRestoreInput{} }))
		wr.Post("/velero-schedule/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.VeleroScheduleInput{} }))
		wr.Post("/certificate/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.CertificateInput{} }))
		wr.Post("/issuer/preview", h.HandlePreview(func() wizard.WizardInput {
			return &wizard.IssuerInput{Scope: wizard.IssuerScopeNamespaced}
		}))
		wr.Post("/cluster-issuer/preview", h.HandlePreview(func() wizard.WizardInput {
			return &wizard.IssuerInput{Scope: wizard.IssuerScopeCluster}
		}))
	})
}

func (s *Server) registerMonitoringRoutes(ar chi.Router) {
	h := s.MonitoringHandler
	ar.Route("/monitoring", func(mr chi.Router) {
		// No rate limit on monitoring — read-only, behind auth, Prometheus handles load
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
		sr.Get("/snapshots/{namespace}/{name}", h.HandleGetSnapshot)
		sr.Post("/snapshots/{namespace}", h.HandleCreateSnapshot)
		sr.Delete("/snapshots/{namespace}/{name}", h.HandleDeleteSnapshot)
		sr.Get("/snapshot-classes", h.HandleListSnapshotClasses)
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

		// Hubble flow endpoint (no rate limit — read-only, behind auth)
		nr.Get("/hubble/flows", h.HandleHubbleFlows)

		// Cilium subsystem endpoints — read-only, polled by frontend islands
		nr.Get("/cilium/bgp", h.HandleCiliumBGP)
		nr.Get("/cilium/ipam", h.HandleCiliumIPAM)
		nr.Get("/cilium/subsystems", h.HandleCiliumSubsystems)
		nr.With(middleware.RequireAdmin).Get("/cilium/connectivity", h.HandleCiliumConnectivity)
	})
}

func (s *Server) registerAlertingRoutes(ar chi.Router) {
	h := s.AlertingHandler
	ar.Route("/alerts", func(alr chi.Router) {
		// Share YAML rate limiter (30 req/min) for alerting endpoints
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		alr.Use(middleware.RateLimit(yamlRL))

		alr.Get("/", h.HandleListActive)
		alr.Get("/history", h.HandleListHistory)
		alr.Get("/rules", h.HandleListRules)
		alr.Get("/rules/{namespace}/{name}", h.HandleGetRule)
		alr.Post("/rules", h.HandleCreateRule)
		alr.Put("/rules/{namespace}/{name}", h.HandleUpdateRule)
		alr.Delete("/rules/{namespace}/{name}", h.HandleDeleteRule)
		alr.Get("/settings", h.HandleGetSettings)
		alr.With(middleware.RequireAdmin).Put("/settings", h.HandleUpdateSettings)
		alr.With(middleware.RequireAdmin).Post("/test", h.HandleTestEmail)
	})
}

func (s *Server) registerExtensionRoutes(ar chi.Router) {
	h := s.CRDHandler
	yamlRL := s.YAMLRateLimiter
	if yamlRL == nil {
		yamlRL = s.RateLimiter
	}

	ar.Route("/extensions", func(er chi.Router) {
		// CRD discovery endpoints (read-only)
		er.Get("/crds", h.HandleListCRDs)
		er.Get("/crds/counts", h.HandleCRDCounts)
		er.Get("/crds/{group}/{resource}", h.HandleGetCRD)

		// CRD instance CRUD
		er.Route("/resources/{group}/{resource}", func(cr chi.Router) {
			cr.Get("/", h.HandleListCRDInstances)
			cr.With(middleware.RateLimit(yamlRL)).Post("/-/validate", h.HandleValidateCRDInstance)
			cr.Get("/{ns}", h.HandleListCRDInstances)
			cr.Get("/{ns}/{name}", h.HandleGetCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Post("/{ns}", h.HandleCreateCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Put("/{ns}/{name}", h.HandleUpdateCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Delete("/{ns}/{name}", h.HandleDeleteCRDInstance)
		})
	})
}

func (s *Server) registerTopologyRoutes(ar chi.Router) {
	h := s.TopologyHandler
	ar.Route("/topology", func(tr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		tr.Use(middleware.RateLimit(yamlRL))
		tr.Use(resources.ValidateURLParams)
		tr.Get("/{namespace}", h.HandleNamespaceGraph)
	})
}

func (s *Server) registerLogRoutes(ar chi.Router) {
	h := s.LokiHandler
	ar.Route("/logs", func(lr chi.Router) {
		// Dedicated rate limiter for log queries (30 req/min, separate from write limiter)
		logRL := s.LogQueryLimiter
		if logRL == nil {
			logRL = s.RateLimiter
		}
		lr.Use(middleware.RateLimit(logRL))

		lr.Get("/status", h.HandleStatus)
		lr.Get("/query", h.HandleQuery)
		lr.Get("/labels", h.HandleLabels)
		lr.Get("/labels/{name}/values", h.HandleLabelValues)
		lr.Get("/volume", h.HandleVolume)
	})
}

func (s *Server) registerDiagnosticsRoutes(ar chi.Router) {
	h := s.DiagnosticsHandler
	ar.Route("/diagnostics", func(dr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		dr.Use(middleware.RateLimit(yamlRL))
		dr.Use(resources.ValidateURLParams)
		dr.Get("/{namespace}/summary", h.HandleNamespaceSummary)
		dr.Get("/{namespace}/{kind}/{name}", h.HandleDiagnostics)
	})
}

func (s *Server) registerPolicyRoutes(ar chi.Router) {
	h := s.PolicyHandler
	ar.Route("/policies", func(pr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		pr.Use(middleware.RateLimit(yamlRL))
		pr.Use(resources.ValidateURLParams)
		pr.Get("/status", h.HandleStatus)
		pr.Get("/", h.HandleListPolicies)
		pr.Get("/violations", h.HandleListViolations)
		pr.Get("/compliance", h.HandleCompliance)
		pr.With(middleware.RequireAdmin).Get("/compliance/history", h.HandleComplianceHistory)
	})
}

func (s *Server) registerGitOpsRoutes(ar chi.Router) {
	h := s.GitOpsHandler
	ar.Route("/gitops", func(gr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		gr.Use(middleware.RateLimit(yamlRL))
		gr.Get("/status", h.HandleStatus)
		gr.Get("/applications", h.HandleListApplications)
		gr.Get("/applications/{id}", h.HandleGetApplication)
		gr.Get("/commits", h.HandleGetCommits)

		// Action endpoints
		gr.Post("/applications/{id}/sync", h.HandleSync)
		gr.Post("/applications/{id}/suspend", h.HandleSuspend)
		gr.Post("/applications/{id}/rollback", h.HandleRollback)

		// ApplicationSet endpoints
		gr.Get("/applicationsets", h.HandleListAppSets)
		gr.Get("/applicationsets/{id}", h.HandleGetAppSet)
		gr.Post("/applicationsets/{id}/refresh", h.HandleRefreshAppSet)
		gr.Delete("/applicationsets/{id}", h.HandleDeleteAppSet)

		// Notification routes — only if handler is available
		if s.FluxNotifHandler != nil {
			nh := s.FluxNotifHandler
			gr.Route("/notifications", func(nr chi.Router) {
				nr.Get("/status", nh.HandleStatus)
				nr.Get("/providers", nh.HandleListProviders)
				nr.Get("/alerts", nh.HandleListAlerts)
				nr.Get("/receivers", nh.HandleListReceivers)
				nr.Post("/providers", nh.HandleCreateProvider)
				nr.Post("/alerts", nh.HandleCreateAlert)
				nr.Post("/receivers", nh.HandleCreateReceiver)
				// Parameterized routes — validate namespace/name URL params
				nr.Group(func(vr chi.Router) {
					vr.Use(resources.ValidateURLParams)
					vr.Put("/providers/{namespace}/{name}", nh.HandleUpdateProvider)
					vr.Delete("/providers/{namespace}/{name}", nh.HandleDeleteProvider)
					vr.Post("/providers/{namespace}/{name}/suspend", nh.HandleSuspendProvider)
					vr.Put("/alerts/{namespace}/{name}", nh.HandleUpdateAlert)
					vr.Delete("/alerts/{namespace}/{name}", nh.HandleDeleteAlert)
					vr.Post("/alerts/{namespace}/{name}/suspend", nh.HandleSuspendAlert)
					vr.Put("/receivers/{namespace}/{name}", nh.HandleUpdateReceiver)
					vr.Delete("/receivers/{namespace}/{name}", nh.HandleDeleteReceiver)
					vr.Post("/receivers/{namespace}/{name}/suspend", nh.HandleSuspendReceiver)
				})
			})
		}
	})
}

func (s *Server) registerScanningRoutes(ar chi.Router) {
	h := s.ScanningHandler
	ar.Route("/scanning", func(sr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		sr.Use(middleware.RateLimit(yamlRL))
		sr.Get("/status", h.HandleStatus)
		sr.Get("/vulnerabilities", h.HandleVulnerabilities)
		sr.With(resources.ValidateURLParams).Get("/vulnerabilities/{namespace}/{kind}/{name}", h.HandleVulnerabilityDetail)
	})
}

func (s *Server) registerLimitsRoutes(ar chi.Router) {
	h := s.LimitsHandler
	ar.Route("/limits", func(lr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		lr.Use(middleware.RateLimit(yamlRL))
		lr.Get("/status", h.HandleStatus)
		lr.Get("/namespaces", h.HandleListNamespaces)
		lr.With(resources.ValidateURLParams).Get("/namespaces/{namespace}", h.HandleGetNamespace)
	})
}

func (s *Server) registerVeleroRoutes(ar chi.Router) {
	h := s.VeleroHandler
	ar.Route("/velero", func(vr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		// Status (read-only, no rate limit needed)
		vr.Get("/status", h.HandleStatus)

		// Backups
		vr.Get("/backups", h.HandleListBackups)
		vr.With(resources.ValidateURLParams).Get("/backups/{namespace}/{name}", h.HandleGetBackup)
		vr.With(middleware.RateLimit(yamlRL)).Post("/backups", h.HandleCreateBackup)
		vr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).Delete("/backups/{namespace}/{name}", h.HandleDeleteBackup)
		vr.With(resources.ValidateURLParams).Get("/backups/{namespace}/{name}/logs", h.HandleGetBackupLogs)

		// Restores
		vr.Get("/restores", h.HandleListRestores)
		vr.With(resources.ValidateURLParams).Get("/restores/{namespace}/{name}", h.HandleGetRestore)
		vr.With(middleware.RateLimit(yamlRL)).Post("/restores", h.HandleCreateRestore)

		// Schedules
		vr.Get("/schedules", h.HandleListSchedules)
		vr.With(resources.ValidateURLParams).Get("/schedules/{namespace}/{name}", h.HandleGetSchedule)
		vr.With(middleware.RateLimit(yamlRL)).Post("/schedules", h.HandleCreateSchedule)
		vr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).Put("/schedules/{namespace}/{name}", h.HandleUpdateSchedule)
		vr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).Delete("/schedules/{namespace}/{name}", h.HandleDeleteSchedule)
		vr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).Post("/schedules/{namespace}/{name}/trigger", h.HandleTriggerSchedule)

		// Locations (read-only)
		vr.Get("/locations", h.HandleListLocations)
	})
}

func (s *Server) registerCertManagerRoutes(ar chi.Router) {
	h := s.CertManagerHandler
	ar.Route("/certificates", func(cr chi.Router) {
		// Read endpoints
		cr.Get("/status", h.HandleStatus)
		cr.Get("/certificates", h.HandleListCertificates)
		cr.With(resources.ValidateURLParams).Get("/certificates/{namespace}/{name}", h.HandleGetCertificate)
		cr.Get("/issuers", h.HandleListIssuers)
		cr.Get("/clusterissuers", h.HandleListClusterIssuers)
		cr.Get("/expiring", h.HandleListExpiring)

		// Write endpoints (rate-limited)
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
			Post("/certificates/{namespace}/{name}/renew", h.HandleRenew)
		cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
			Post("/certificates/{namespace}/{name}/reissue", h.HandleReissue)
	})
}

func (s *Server) registerGatewayRoutes(ar chi.Router) {
	h := s.GatewayHandler
	rl := s.YAMLRateLimiter
	if rl == nil {
		rl = s.RateLimiter
	}
	ar.Route("/gateway", func(gr chi.Router) {
		// Cached list endpoints (no direct API calls)
		gr.Get("/status", h.HandleStatus)
		gr.Get("/summary", h.HandleSummary)
		gr.Get("/gatewayclasses", h.HandleListGatewayClasses)
		gr.Get("/gateways", h.HandleListGateways)
		gr.Get("/httproutes", h.HandleListHTTPRoutes)
		gr.Get("/routes", h.HandleListRoutes)

		// Detail endpoints hit the API server directly — rate-limited
		gr.With(middleware.RateLimit(rl), resources.ValidateURLParams).Get("/gatewayclasses/{name}", h.HandleGetGatewayClass)
		gr.With(middleware.RateLimit(rl), resources.ValidateURLParams).Get("/gateways/{namespace}/{name}", h.HandleGetGateway)
		gr.With(middleware.RateLimit(rl), resources.ValidateURLParams).Get("/httproutes/{namespace}/{name}", h.HandleGetHTTPRoute)
		gr.With(middleware.RateLimit(rl), resources.ValidateURLParams).Get("/routes/{kind}/{namespace}/{name}", h.HandleGetRoute)
	})
}

func (s *Server) registerResourceEndpoints(ar chi.Router, h *resources.Handler) {
	// Generic adapter-based routes — any resource registered via Register() in adapter init()
	// is served by these. Per-resource routes below take priority for resources with custom behavior.
	ar.Get("/resources/{kind}", h.HandleListResource)
	ar.Get("/resources/{kind}/{namespace}", h.HandleListResource)
	ar.Get("/resources/{kind}/{namespace}/{name}", h.HandleGetResource)
	ar.Post("/resources/{kind}", h.HandleCreateResource)
	ar.Post("/resources/{kind}/{namespace}", h.HandleCreateResource)
	ar.Put("/resources/{kind}/{namespace}/{name}", h.HandleUpdateResource)
	ar.Delete("/resources/{kind}/{namespace}", h.HandleDeleteResource)
	ar.Delete("/resources/{kind}/{namespace}/{name}", h.HandleDeleteResource)

	// Generic action routes
	ar.Post("/resources/{kind}/{namespace}/{name}/scale", h.HandleScaleResource)
	ar.Post("/resources/{kind}/{namespace}/{name}/restart", h.HandleRestartResource)
	ar.Post("/resources/{kind}/{namespace}/{name}/suspend", h.HandleSuspendResource)
	ar.Post("/resources/{kind}/{namespace}/{name}/trigger", h.HandleTriggerResource)
	ar.Post("/resources/{kind}/{namespace}/{name}/rollback", h.HandleRollbackResource)

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
	ar.Post("/resources/statefulsets/{namespace}/{name}/restart", h.HandleRestartStatefulSet)

	// DaemonSets
	ar.Get("/resources/daemonsets", h.HandleListDaemonSets)
	ar.Get("/resources/daemonsets/{namespace}", h.HandleListDaemonSets)
	ar.Get("/resources/daemonsets/{namespace}/{name}", h.HandleGetDaemonSet)
	ar.Post("/resources/daemonsets/{namespace}", h.HandleCreateDaemonSet)
	ar.Put("/resources/daemonsets/{namespace}/{name}", h.HandleUpdateDaemonSet)
	ar.Delete("/resources/daemonsets/{namespace}/{name}", h.HandleDeleteDaemonSet)
	ar.Post("/resources/daemonsets/{namespace}/{name}/restart", h.HandleRestartDaemonSet)

	// Pods
	ar.Get("/resources/pods", h.HandleListPods)
	ar.Get("/resources/pods/{namespace}", h.HandleListPods)
	ar.Get("/resources/pods/{namespace}/{name}", h.HandleGetPod)
	ar.Delete("/resources/pods/{namespace}/{name}", h.HandleDeletePod)
	ar.Get("/resources/pods/{namespace}/{name}/logs", h.HandlePodLogs)

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

	// Nodes (cluster-scoped)
	ar.Get("/resources/nodes", h.HandleListNodes)
	ar.Get("/resources/nodes/{name}", h.HandleGetNode)
	ar.Post("/resources/nodes/{name}/cordon", h.HandleCordonNode)
	ar.Post("/resources/nodes/{name}/uncordon", h.HandleUncordonNode)
	ar.Post("/resources/nodes/{name}/drain", h.HandleDrainNode)

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
	ar.Post("/resources/jobs/{namespace}/{name}/suspend", h.HandleSuspendJob)

	// CronJobs
	ar.Get("/resources/cronjobs", h.HandleListCronJobs)
	ar.Get("/resources/cronjobs/{namespace}", h.HandleListCronJobs)
	ar.Get("/resources/cronjobs/{namespace}/{name}", h.HandleGetCronJob)
	ar.Post("/resources/cronjobs/{namespace}", h.HandleCreateCronJob)
	ar.Delete("/resources/cronjobs/{namespace}/{name}", h.HandleDeleteCronJob)
	ar.Post("/resources/cronjobs/{namespace}/{name}/suspend", h.HandleSuspendCronJob)
	ar.Post("/resources/cronjobs/{namespace}/{name}/trigger", h.HandleTriggerCronJob)

	// NetworkPolicies
	ar.Get("/resources/networkpolicies", h.HandleListNetworkPolicies)
	ar.Get("/resources/networkpolicies/{namespace}", h.HandleListNetworkPolicies)
	ar.Get("/resources/networkpolicies/{namespace}/{name}", h.HandleGetNetworkPolicy)
	ar.Post("/resources/networkpolicies/{namespace}", h.HandleCreateNetworkPolicy)
	ar.Put("/resources/networkpolicies/{namespace}/{name}", h.HandleUpdateNetworkPolicy)
	ar.Delete("/resources/networkpolicies/{namespace}/{name}", h.HandleDeleteNetworkPolicy)

	// ReplicaSets (read-only — managed by Deployments)
	ar.Get("/resources/replicasets", h.HandleListReplicaSets)
	ar.Get("/resources/replicasets/{namespace}", h.HandleListReplicaSets)
	ar.Get("/resources/replicasets/{namespace}/{name}", h.HandleGetReplicaSet)

	// HorizontalPodAutoscalers
	ar.Get("/resources/hpas", h.HandleListHPAs)
	ar.Get("/resources/hpas/{namespace}", h.HandleListHPAs)
	ar.Get("/resources/hpas/{namespace}/{name}", h.HandleGetHPA)
	ar.Post("/resources/hpas/{namespace}", h.HandleCreateHPA)
	ar.Put("/resources/hpas/{namespace}/{name}", h.HandleUpdateHPA)
	ar.Delete("/resources/hpas/{namespace}/{name}", h.HandleDeleteHPA)

	// PersistentVolumes (cluster-scoped, read-only)
	ar.Get("/resources/pvs", h.HandleListPVs)
	ar.Get("/resources/pvs/{name}", h.HandleGetPV)

	// StorageClasses (cluster-scoped, read-only)
	ar.Get("/resources/storageclasses", h.HandleListStorageClasses)
	ar.Get("/resources/storageclasses/{name}", h.HandleGetStorageClass)

	// Events (read-only)
	ar.Get("/resources/events", h.HandleListEvents)
	ar.Get("/resources/events/{namespace}", h.HandleListEvents)
	ar.Get("/resources/events/{namespace}/{name}", h.HandleGetEvent)

	// Webhook Configurations (cluster-scoped, read-only)
	ar.Get("/resources/validatingwebhookconfigurations", h.HandleListValidatingWebhookConfigurations)
	ar.Get("/resources/validatingwebhookconfigurations/{name}", h.HandleGetValidatingWebhookConfiguration)
	ar.Get("/resources/mutatingwebhookconfigurations", h.HandleListMutatingWebhookConfigurations)
	ar.Get("/resources/mutatingwebhookconfigurations/{name}", h.HandleGetMutatingWebhookConfiguration)

	// ResourceQuotas (read-only)
	ar.Get("/resources/resourcequotas", h.HandleListResourceQuotas)
	ar.Get("/resources/resourcequotas/{namespace}", h.HandleListResourceQuotas)
	ar.Get("/resources/resourcequotas/{namespace}/{name}", h.HandleGetResourceQuota)

	// LimitRanges (read-only)
	ar.Get("/resources/limitranges", h.HandleListLimitRanges)
	ar.Get("/resources/limitranges/{namespace}", h.HandleListLimitRanges)
	ar.Get("/resources/limitranges/{namespace}/{name}", h.HandleGetLimitRange)

	// PodDisruptionBudgets
	ar.Get("/resources/pdbs", h.HandleListPDBs)
	ar.Get("/resources/pdbs/{namespace}", h.HandleListPDBs)
	ar.Get("/resources/pdbs/{namespace}/{name}", h.HandleGetPDB)
	ar.Post("/resources/pdbs/{namespace}", h.HandleCreatePDB)
	ar.Delete("/resources/pdbs/{namespace}/{name}", h.HandleDeletePDB)

	// EndpointSlices (read-only)
	ar.Get("/resources/endpointslices", h.HandleListEndpointSlices)
	ar.Get("/resources/endpointslices/{namespace}", h.HandleListEndpointSlices)
	ar.Get("/resources/endpointslices/{namespace}/{name}", h.HandleGetEndpointSlice)

	// CiliumNetworkPolicies (via dynamic client)
	ar.Get("/resources/ciliumnetworkpolicies", h.HandleListCiliumPolicies)
	ar.Get("/resources/ciliumnetworkpolicies/{namespace}", h.HandleListCiliumPolicies)
	ar.Get("/resources/ciliumnetworkpolicies/{namespace}/{name}", h.HandleGetCiliumPolicy)
	ar.Post("/resources/ciliumnetworkpolicies/{namespace}", h.HandleCreateCiliumPolicy)
	ar.Put("/resources/ciliumnetworkpolicies/{namespace}/{name}", h.HandleUpdateCiliumPolicy)
	ar.Delete("/resources/ciliumnetworkpolicies/{namespace}/{name}", h.HandleDeleteCiliumPolicy)

	// RBAC — Roles and ClusterRoles (read-only), Bindings (full CRUD)
	ar.Get("/resources/roles", h.HandleListRoles)
	ar.Get("/resources/roles/{namespace}", h.HandleListRoles)
	ar.Get("/resources/roles/{namespace}/{name}", h.HandleGetRole)
	ar.Get("/resources/clusterroles", h.HandleListClusterRoles)
	ar.Get("/resources/clusterroles/{name}", h.HandleGetClusterRole)
	ar.Get("/resources/rolebindings", h.HandleListRoleBindings)
	ar.Get("/resources/rolebindings/{namespace}", h.HandleListRoleBindings)
	ar.Get("/resources/rolebindings/{namespace}/{name}", h.HandleGetRoleBinding)
	ar.Post("/resources/rolebindings/{namespace}", h.HandleCreateRoleBinding)
	ar.Put("/resources/rolebindings/{namespace}/{name}", h.HandleUpdateRoleBinding)
	ar.Delete("/resources/rolebindings/{namespace}/{name}", h.HandleDeleteRoleBinding)
	ar.Get("/resources/clusterrolebindings", h.HandleListClusterRoleBindings)
	ar.Get("/resources/clusterrolebindings/{name}", h.HandleGetClusterRoleBinding)
	ar.Post("/resources/clusterrolebindings", h.HandleCreateClusterRoleBinding)
	ar.Put("/resources/clusterrolebindings/{name}", h.HandleUpdateClusterRoleBinding)
	ar.Delete("/resources/clusterrolebindings/{name}", h.HandleDeleteClusterRoleBinding)
}

// registerNotifCenterRoutes registers notification center endpoints.
// Feed endpoints are accessible to all authenticated users.
// Channel and rule management require admin role.
func (s *Server) registerNotifCenterRoutes(ar chi.Router) {
	h := s.NotifCenterHandler
	ar.Route("/notifications", func(r chi.Router) {
		// Feed — all authenticated users
		r.Get("/", h.HandleList)
		r.Post("/{id}/read", h.HandleMarkRead)
		r.Post("/read-all", h.HandleMarkAllRead)
		r.Get("/unread-count", h.HandleUnreadCount)

		// Channels — admin only
		r.Route("/channels", func(cr chi.Router) {
			cr.Use(middleware.RequireAdmin)
			cr.Get("/", h.HandleListChannels)
			cr.Post("/", h.HandleCreateChannel)
			cr.Put("/{id}", h.HandleUpdateChannel)
			cr.Delete("/{id}", h.HandleDeleteChannel)
			cr.Post("/{id}/test", h.HandleTestChannel)
		})

		// Rules — admin only
		r.Route("/rules", func(rr chi.Router) {
			rr.Use(middleware.RequireAdmin)
			rr.Get("/", h.HandleListRules)
			rr.Post("/", h.HandleCreateRule)
			rr.Put("/{id}", h.HandleUpdateRule)
			rr.Delete("/{id}", h.HandleDeleteRule)
		})
	})
}
