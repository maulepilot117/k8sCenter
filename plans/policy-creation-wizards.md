# feat: Policy Creation Wizards

## Overview

Add wizard-based UI for creating 8 common Kyverno and Gatekeeper policies across 4 categories. Builds on the existing wizard infrastructure (WizardInput interface, HandlePreview, WizardStepper, WizardReviewStep) and the existing policy integration (PolicyDiscoverer, Kyverno/Gatekeeper adapters).

The wizard generates YAML applied through the existing `/yaml/apply` endpoint (SSA with user impersonation). No new apply infrastructure needed.

## Problem Statement

k8sCenter can read and display policies (Phase 8) but cannot create them. Users must manually write Kyverno ClusterPolicy or Gatekeeper ConstraintTemplate+Constraint YAML — error-prone and requiring deep knowledge of each engine's schema. A wizard-driven approach lowers the barrier to policy adoption while generating correct, best-practice YAML.

## Architecture

### Design Decisions

| Decision | Choice | Why |
|---|---|---|
| Template metadata | Frontend TypeScript constants + backend template registry | Static data, single source of truth per layer |
| Backend parameter model | Common fields + `Params json.RawMessage` with per-template structs | Type-safe, extensible, adding templates never touches shared struct |
| Gatekeeper apply | Retry-on-discovery-error in existing `ApplyDocuments` | No new endpoint; reuses existing apply infrastructure |
| Rego storage | `go:embed` .rego files | Readable, testable with OPA tooling, clean Go code |
| Rego parameterization | `input.parameters` for all dynamic values; Rego is static | Prevents Rego injection; matches Gatekeeper library conventions |
| Kyverno scope | ClusterPolicy only (v1) | Namespaced Policy adds RBAC complexity; add later |
| Kyverno format | Legacy `kyverno.io/v1 ClusterPolicy` | Most clusters run pre-1.17; CEL format is a future option |
| Wizard steps | 3 steps (Template, Configuration + Engine, Review) | Matches existing wizard conventions; engine selector is just a config field |
| RBAC gating | Hide "Create Policy" button for non-admin users | ClusterPolicy/ConstraintTemplate require cluster-level RBAC |
| Cache invalidation | Rely on existing 30s TTL | No new plumbing; acceptable delay |

### 8 Policy Templates (4 Categories)

**A. Pod Security (4)**

| # | ID | Name | Severity | Kyverno | Gatekeeper | Parameters |
|---|---|---|---|---|---|---|
| 1 | `disallow-privileged` | Disallow Privileged Containers | high | validate pattern | K8sPSPPrivilegedContainer | none |
| 2 | `disallow-root` | Disallow Root User | high | validate pattern | K8sPSPAllowedUsers | none |
| 3 | `disallow-privilege-escalation` | Disallow Privilege Escalation | high | validate pattern | K8sPSPAllowPrivilegeEscalation | none |
| 4 | `restrict-capabilities` | Restrict Capabilities | medium | validate pattern | K8sPSPCapabilities | `dropAll` (bool, default true), `allowedAdd` ([]string) |

**B. Image Policies (2)**

| # | ID | Name | Severity | Parameters |
|---|---|---|---|---|
| 5 | `allowed-registries` | Restrict Image Registries | high | `registries` ([]string, prefix patterns) |
| 6 | `disallow-latest-tag` | Disallow Latest Tag | medium | none |

**C. Resource Management (1)**

| # | ID | Name | Severity | Parameters |
|---|---|---|---|---|
| 7 | `require-resource-limits` | Require Resource Limits | medium | `requireCPU` (bool), `requireMemory` (bool) |

**D. Labeling (1)**

| # | ID | Name | Severity | Parameters |
|---|---|---|---|---|
| 8 | `require-labels` | Require Labels | medium | `labels` ([]string, required label keys) |

**Deferred to follow-up (9 templates):**
- `disallow-host-namespaces`, `require-readonly-rootfs` (pod security, validate — easy to add)
- `require-image-digest` (low severity, niche)
- `require-resource-requests`, `max-resource-limits` (variations of #7)
- `restrict-label-values` (advanced regex constraints)
- `default-deny-network`, `require-namespace-quota` (Kyverno `generate` rules — architecturally different)
- `restrict-service-types` (medium value, can wait)

### Common Fields (All Templates)

| Field | Type | Default | Validation |
|---|---|---|---|
| `name` | string | Auto-generated from template ID | DNS label regex, max 63 chars |
| `action` | enum | high → `Enforce`/`deny`; medium → `Audit`/`dryrun` | Must be valid for selected engine |
| `engine` | enum | Auto-detected (or user-selected) | `"kyverno"` or `"gatekeeper"` |
| `targetKinds` | []string | Template default (usually `["Pod"]`) | PascalCase k8s kind names |
| `excludedNamespaces` | []string | `["kube-system", "kube-public", "kube-node-lease"]` | Valid DNS labels |
| `description` | string | Template default description | Optional override |

### Wizard UX Flow (3 Steps)

```
Step 1: Template Selection
  ├── 4 categories as collapsible sections (accordion)
  ├── Each template: name, description, severity badge, engine icons (Both)
  └── Click to select → advance to Step 2

Step 2: Configuration
  ├── Engine selector (radio buttons, or auto-selected if only one available)
  ├── Common fields: name (auto-filled), action dropdown, description textarea
  ├── Target Kinds multi-select chips, Excluded Namespaces editable tag input
  └── Template-specific parameters rendered conditionally based on templateId

Step 3: Review & Apply
  ├── YAML preview fetched on entry via POST /api/v1/wizards/policy/preview
  ├── Editable YAML in Monaco editor (via existing WizardReviewStep)
  ├── Apply button → POST /api/v1/yaml/apply (both engines, same endpoint)
  ├── For Gatekeeper: multi-doc YAML (ConstraintTemplate + Constraint)
  └── Success: link to /security/policies
```

### Edge Cases

| Scenario | Behavior |
|---|---|
| No engine installed | "Create Policy" button hidden. Direct URL shows blocked state with installation links. |
| Both engines installed | Engine radio selector in Step 2. Default to engine with more existing policies. |
| Single engine installed | Auto-selected with read-only label in Step 2. |
| RBAC denied at apply | Step 3 shows inline error (existing WizardReviewStep behavior). |
| Gatekeeper ConstraintTemplate already exists | SSA returns "unchanged" for template, "created" for constraint. |
| Gatekeeper CRD race | Retry-on-discovery-error in ApplyDocuments (3 retries, 2s apart). |
| Policy name collision | SSA returns "configured" (updates existing). |
| Browser refresh mid-wizard | State lost, restart from Step 1 (matches existing wizard behavior). |
| Multi-cluster | Wizard uses active X-Cluster-ID for both status check and apply. |

---

## Implementation Phases

### Phase 1: Backend — Input, Validation & YAML Generation

**Files to create:**

| File | Purpose | LOC Est. |
|---|---|---|
| `backend/internal/wizard/policy_input.go` | `PolicyWizardInput` + template registry + per-template param structs | ~300 |
| `backend/internal/wizard/policy_kyverno.go` | Kyverno ClusterPolicy YAML generation for 8 templates | ~300 |
| `backend/internal/wizard/policy_gatekeeper.go` | Gatekeeper ConstraintTemplate + Constraint YAML generation | ~350 |
| `backend/internal/wizard/rego/` | Embedded `.rego` files for 8 Gatekeeper constraint templates | ~200 |
| `backend/internal/wizard/policy_input_test.go` | Validation + YAML golden-file tests (8 templates x 2 engines) | ~300 |

**Files to modify:**

| File | Change |
|---|---|
| `backend/internal/server/routes.go` | Add `POST /wizards/policy/preview` route (1 line) |
| `backend/internal/yaml/applier.go` | Add retry-on-discovery-error for CRD race condition |

**Key implementation details:**

```go
// PolicyWizardInput — common fields + raw params dispatched by templateId
type PolicyWizardInput struct {
    TemplateID         string          `json:"templateId"`
    Engine             string          `json:"engine"`
    Name               string          `json:"name"`
    Action             string          `json:"action"`
    TargetKinds        []string        `json:"targetKinds"`
    ExcludedNamespaces []string        `json:"excludedNamespaces"`
    Description        string          `json:"description"`
    Params             json.RawMessage `json:"params"`
}

// Per-template param structs (unmarshaled from Params based on TemplateID)
type CapabilityParams struct {
    DropAll    bool     `json:"dropAll"`
    AllowedAdd []string `json:"allowedAdd"`
}

type RegistryParams struct {
    Registries []string `json:"registries"`
}

type ResourceLimitParams struct {
    RequireCPU    bool `json:"requireCpu"`
    RequireMemory bool `json:"requireMemory"`
}

type RequireLabelsParams struct {
    Labels []string `json:"labels"`
}
```

Gatekeeper Rego files embedded via `go:embed`:
```go
//go:embed rego/*.rego
var regoFS embed.FS
```

For Gatekeeper multi-doc YAML, `ToYAML()` returns ConstraintTemplate + `---` + Constraint. The existing `ApplyDocuments` applies each doc sequentially. Added retry logic handles the CRD discovery race.

**Acceptance criteria:**
- [ ] All 8 templates generate valid YAML for both engines
- [ ] `Validate()` catches: empty name, invalid DNS label, unknown template ID, missing required params
- [ ] Kyverno YAML includes correct annotations (`policies.kyverno.io/*`)
- [ ] Gatekeeper YAML includes both ConstraintTemplate and Constraint as multi-doc
- [ ] Gatekeeper Rego uses `input.parameters` exclusively (no injection)
- [ ] Golden-file tests for all 8 templates x 2 engines = 16 YAML generation tests
- [ ] Validation edge case tests (empty registries, invalid caps, etc.)
- [ ] `go vet ./...` passes

---

### Phase 2: Frontend — PolicyWizard Island & Route

**Files to create:**

| File | Purpose | LOC Est. |
|---|---|---|
| `frontend/islands/PolicyWizard.tsx` | Main wizard island: 3-step flow with signals state | ~450 |
| `frontend/components/wizard/PolicyTemplateStep.tsx` | Step 1: Template selection with category accordions | ~200 |
| `frontend/components/wizard/PolicyConfigStep.tsx` | Step 2: Common fields + engine selector + template params | ~350 |
| `frontend/lib/policy-templates.ts` | TypeScript constants: 8 template definitions with metadata | ~150 |
| `frontend/routes/security/create-policy.tsx` | Route file rendering PolicyWizard island | ~20 |

**Files to modify:**

| File | Change |
|---|---|
| `frontend/islands/PolicyDashboard.tsx` | Add "Create Policy" button (admin-only, hidden when noEngine) |
| `frontend/lib/constants.ts` | Add command palette entry for "Create Policy" |

**Island state shape:**
```tsx
interface PolicyWizardForm {
  templateId: string;
  engine: "kyverno" | "gatekeeper";
  name: string;
  action: string;
  targetKinds: string[];
  excludedNamespaces: string[];
  description: string;
  params: Record<string, unknown>;
}
```

**Acceptance criteria:**
- [ ] Form state preserved on back-navigation between steps 1-2
- [ ] Engine auto-detected via `GET /policy/status` on island mount
- [ ] Single-engine: auto-selected with read-only label
- [ ] No-engine: blocked state with installation guidance
- [ ] `useDirtyGuard` prevents accidental navigation away
- [ ] All 8 template-specific param forms render with inline validation
- [ ] Apply uses single `/yaml/apply` endpoint for both engines
- [ ] Success result links to `/security/policies`
- [ ] "Create Policy" button in PolicyDashboard: visible for admins, hidden when noEngine
- [ ] Command palette "Create Policy" action
- [ ] Theme-compliant: CSS custom properties for all colors
- [ ] `deno lint` passes

---

## File Summary

### New Files (9 + rego/)

| File | Phase |
|---|---|
| `backend/internal/wizard/policy_input.go` | 1 |
| `backend/internal/wizard/policy_kyverno.go` | 1 |
| `backend/internal/wizard/policy_gatekeeper.go` | 1 |
| `backend/internal/wizard/rego/*.rego` (8 files) | 1 |
| `backend/internal/wizard/policy_input_test.go` | 1 |
| `frontend/islands/PolicyWizard.tsx` | 2 |
| `frontend/components/wizard/PolicyTemplateStep.tsx` | 2 |
| `frontend/components/wizard/PolicyConfigStep.tsx` | 2 |
| `frontend/lib/policy-templates.ts` | 2 |
| `frontend/routes/security/create-policy.tsx` | 2 |

### Modified Files (3)

| File | Phase | Change |
|---|---|---|
| `backend/internal/server/routes.go` | 1 | Add 1 wizard route |
| `backend/internal/yaml/applier.go` | 1 | Retry-on-discovery-error for CRD race |
| `frontend/islands/PolicyDashboard.tsx` | 2 | Add "Create Policy" button |
| `frontend/lib/constants.ts` | 2 | Command palette entry |

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Gatekeeper CRD race condition | Constraint apply fails after ConstraintTemplate | Retry in ApplyDocuments (3x, 2s backoff) |
| Kyverno 1.17 CEL format | Generated YAML may be deprecated for newest clusters | Legacy `ClusterPolicy` v1 (universally supported); CEL toggle later |
| Non-admin users hit RBAC wall | Wasted user effort | Hide "Create Policy" for non-admin |
| Rego injection | Security vulnerability | All dynamic values via `input.parameters`; Rego is static `go:embed` |

## Future Enhancements

- 9 additional templates (host namespaces, readonly rootfs, resource requests, max limits, label values, generate-rule templates)
- Kyverno namespaced `Policy` support (scope selector)
- Kyverno CEL `ValidatingPolicy` format toggle
- Dry-run validation before apply (`--dry-run=server`)
- Table-driven generators for pod security family (reduce duplication)

## References

### Internal
- Wizard infrastructure: `backend/internal/wizard/handler.go:19-24` (WizardInput interface)
- Wizard route registration: `backend/internal/server/routes.go:234-261`
- Policy discovery: `backend/internal/policy/discovery.go`
- Kyverno adapter: `backend/internal/policy/kyverno.go:16-23` (GVR constants)
- YAML applier: `backend/internal/yaml/applier.go` (SSA pattern)
- Frontend wizard pattern: `frontend/islands/NetworkPolicyWizard.tsx` (best exemplar)
- WizardStepper: `frontend/components/wizard/WizardStepper.tsx`
- WizardReviewStep: `frontend/components/wizard/WizardReviewStep.tsx`
- Wizard constants: `frontend/lib/wizard-constants.ts` (WIZARD_INPUT_CLASS, DNS_LABEL_REGEX)

### External
- [Kyverno Policy Library](https://kyverno.io/policies/) — 300+ reference policies
- [Gatekeeper Library](https://github.com/open-policy-agent/gatekeeper-library) — canonical ConstraintTemplates
- [NSA/CISA Kubernetes Hardening Guide](https://kubernetes.io/blog/2021/10/05/nsa-cisa-kubernetes-hardening-guidance/)
