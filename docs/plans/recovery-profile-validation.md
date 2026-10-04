# Recovery Rehearsal Profile and Validation Contract

**Status: TEMPLATE. Q3 is PENDING. Not signed off. Not authorized for execution.**

This document specifies the first named recovery rehearsal profile for Release F
(U37; covers R24, R25; KTD12). Every operator-supplied fact is an explicit
`PENDING-Q3` placeholder. No cluster fact, version, or name in this file was
invented, and no cluster was queried to produce it.

> **This document authorizes no live restore.** Nothing here adds, enables, or
> prepares any code path that creates a `velero.io/v1 Restore` object. Rehearsal
> execution stays disabled until Q3 is answered, this profile is signed off, and
> the six milestones in the plan's "Deferred Appendix - Recovery Rehearsal
> Delivery" each ship under their own approval.

Source plan: `docs/plans/2026-09-10-release-f-backup-assurance-impl.md` (section U37,
Deferred Appendix, Risk O-1).

Find every open input with: `grep -rn "PENDING-Q3" docs/ e2e/fixtures/`

## Evidence rule (citations pending Q3)

Every claim in the completed profile must cite one of:

1. **The operator inventory** (the supplied answer to the matching input below),
   identified by input number and date; or
2. **Official documentation** for the version actually installed. The Velero
   restore reference is read for the installed Velero version. Never assume 1.18;
   the plan's 1.18 citation is research evidence, not a statement of what is
   installed.

`PENDING-Q3 (citations): no operator inventory has been supplied and no installed-version documentation has been selected. Required evidence: the operator's written inventory for inputs 1-11, plus the documentation URL and version for each of Kubernetes, Velero, each plugin and each CSI driver.`

## The eleven required inputs

Each input is a placeholder until the operator answers it.

### 1. Destination identity

- Cluster name: `PENDING-Q3: destination cluster name, supplied by the operator`
- API server endpoint: `PENDING-Q3: destination API endpoint, supplied by the operator`
- Administration (how and by whom): `PENDING-Q3: destination administrator and access path, supplied by the operator`
- Trust boundary: `PENDING-Q3: statement of why the destination is isolated from production, supplied by the operator and reviewed`

Reachability is not isolation. Namespace remapping is not isolation. The trust
boundary statement must say what separates the destination from production at the
network, credential and administrative level.

### 2. Destination versions

`PENDING-Q3: destination Kubernetes server version, Velero server version, every installed Velero plugin with version, CSI driver(s) with version, snapshot-controller version; supplied by the operator from the destination cluster`

### 3. Source versions

`PENDING-Q3: the same list for the source cluster, plus the Velero version that wrote the backup; supplied by the operator`

Restore compatibility is a function of both sides. The source backup portability
analysis records which backed-up resources and volume data can be restored on the
destination and which cannot:
`PENDING-Q3: portability analysis, performed against the official documentation for the installed versions`

### 4. Storage mapping

`PENDING-Q3: every StorageClass named in the source backup mapped to a StorageClass present on the destination; every VolumeSnapshotClass likewise; supplied by the operator`

Any unmapped class is a preflight blocker named by class (AE9).

### 5. Backup storage location access

`PENDING-Q3: whether the destination reads the source BSL bucket, with which credentials, and whether those credentials are read-only; supplied by the operator`

### 6. Resource allow-list

`PENDING-Q3: exact namespaces and resource kinds permitted in the restore; kinds explicitly excluded (Secrets, ServiceAccounts, PVs, CRDs, webhooks, anything cluster-wide); supplied by the operator`

### 7. Side-effect suppression

`PENDING-Q3: per controller, admission webhook, ingress controller, GitOps agent, external integration and outbound notifier (including DNS and email/chat notifiers): whether it must be suppressed in the destination, and the mechanism; supplied by the operator`

### 8. Application validation checks

`PENDING-Q3: the concrete readiness and data assertions that define "the application recovered", the credentials each needs, the network destinations each may reach; supplied by the operator`

See "What each check does and does not establish" below for the structure each
check must fill in.

### 9. Data-handling policy

`PENDING-Q3: whether production data may exist in the destination, for how long, who may access it, how it is destroyed, and which regulatory constraints apply; supplied by the operator and compliance owner`

### 10. Cleanup ownership

`PENDING-Q3: the named human or process responsible for cleanup, the retention decision, and the escalation path when a finalizer wedges a namespace; supplied by the operator`

### 11. Cadence and approval

`PENDING-Q3: who authorizes each rehearsal, and how a rehearsal is aborted mid-flight; supplied by the operator`

## What each check does and does not establish

This structure is fixed and does not depend on operator facts.

| Check class | Establishes | Does NOT establish |
|---|---|---|
| Preflight (pure validation) | The profile is internally consistent: versions recorded, every source StorageClass and VolumeSnapshotClass mapped, destination matches the profile, required APIs exist. | That a restore would succeed. Preflight creates nothing and observes no restored data. |
| Restore completion (Velero phase and its warnings/errors, retained verbatim) | Velero reports it finished processing the reviewed resource set. | That the application works, that data is intact, or that nothing was skipped beyond what Velero reported. |
| Readiness check (workload ready) | The named workload reached its readiness condition in the destination. | That data is correct, complete or current. |
| Data check (workload-specific assertion) | The named assertion held against restored data at the time it ran. | That any other data is intact, or that production would behave identically. |
| Cleanup report | Resources carrying the rehearsal ownership label were removed, or listed as operator-intervention items. | That pre-existing resources were untouched, unless the ownership inventory proves it. Cleanup never deletes anything not owned. |

Restore completion and application health are separate outcomes and are reported
independently. A restore may complete while application checks fail.

## No RPO/RTO claim (R25)

A rehearsal result is evidence about one backup, one destination, one moment. It
is never presented as a Recovery Point Objective or Recovery Time Objective, and no
timing observed during a rehearsal may be quoted as an RTO. `passed` means the
listed checks passed, nothing more.

## Credential handling (KTD12)

No user bearer credential is persisted for later use. A future durable execution
record stores the profile revision, backup UID, initiating identity, a service
authorization, phase, ownership labels, timestamps and observed results. The
initiating user's token is used only within the request that carries it.

## Sign-off

| Role | Name | Date |
|---|---|---|
| Operator | `PENDING-Q3: operator name` | `PENDING-Q3` |
| Reviewer | `PENDING-Q3: reviewer name` | `PENDING-Q3` |

Until every `PENDING-Q3` marker above is replaced by operator-supplied, cited
content and the sign-off table is complete, this profile is a template only.
