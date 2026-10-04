# Recovery Rehearsal Runbook

**Status: TEMPLATE. Q3 is PENDING. This runbook cannot be executed.**

> **Gate: Do not proceed past Preflight until the six Deferred Appendix milestones
> have shipped and the profile in `docs/plans/recovery-profile-validation.md` has
> been signed off.**

Today no code path in k8sCenter creates a Velero Restore, and this document
authorizes none. It records the human procedure so the profile and checks can be
reviewed before any capability is built. Find open inputs with
`grep -rn "PENDING-Q3" docs/ e2e/fixtures/`.

## What a rehearsal would establish

- One specific backup restored into one specific, isolated destination under a
  reviewed profile.
- Velero's own reported outcome, with warnings and errors retained verbatim.
- The listed readiness and data checks passed or failed at the time they ran.

## What it would not establish

- Any RPO or RTO (R25). Timings are never quoted as objectives.
- That any other backup, schedule or namespace is restorable.
- That application health follows from restore completion; they are separate outcomes.
- That production restore would behave identically.

## Checklist

### 1. Preflight (creates nothing)

- [ ] Profile signed off: `PENDING-Q3: sign-off record location`
- [ ] Destination identity and trust boundary confirmed (input 1)
- [ ] Source and destination versions recorded and compatible (inputs 2, 3)
- [ ] Every StorageClass and VolumeSnapshotClass mapped; any unmapped class blocks by name (input 4)
- [ ] Backup storage location access confirmed read-only (input 5)
- [ ] Resource allow-list and exclusions reviewed (input 6)
- [ ] Side-effect suppression in place in the destination (input 7)
- [ ] Data-handling policy accepted by the data owner (input 9)
- [ ] Cleanup owner named and available (input 10)
- [ ] Approval to run recorded (input 11): `PENDING-Q3: approver`

### 2. Restore (manual, opt-in per execution; no scheduling)

- [ ] Only reviewed resources are created, only in the designated destination
- [ ] Existing-resource conflicts are reported, never silently overwritten
- [ ] No user bearer credential is stored for later use (KTD12)

### 3. Validate

- [ ] Run each check in the profile (input 8); record restore result and application result separately
- [ ] Record what each check does not establish

### 4. Cleanup (separately authorized)

- [ ] Inventory resources by ownership label and preview before deleting
- [ ] Retention choice made explicitly (input 10)
- [ ] Never delete pre-existing resources
- [ ] Stuck finalizers: escalate per `PENDING-Q3: finalizer escalation path`; surface as operator-intervention, not as silent failure

## Abort conditions

Abort and record the reason if any of the following holds. Cancellation stops
future steps; it does not promise reversal of already-created resources, which
cleanup handles separately.

- Preflight blocker of any kind (unmapped class, missing destination API, destination mismatch)
- Destination found reachable from, or sharing credentials with, production
- A resource outside the allow-list appears, or a side-effect suppression fails
- Production data appears where the data-handling policy forbids it
- Abort authority and mid-flight procedure: `PENDING-Q3: who may abort and how`
