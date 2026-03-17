# Step 21: Cilium Network Policy Editor

## Overview

Add a visual CiliumNetworkPolicy viewer and creator to k8sCenter. Uses the existing resource handler pattern with dynamic client for CRD access. The core deliverable is an NSX-T-style rule table editor.

## Simplified Approach (per reviewer feedback)

- **No new backend package** — add CiliumNetworkPolicy as a dynamic-client resource in the existing handler
- **Single-page form, not wizard** — no sequential dependency between steps
- **Cut label auto-suggest and pod count preview** — defer to Phase B
- **Server-side policy construction** — accept structured Go struct, not raw JSON
- **Input validation** — CIDR, ports, entities, labels validated server-side
- **Dangerous policy warnings** — deny-all detection, protected namespace checks

## Implementation

### Backend

**Existing file changes:**

1. `backend/internal/k8s/resources/access.go` — Fix `apiGroupForResource` to support CRD groups (add `ciliumnetworkpolicies` → `cilium.io`)

2. `backend/internal/k8s/resources/` — Add `cilium.go`:
   - List/Get/Delete CiliumNetworkPolicies via `dynamic.Interface`
   - Create via structured Go payload (not raw unstructured):
     ```go
     type CiliumPolicyRequest struct {
         Name             string            `json:"name"`
         Namespace        string            `json:"namespace"`
         EndpointSelector map[string]string `json:"endpointSelector"`
         IngressRules     []PolicyRule      `json:"ingressRules"`
         EgressRules      []PolicyRule      `json:"egressRules"`
     }
     type PolicyRule struct {
         PeerType string            `json:"peerType"` // "endpoints", "entities", "cidr"
         Labels   map[string]string `json:"labels,omitempty"`
         Entities []string          `json:"entities,omitempty"`
         CIDRs    []string          `json:"cidrs,omitempty"`
         Ports    []PortRule        `json:"ports,omitempty"`
         Action   string            `json:"action"` // "allow", "deny"
     }
     type PortRule struct {
         Port     int    `json:"port"`
         Protocol string `json:"protocol"` // "TCP", "UDP", "ANY"
     }
     ```
   - Server-side construction of `unstructured.Unstructured` CiliumNetworkPolicy
   - Input validation: `validateCIDR`, `validatePort`, `validateEntity`, `validateLabels`
   - Dangerous policy detection: warn on deny-all, protected namespaces
   - Audit logging with policy summary in detail field

3. `backend/internal/server/routes.go` — Register Cilium policy routes (behind auth + CSRF)

### Frontend

1. `frontend/lib/resource-columns.ts` — Add column definitions for `ciliumnetworkpolicies`

2. `frontend/routes/networking/cilium-policies.tsx` — List page (reuses ResourceTable)

3. `frontend/islands/CiliumPolicyEditor.tsx` — The core deliverable:
   - **Applied To** section: key=value label pairs for endpoint selector
   - **Rules** section: NSX-T-style rule table
     ```
     | # | Direction | Peers          | Ports       | Action |
     |---|-----------|----------------|-------------|--------|
     | 1 | Ingress   | app=frontend   | TCP/80      | Allow  |
     | 2 | Egress    | world          | TCP/443     | Allow  |
     ```
   - Add/remove rules inline
   - Peer type selector: Endpoints (labels) / Entities / CIDR
   - Port + protocol inputs
   - Allow / Deny toggle
   - YAML preview (read-only, auto-generated)
   - Submit: validates, shows warnings for dangerous patterns, applies
   - Single-page form with collapsible sections (not wizard)

4. `frontend/routes/networking/cilium-policies/new.tsx` — Create page (renders CiliumPolicyEditor)

5. `frontend/lib/constants.ts` — Add sidebar nav entry under Networking

### Validation Rules (server-side)

| Field | Validation |
|---|---|
| Policy name | k8s name regex, max 253 chars |
| Labels | key/value regex, max 63 chars each, max 20 pairs |
| CIDR | `net.ParseCIDR`, warn on `0.0.0.0/0`, reject loopback |
| Ports | 1-65535, protocol in [TCP, UDP, SCTP, ANY] |
| Entities | Must be in: world, cluster, host, remote-node, kube-apiserver, health, init, ingress, all |
| Namespace | Warn on kube-system, cilium, k8scenter namespace |
| Deny-all | Warn if empty endpoint selector + deny rules |

## Acceptance Criteria

- [ ] List CiliumNetworkPolicies in ResourceTable
- [ ] View policy detail (YAML tab via existing ResourceDetail)
- [ ] Create policy via single-page form with rule table
- [ ] Endpoints, entities, CIDR peer types
- [ ] Port + protocol configuration
- [ ] Allow / deny rules
- [ ] YAML preview before apply
- [ ] Server-side input validation (CIDR, ports, entities, labels)
- [ ] Dangerous policy warnings (deny-all, protected namespaces)
- [ ] RBAC via impersonation (cilium.io API group in AccessChecker)
- [ ] Audit logging with policy summary
- [ ] Works on homelab Cilium 1.19

## References

- Reviewer feedback: simplicity (reuse existing patterns), security (validation, lockout protection)
- Cilium CRD docs: https://docs.cilium.io/en/stable/security/policy/language/
- Existing dynamic client: `backend/internal/k8s/client.go` DynamicClientForUser
- Existing resource handler: `backend/internal/k8s/resources/handler.go`
