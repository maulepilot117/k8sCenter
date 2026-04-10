---
status: complete
priority: p2
issue_id: 276
tags: [architecture, refactor, policy, code-review, pr-163]
dependencies: []
---

## Problem Statement

`NormalizedPolicy.Name` is overloaded as both a display label (Kyverno uses the `policies.kyverno.io/title` annotation when present) and the join key used by violations (`NormalizedViolation.Policy` = raw k8s resource name). This forced PR #163 to add a `kyvernoK8sName` helper that reverse-parses the raw k8s name out of the composite `ID` string — a parser built on top of another parser.

Gatekeeper has the mirror-image of this bug (`Name` = title annotation, `Policy` = `"{kind}/{name}"`), silently breaking compliance scoring whenever a Gatekeeper constraint has a title annotation set. PR #163 left that alone as out of scope, but the root cause is the same.

## Findings

- `kyverno.go:97-106`: `title := annotations["policies.kyverno.io/title"]; if title == "" { title = name }; ... Name: title`
- `gatekeeper.go:130-143`: identical pattern with `metadata.gatekeeper.sh/title`.
- `handler.go` (post-PR #163): `kyvernoK8sName` helper parses `"kyverno::name"` / `"kyverno:ns/name"` back to the k8s name.
- `computeCompliance`: engine switch to choose between `p.Name` and `kyvernoK8sName(p.ID)` as lookup key.

## Proposed Solutions

### Option A — Add `MatchKey` (or `ResourceName`) field to `NormalizedPolicy`
Add a new string field populated at normalization time:
```go
type NormalizedPolicy struct {
    ID        string // composite, used for URLs
    Name      string // display label (title or k8s name)
    MatchKey  string // join key for violations
    ...
}
```
- For Kyverno: `MatchKey = obj.GetName()` (k8s name).
- For Gatekeeper: `MatchKey = fmt.Sprintf("%s/%s", constraintKind, obj.GetName())` (matches what `extractGatekeeperViolations` writes into `NormalizedViolation.Policy`).
- `kyvernoK8sName` helper disappears.
- `computeCompliance` engine switch disappears.
- Fixes the Gatekeeper pre-existing bug as a side effect.
- Pros: One field, two engines, no string parsing. Architectural smell eliminated.
- Cons: Adds a field to a type already exposed over the wire — frontend might try to use it. Gate with `json:"-"` to keep it internal-only.
- Effort: Small.
- Risk: Low.

### Option B — Split `Name` into `ResourceName` + `DisplayName`
More explicit, but requires frontend changes (the dashboard currently shows `p.name`).
- Pros: Clearer semantics.
- Cons: Frontend breaking change; wire format changes.
- Effort: Medium.
- Risk: Medium.

### Option C — Leave as-is
Keep the `kyvernoK8sName` helper and engine switch.
- Pros: No change.
- Cons: Gatekeeper title bug remains; future engine additions will inherit the same workaround.
- Effort: None.

## Recommended Action

## Technical Details

**Affected files:**
- `backend/internal/policy/types.go` — add field (tag `json:"-"`).
- `backend/internal/policy/kyverno.go` — set `MatchKey = name`.
- `backend/internal/policy/gatekeeper.go` — set `MatchKey = "{kind}/{name}"`.
- `backend/internal/policy/handler.go` — remove `kyvernoK8sName` helper and engine switch.

## Acceptance Criteria

- [ ] `MatchKey` populated by both adapters.
- [ ] `kyvernoK8sName` deleted.
- [ ] `computeCompliance` has no engine-specific branches.
- [ ] Gatekeeper compliance score is correct when constraints have a title annotation (new test).
- [ ] Kyverno compliance + violation counts remain correct (existing behavior preserved).

## Work Log

_2026-04-10_: Discovered during `/review` on PR #163 by code-simplicity-reviewer and architecture-strategist. Independently recommended by both.

## Resources

- Related: todo 274 (P1 leak), todo 275 (move into adapter).
