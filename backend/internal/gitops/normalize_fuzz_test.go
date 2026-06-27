package gitops

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an *unstructured.Unstructured.
// Inputs that don't decode to a JSON/YAML object are skipped — the seed corpus
// carries the structural diversity and the mutator explores around it.
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: m}, true
}

// FuzzGitOpsNormalizers asserts every Argo CD / Flux CD normalizer and
// inventory/history extractor is crash-safe on arbitrary/adversarial
// unstructured input. Oracle: no panic; zero-values/empty slices are fine.
func FuzzGitOpsNormalizers(f *testing.F) {
	// ---- realistic seeds ----

	// Flux Kustomization (healthy)
	f.Add([]byte(`
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  sourceRef:
    kind: GitRepository
    name: podinfo
  path: ./kustomize
  targetNamespace: default
  suspend: false
status:
  lastAppliedRevision: main@sha1:a1b2c3d4
  lastHandledReconcileAt: "2024-01-15T10:00:00Z"
  conditions:
  - type: Ready
    status: "True"
    reason: ReconciliationSucceeded
    message: "Applied revision: main@sha1:a1b2c3d4"
    lastTransitionTime: "2024-01-15T10:00:00Z"
  inventory:
    entries:
    - id: default_podinfo_apps_Deployment
      v: v1
    - id: default_podinfo__Service
      v: v1
`))

	// Flux Kustomization (stalled + inventory missing)
	f.Add([]byte(`
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: infra
  namespace: flux-system
spec:
  sourceRef:
    kind: GitRepository
    name: infra
  path: ./clusters/prod
  suspend: true
status:
  conditions:
  - type: Stalled
    status: "True"
    reason: BuildFailed
    message: "kustomize build failed: missing resource"
    lastTransitionTime: "2024-01-14T08:00:00Z"
`))

	// Flux HelmRelease (healthy)
	f.Add([]byte(`
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: nginx
  namespace: default
spec:
  chart:
    spec:
      chart: nginx
      version: "15.x"
      sourceRef:
        kind: HelmRepository
        name: bitnami
  targetNamespace: default
  suspend: false
status:
  lastAppliedRevision: "15.3.2"
  lastHandledReconcileAt: "2024-01-15T11:00:00Z"
  conditions:
  - type: Ready
    status: "True"
    reason: InstallSucceeded
    message: "Helm install succeeded for release default/nginx"
    lastTransitionTime: "2024-01-15T11:00:00Z"
  history:
  - chartVersion: "15.3.2"
    status: deployed
    firstDeployed: "2024-01-15T11:00:00Z"
    digest: sha256:abc123
  - chartVersion: "15.3.1"
    status: superseded
    firstDeployed: "2024-01-10T09:00:00Z"
    digest: sha256:def456
`))

	// Flux HelmRelease (reconciling, no history)
	f.Add([]byte(`
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: cert-manager
  namespace: cert-manager
spec:
  chart:
    spec:
      chart: cert-manager
      version: "v1.x"
      sourceRef:
        kind: HelmRepository
        name: jetstack
status:
  conditions:
  - type: Reconciling
    status: "True"
    reason: Progressing
    message: "Running 'upgrade' action"
    lastTransitionTime: "2024-01-15T12:00:00Z"
  - type: Ready
    status: Unknown
    reason: Progressing
    message: "Running 'upgrade' action"
    lastTransitionTime: "2024-01-15T12:00:00Z"
`))

	// Argo CD Application (synced + full status)
	f.Add([]byte(`
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: argocd
spec:
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    path: guestbook
    targetRevision: HEAD
  destination:
    server: https://kubernetes.default.svc
    namespace: guestbook
status:
  sync:
    status: Synced
    revision: a1b2c3d4e5f6
  health:
    status: Healthy
  reconciledAt: "2024-01-15T10:30:00Z"
  operationState:
    message: "successfully synced (all tasks run)"
    phase: Succeeded
    syncResult:
      revision: a1b2c3d4e5f6
  resources:
  - group: apps
    kind: Deployment
    namespace: guestbook
    name: guestbook-ui
    status: Synced
    health:
      status: Healthy
  - group: ""
    kind: Service
    namespace: guestbook
    name: guestbook-ui
    status: Synced
    health:
      status: Healthy
  history:
  - revision: a1b2c3d4e5f6
    deployedAt: "2024-01-15T10:30:00Z"
  - revision: 0000000000ab
    deployedAt: "2024-01-10T08:00:00Z"
`))

	// Argo CD Application (OutOfSync, degraded health, conditions fallback)
	f.Add([]byte(`
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: broken-app
  namespace: argocd
spec:
  source:
    repoURL: https://github.com/example/app
    path: manifests
    targetRevision: main
  destination:
    server: https://kubernetes.default.svc
    namespace: production
status:
  sync:
    status: OutOfSync
    revision: deadbeef
  health:
    status: Degraded
  conditions:
  - type: SyncError
    status: "True"
    message: "cannot apply resource: admission webhook denied"
    lastTransitionTime: "2024-01-15T09:00:00Z"
  resources:
  - kind: Deployment
    namespace: production
    name: api
    status: OutOfSync
    health:
      status: Degraded
`))

	// Argo CD Application (Suspended health)
	f.Add([]byte(`
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: paused-app
  namespace: argocd
spec:
  source:
    repoURL: https://github.com/example/app
    path: charts
    targetRevision: v1.2.3
  destination:
    server: https://kubernetes.default.svc
    namespace: staging
status:
  sync:
    status: Synced
    revision: cafebabe
  health:
    status: Suspended
`))

	// Argo CD ApplicationSet (healthy, git generator)
	f.Add([]byte(`
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: cluster-addons
  namespace: argocd
spec:
  generators:
  - git:
      repoURL: https://github.com/example/gitops
      revision: HEAD
      directories:
      - path: addons/*
  - list:
      elements:
      - cluster: staging
        url: https://staging.example.com
  template:
    spec:
      source:
        repoURL: https://github.com/example/gitops
        path: addons/{{path.basename}}
        targetRevision: HEAD
      destination:
        server: https://kubernetes.default.svc
        namespace: default
  syncPolicy:
    preserveResourcesOnDeletion: true
status:
  conditions:
  - type: ResourcesUpToDate
    status: "True"
    reason: ApplicationSetUpToDate
    message: "All applications are up to date"
    lastTransitionTime: "2024-01-15T10:00:00Z"
`))

	// Argo CD ApplicationSet (error condition, matrix generator)
	f.Add([]byte(`
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: error-appset
  namespace: argocd
spec:
  generators:
  - matrix:
      generators:
      - git:
          repoURL: https://github.com/example/gitops
          revision: HEAD
      - clusters: {}
  template:
    spec:
      source:
        repoURL: https://github.com/example/gitops
        path: apps/{{cluster}}
        targetRevision: HEAD
      destination:
        server: "{{url}}"
        namespace: default
status:
  conditions:
  - type: ErrorOccurred
    status: "True"
    reason: ApplicationGenerationError
    message: "failed to generate applications: template error"
    lastTransitionTime: "2024-01-15T09:00:00Z"
`))

	// ---- malformed/adversarial seeds ----

	// Completely empty object
	f.Add([]byte(`{}`))

	// metadata is a string instead of map
	f.Add([]byte(`{"metadata":"oops"}`))

	// spec is a list, status is a string
	f.Add([]byte(`{"spec":[],"status":"x"}`))

	// inventory entries is a string instead of list
	f.Add([]byte(`{"status":{"inventory":{"entries":"notalist"}}}`))

	// resources is a string, history is a map (not list)
	f.Add([]byte(`{"status":{"resources":"notalist","history":{}}}`))

	// deeply nested nulls
	f.Add([]byte(`{"spec":null,"status":null,"metadata":null}`))

	// conditions is a map instead of slice
	f.Add([]byte(`{"status":{"conditions":{"type":"Ready","status":"True"}}}`))

	// inventory entries contain non-map items
	f.Add([]byte(`{"status":{"inventory":{"entries":["notamap",42,null,true]}}}`))

	// history items that are primitives
	f.Add([]byte(`{"status":{"history":["string",123,null,false]}}`))

	// resources items with wrong types for nested fields
	f.Add([]byte(`{"status":{"resources":[{"group":[],"kind":null,"health":{"status":42}}]}}`))

	// generators is a list of non-maps
	f.Add([]byte(`{"spec":{"generators":["git","list",42,null]}}`))

	// operationState.message is not a string
	f.Add([]byte(`{"status":{"operationState":{"message":{"nested":"object"}}}}`))

	// sync.status is a number
	f.Add([]byte(`{"status":{"sync":{"status":999,"revision":true}}}`))

	// health.status is a list
	f.Add([]byte(`{"status":{"health":{"status":["Healthy","Degraded"]}}}`))

	// Very long strings (path-based)
	f.Add([]byte(`{"spec":{"path":"` + string(make([]byte, 512)) + `"}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}

		// Flux normalizers
		_ = NormalizeFluxKustomization(u)
		_ = NormalizeFluxHelmRelease(u)
		_ = extractFluxInventory(u)
		_ = extractFluxHelmHistory(u)

		// Argo CD normalizers
		_ = NormalizeArgoApp(u)
		_ = extractArgoResources(u)
		_ = extractArgoHistory(u)
		_ = NormalizeArgoAppSet(u)
	})
}
