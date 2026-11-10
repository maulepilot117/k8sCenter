/** @jsxImportSource react */
/** Backup assurance — recovery readiness page (Release F U36).
 *
 * Reads the U35 API under /v1/velero/assurance: the collector's status, the
 * caller's visible exceptions, and (admins only) the freshness policies.
 *
 * Honesty rule (plan §7): backup freshness is not demonstrated
 * recoverability. The page therefore
 *   - opens with a persistent, non-dismissible banner saying so;
 *   - shows no aggregate score, percentage, shield or "protected" badge —
 *     counts are per condition and always labelled with the condition;
 *   - uses the R3 six-state vocabulary verbatim and never renders an
 *     `unknown` collection as zero or as "no backups";
 *   - offers no restore action of any kind. It observes; it never mutates a
 *     cluster. The only writes are admin policy edits, which touch
 *     PostgreSQL alone.
 *
 * Local cluster only: a remote selection is answered 501
 * remote_assurance_unsupported and the page says so instead of rendering an
 * empty list that would read as "no exceptions".
 *
 * Glass is chrome-only: the banner and page header use `.glass-bar`; every
 * data surface (counts, exception cards, policy table) is solid.
 *
 * This island owns state, requests and orchestration. The presentational
 * pieces live in components/velero/Assurance*.tsx: status panels, the
 * exception list, the policy editor/table, and their shared chips.
 */

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import {
  ExceptionList,
  type RequestFailure,
} from "@/components/velero/AssuranceExceptions.tsx";
import {
  emptyForm,
  FIELD_LABELS,
  formFromPolicy,
  localFieldErrors,
  minutesToSeconds,
  PolicyEditor,
  type PolicyForm,
  PolicyTable,
} from "@/components/velero/AssurancePolicies.tsx";
import {
  ConditionCounts,
  FreshnessRow,
  RuntimePanel,
} from "@/components/velero/AssuranceStatusPanels.tsx";
import { ApiError, api, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import {
  ASSURANCE_REASONS,
  type AssuranceException,
  type AssuranceExceptionState,
  type AssuranceFieldError,
  type AssurancePolicy,
  type AssurancePolicyCreate,
  type AssurancePolicyDeleted,
  type AssurancePolicyUpdate,
  type AssuranceStatus,
  EXCEPTION_PAGE_SIZE,
  HONESTY_TEXT,
  scopeLabel,
  surfaceStateFor,
} from "@/lib/backup-assurance-types.ts";
import type { VeleroStatus } from "@/lib/velero-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

// Full literals, one per mounted route: e2e/tests/api-routes.spec.ts reads
// every quoted "/v1/..." literal and asserts the backend mounts it, so each
// real endpoint stays route-checked. Never reintroduce a bare base path.
const STATUS_URL = "/v1/velero/assurance/status";
const EXCEPTIONS_URL = "/v1/velero/assurance/exceptions";
const POLICIES_URL = "/v1/velero/assurance/policies";

const ROOT_CLASS = "flex flex-col gap-5 p-6";
const ROOT_TEST_ID = "backup-assurance";

function toFailure(err: unknown): RequestFailure {
  if (err instanceof ApiError) {
    return {
      status: err.status,
      reason: err.reason,
      message: err.body?.error?.message ?? err.detail ?? "Request failed",
    };
  }
  return { status: 0, message: "The request could not be completed." };
}

function fieldErrorsOf(err: unknown): AssuranceFieldError[] {
  if (!(err instanceof ApiError)) return [];
  const raw = err.body?.error?.extra?.fieldErrors;
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (e): e is AssuranceFieldError =>
      typeof e === "object" &&
      e !== null &&
      typeof (e as AssuranceFieldError).field === "string" &&
      typeof (e as AssuranceFieldError).message === "string",
  );
}

function numberExtra(err: unknown, key: string): number | null {
  if (!(err instanceof ApiError)) return null;
  const v = err.body?.error?.extra?.[key];
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

interface DeleteTarget {
  policy: AssurancePolicy;
  openExceptions: number;
}

export default function BackupAssurance() {
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<AssuranceStatus | null>(null);
  const [statusFailure, setStatusFailure] = useState<RequestFailure | null>(
    null,
  );
  const [veleroDetected, setVeleroDetected] = useState<boolean | null>(null);

  const [exState, setExState, exStateRef] =
    useStateRef<AssuranceExceptionState>("open");
  const [exOffset, setExOffset, exOffsetRef] = useStateRef(0);
  const [exceptions, setExceptions] = useState<AssuranceException[]>([]);
  const [exTotal, setExTotal] = useState(0);
  const [exFailure, setExFailure] = useState<RequestFailure | null>(null);
  const [exLoading, setExLoading] = useState(false);

  const [policies, setPolicies, policiesRef] = useStateRef<
    AssurancePolicy[] | null
  >(null);
  const [policiesFailure, setPoliciesFailure] = useState<RequestFailure | null>(
    null,
  );

  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<AssurancePolicy | null>(null);
  const [form, setForm] = useState<PolicyForm>(emptyForm());
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [saving, setSaving] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const isClient = useIsClient();

  /**
   * One counter per request stream. Each call takes the next ticket and
   * applies its reply only while it still holds the latest one, so a slow
   * reply to an earlier status load, page or Open/Resolved toggle can never
   * overwrite a newer one.
   */
  const statusTicket = useRef(0);
  const exTicket = useRef(0);
  const policyTicket = useRef(0);

  async function loadExceptions(): Promise<void> {
    const ticket = ++exTicket.current;
    setExLoading(true);
    try {
      const qs = new URLSearchParams({
        state: exStateRef.current,
        limit: String(EXCEPTION_PAGE_SIZE),
        offset: String(exOffsetRef.current),
      });
      const res = await apiGet<AssuranceException[]>(
        `${EXCEPTIONS_URL}?${qs.toString()}`,
      );
      if (ticket !== exTicket.current) return;
      const rows = Array.isArray(res.data) ? res.data : [];
      const total = res.metadata?.total ?? rows.length;
      if (rows.length === 0 && total > 0 && exOffsetRef.current > 0) {
        // The list shrank below the page being shown (a refresh, a resolved
        // exception or a deleted policy). Move to the last page that exists
        // instead of rendering an empty page as "no exceptions".
        setExOffset(
          Math.floor((total - 1) / EXCEPTION_PAGE_SIZE) * EXCEPTION_PAGE_SIZE,
        );
        await loadExceptions();
        return;
      }
      setExceptions(rows);
      setExTotal(total);
      setExFailure(null);
    } catch (err) {
      if (ticket !== exTicket.current) return;
      setExceptions([]);
      setExTotal(0);
      setExFailure(toFailure(err));
    } finally {
      if (ticket === exTicket.current) setExLoading(false);
    }
  }

  async function loadPolicies(): Promise<void> {
    const ticket = ++policyTicket.current;
    try {
      const res = await apiGet<AssurancePolicy[]>(POLICIES_URL);
      if (ticket !== policyTicket.current) return;
      setPolicies(Array.isArray(res.data) ? res.data : []);
      setPoliciesFailure(null);
    } catch (err) {
      if (ticket !== policyTicket.current) return;
      setPolicies(null);
      setPoliciesFailure(toFailure(err));
    }
  }

  async function loadAll(): Promise<void> {
    const ticket = ++statusTicket.current;
    setLoading(true);
    const [statusRes, veleroRes] = await Promise.allSettled([
      apiGet<AssuranceStatus>(STATUS_URL),
      apiGet<VeleroStatus>("/v1/velero/status"),
    ]);
    if (ticket !== statusTicket.current) return;

    setVeleroDetected(
      veleroRes.status === "fulfilled" &&
        typeof veleroRes.value.data?.detected === "boolean"
        ? veleroRes.value.data.detected
        : null,
    );

    if (statusRes.status === "rejected") {
      setStatus(null);
      setStatusFailure(toFailure(statusRes.reason));
      // Invalidate any list request still in flight: nothing below the
      // failed status is shown, and none of it may land later.
      exTicket.current++;
      policyTicket.current++;
      setExceptions([]);
      setExTotal(0);
      setExLoading(false);
      setPolicies(null);
      setLoading(false);
      return;
    }
    setStatus(statusRes.value.data);
    setStatusFailure(null);

    // `runtime` is projected for admins only, so its presence is the
    // server's own answer to "may this caller manage policies".
    const admin = statusRes.value.data?.runtime !== undefined;
    await Promise.all([
      loadExceptions(),
      admin ? loadPolicies() : Promise.resolve(),
    ]);
    if (ticket === statusTicket.current) setLoading(false);
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `loadAll` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    loadAll();
  }, []);

  // The SSR placeholder and the hydrated root share their attributes:
  // hydration never re-applies the root's own props (see
  // server/check-placeholder-root-parity.ts).
  if (!isClient) {
    return <div className={ROOT_CLASS} data-testid={ROOT_TEST_ID} />;
  }

  const failure = statusFailure;
  const isAdmin = status?.runtime !== undefined;
  const state = surfaceStateFor(status, failure?.status);

  function changePage(delta: number) {
    setExOffset(Math.max(0, exOffsetRef.current + delta * EXCEPTION_PAGE_SIZE));
    loadExceptions();
  }

  function switchExState(next: AssuranceExceptionState) {
    if (exStateRef.current === next) return;
    setExState(next);
    setExOffset(0);
    loadExceptions();
  }

  function openCreate() {
    setEditing(null);
    setForm(emptyForm());
    setFieldErrors({});
    setFormError(null);
    setConflict(false);
    setEditorOpen(true);
    setNotice(null);
  }

  function openEdit(p: AssurancePolicy) {
    setEditing(p);
    setForm(formFromPolicy(p));
    setFieldErrors({});
    setFormError(null);
    setConflict(false);
    setEditorOpen(true);
    setNotice(null);
  }

  function closeEditor() {
    setEditorOpen(false);
    setEditing(null);
  }

  async function reloadEditedPolicy() {
    const current = editing;
    await loadPolicies();
    const fresh = policiesRef.current?.find((p) => p.id === current?.id);
    if (fresh) {
      openEdit(fresh);
      setNotice(
        "Reloaded the current values. Re-apply your changes and save again.",
      );
    } else {
      closeEditor();
      setNotice("That policy no longer exists.");
    }
  }

  async function submitPolicy() {
    const f = form;
    const creating = editing === null;
    const local = localFieldErrors(f, creating);
    setFieldErrors(local);
    setFormError(null);
    if (Object.keys(local).length > 0) return;

    const maxAgeSeconds = minutesToSeconds(f.maxAgeMinutes) ?? 0;
    const graceSeconds = minutesToSeconds(f.graceMinutes) ?? 0;
    setSaving(true);
    try {
      if (creating) {
        const body: AssurancePolicyCreate = {
          scopeKind: f.scopeKind,
          scopeNamespace:
            f.scopeKind === "cluster" ? "" : f.scopeNamespace.trim(),
          scopeName: f.scopeKind === "schedule" ? f.scopeName.trim() : "",
          maxAgeSeconds,
          graceSeconds,
          treatPartialAs: f.treatPartialAs,
          alertOnPaused: f.alertOnPaused,
          enabled: f.enabled,
        };
        await apiPost<AssurancePolicy>(POLICIES_URL, body);
        setNotice(
          "Policy created. It is evaluated on the collector's next pass, within about a minute.",
        );
      } else {
        const p = editing as AssurancePolicy;
        const body: AssurancePolicyUpdate = {
          revision: p.revision,
          maxAgeSeconds,
          graceSeconds,
          treatPartialAs: f.treatPartialAs,
          alertOnPaused: f.alertOnPaused,
          enabled: f.enabled,
        };
        await apiPut<AssurancePolicy>(`${POLICIES_URL}/${p.id}`, body);
        setNotice("Policy saved.");
      }
      closeEditor();
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      switch (fail.reason) {
        case ASSURANCE_REASONS.invalidPolicy: {
          const errs: Record<string, string> = {};
          const unplaced: string[] = [];
          for (const fe of fieldErrorsOf(err)) {
            if (fe.field in FIELD_LABELS && fe.field !== "revision") {
              errs[fe.field] = fe.message;
            } else {
              unplaced.push(
                `${FIELD_LABELS[fe.field] ?? fe.field} ${fe.message}`,
              );
            }
          }
          setFieldErrors(errs);
          setFormError(
            unplaced.length > 0
              ? unplaced.join("; ")
              : Object.keys(errs).length > 0
                ? "Correct the highlighted fields."
                : fail.message,
          );
          break;
        }
        case ASSURANCE_REASONS.revisionConflict:
          setConflict(true);
          setFormError(
            "Someone else changed this policy since you opened it. Reload it to see the current values; your edits were not saved.",
          );
          break;
        case ASSURANCE_REASONS.policyExists:
          setFormError(
            "A policy already exists for this scope. Edit that policy instead.",
          );
          break;
        case ASSURANCE_REASONS.policyNotFound:
          setFormError("This policy no longer exists.");
          await loadPolicies();
          break;
        case ASSURANCE_REASONS.scopeImmutable:
          setFormError(
            "A policy's scope cannot be changed. Create a new policy for a different scope.",
          );
          break;
        default:
          setFormError(
            fail.status === 403
              ? "Only administrators can change freshness policies."
              : fail.message,
          );
      }
    } finally {
      setSaving(false);
    }
  }

  /**
   * Step one of a delete: ask the server what it would discard. Without
   * ?confirm=true the handler deletes nothing and answers 400
   * confirmation_required with extra.openExceptions.
   */
  async function requestDelete(p: AssurancePolicy) {
    setDeleteError(null);
    setNotice(null);
    try {
      await api<AssurancePolicyDeleted>(`${POLICIES_URL}/${p.id}`, {
        method: "DELETE",
      });
      // A server that deleted without confirmation would be a contract
      // break; refresh so the page shows the truth either way.
      setNotice("Policy deleted.");
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.confirmationRequired) {
        setDeleteTarget({
          policy: p,
          openExceptions: numberExtra(err, "openExceptions") ?? 0,
        });
      } else if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        setNotice("That policy no longer exists.");
        await loadPolicies();
      } else {
        setNotice(fail.message);
      }
    }
  }

  async function confirmDelete() {
    const target = deleteTarget;
    if (!target) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      const res = await api<AssurancePolicyDeleted>(
        `${POLICIES_URL}/${target.policy.id}?confirm=true`,
        { method: "DELETE" },
      );
      const n = res.data?.discardedOpenExceptions ?? target.openExceptions;
      setDeleteTarget(null);
      setNotice(
        `Policy deleted. ${n} open ${
          n === 1 ? "exception was" : "exceptions were"
        } discarded with its history.`,
      );
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        setDeleteTarget(null);
        setNotice("That policy no longer exists.");
        await loadPolicies();
      } else {
        setDeleteError(fail.message);
      }
    } finally {
      setDeleting(false);
    }
  }

  const st = status;

  return (
    <div className={ROOT_CLASS} data-testid={ROOT_TEST_ID}>
      {/* Honesty banner — first element, persistent, non-dismissible. */}
      <div
        className="glass-bar rounded-2xl px-5 py-3 text-sm text-text-primary"
        role="note"
        data-testid="assurance-honesty-banner"
      >
        {HONESTY_TEXT}
      </div>

      <header className="glass-bar flex flex-wrap items-start justify-between gap-4 rounded-2xl px-5 py-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-text-primary">
            Backup Assurance
          </h1>
          <p className="mt-1 text-sm text-text-muted">
            Freshness of what Velero reported for the local cluster, judged
            against the policies below. Freshness is not demonstrated
            recoverability and is not a recovery-point or recovery-time
            objective.
          </p>
        </div>
        <Button variant="secondary" onClick={() => loadAll()} loading={loading}>
          Refresh
        </Button>
      </header>

      {notice && (
        <div
          className="rounded-md border border-border-subtle bg-surface px-4 py-2 text-sm text-text-secondary"
          role="status"
          data-testid="assurance-notice"
        >
          {notice}
        </div>
      )}

      {loading && !st && !failure ? (
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      ) : failure?.reason === ASSURANCE_REASONS.remote ? (
        <section
          className="rounded-lg border border-border-subtle bg-surface p-6"
          data-testid="assurance-remote-unsupported"
        >
          <h2 className="text-base font-semibold text-text-primary">
            Local cluster only
          </h2>
          <p className="mt-2 text-sm text-text-secondary">
            Backup assurance is collected by the k8sCenter instance running in
            each cluster, for that cluster. The selected cluster is a remote
            cluster, so nothing is shown here — this does not mean it has no
            backup exceptions. Switch to the local cluster to view its backup
            assurance.
          </p>
        </section>
      ) : failure ? (
        <>
          <FreshnessRow
            state={state}
            status={null}
            veleroDetected={veleroDetected}
          />
          {state !== "forbidden" && state !== "unavailable" && (
            <div
              className="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
              role="alert"
              data-testid="assurance-error"
            >
              Backup assurance status could not be loaded: {failure.message}.
              Backup state is unknown.
            </div>
          )}
        </>
      ) : st ? (
        <>
          <FreshnessRow
            state={state}
            status={st}
            veleroDetected={veleroDetected}
          />

          {st.policyCount === 0 && (
            <section
              className="rounded-lg border border-border-subtle bg-surface p-6"
              data-testid="assurance-empty-policies"
            >
              <p className="text-sm text-text-primary">
                {isAdmin
                  ? "No freshness policies configured. Backup assurance evaluates nothing until you add one."
                  : "No freshness policies apply to namespaces you can see. Backup assurance evaluates nothing for you until an administrator adds one."}
              </p>
            </section>
          )}

          {st.policyCount > 0 && <ConditionCounts status={st} state={state} />}

          {st.runtime && <RuntimePanel runtime={st.runtime} />}

          <ExceptionList
            exState={exState}
            exceptions={exceptions}
            total={exTotal}
            offset={exOffset}
            loading={exLoading}
            failure={exFailure}
            state={state}
            policyCount={st.policyCount}
            onSwitchState={switchExState}
            onChangePage={changePage}
          />

          <section
            aria-label="Freshness policies"
            className="flex flex-col gap-3"
            data-testid="assurance-policies"
          >
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 className="text-sm font-semibold text-text-primary">
                Freshness policies
              </h2>
              {isAdmin && !editorOpen && (
                <Button size="sm" onClick={openCreate}>
                  Add policy
                </Button>
              )}
            </div>
            {!isAdmin ? (
              <p
                className="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
                data-testid="assurance-policies-readonly"
              >
                Freshness policies are managed by administrators.{" "}
                {st.policyCount === 1
                  ? "1 policy applies"
                  : `${st.policyCount} policies apply`}{" "}
                to namespaces you can see.
              </p>
            ) : (
              <>
                {editorOpen && (
                  <PolicyEditor
                    editing={editing}
                    form={form}
                    fieldErrors={fieldErrors}
                    formError={formError}
                    conflict={conflict}
                    saving={saving}
                    onChange={(patch) => {
                      setForm((prev) => ({ ...prev, ...patch }));
                    }}
                    onSubmit={submitPolicy}
                    onCancel={closeEditor}
                    onReload={reloadEditedPolicy}
                  />
                )}
                {policiesFailure ? (
                  <p
                    className="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
                    role="alert"
                  >
                    Policies could not be loaded: {policiesFailure.message}
                  </p>
                ) : policies && policies.length > 0 ? (
                  <PolicyTable
                    policies={policies}
                    busy={saving || deleting}
                    onEdit={openEdit}
                    onDelete={requestDelete}
                  />
                ) : null}
              </>
            )}
          </section>
        </>
      ) : null}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete freshness policy"
          danger
          confirmLabel="Delete policy"
          loading={deleting}
          typeToConfirm={
            deleteTarget.policy.scopeName ||
            deleteTarget.policy.scopeNamespace ||
            "cluster"
          }
          message={
            <>
              Deleting the policy for{" "}
              {scopeLabel(
                deleteTarget.policy.scopeKind,
                deleteTarget.policy.scopeNamespace,
                deleteTarget.policy.scopeName,
              )}{" "}
              discards every exception it opened, open and resolved, with their
              history.{" "}
              {deleteTarget.openExceptions === 1
                ? "1 open exception will be discarded."
                : `${deleteTarget.openExceptions} open exceptions will be discarded.`}
              {deleteError && (
                <span className="mt-2 block text-error" role="alert">
                  {deleteError}
                </span>
              )}
            </>
          }
          onConfirm={confirmDelete}
          onCancel={() => {
            setDeleteTarget(null);
            setDeleteError(null);
          }}
        />
      )}
    </div>
  );
}
