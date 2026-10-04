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

import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ExceptionList } from "@/components/velero/AssuranceExceptions.tsx";
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

// Full literals, one per mounted route: e2e/tests/api-routes.spec.ts reads
// every quoted "/v1/..." literal and asserts the backend mounts it, so each
// real endpoint stays route-checked. Never reintroduce a bare base path.
const STATUS_URL = "/v1/velero/assurance/status";
const EXCEPTIONS_URL = "/v1/velero/assurance/exceptions";
const POLICIES_URL = "/v1/velero/assurance/policies";

const ROOT_CLASS = "flex flex-col gap-5 p-6";
const ROOT_TEST_ID = "backup-assurance";

/** A failed request, reduced to what the page branches on. */
interface RequestFailure {
  status: number;
  reason?: string;
  message: string;
}

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
  const loading = useSignal(true);
  const status = useSignal<AssuranceStatus | null>(null);
  const statusFailure = useSignal<RequestFailure | null>(null);
  const veleroDetected = useSignal<boolean | null>(null);

  const exState = useSignal<AssuranceExceptionState>("open");
  const exOffset = useSignal(0);
  const exceptions = useSignal<AssuranceException[]>([]);
  const exTotal = useSignal(0);
  const exFailure = useSignal<RequestFailure | null>(null);
  const exLoading = useSignal(false);

  const policies = useSignal<AssurancePolicy[] | null>(null);
  const policiesFailure = useSignal<RequestFailure | null>(null);

  const editorOpen = useSignal(false);
  const editing = useSignal<AssurancePolicy | null>(null);
  const form = useSignal<PolicyForm>(emptyForm());
  const fieldErrors = useSignal<Record<string, string>>({});
  const formError = useSignal<string | null>(null);
  const conflict = useSignal(false);
  const saving = useSignal(false);

  const deleteTarget = useSignal<DeleteTarget | null>(null);
  const deleting = useSignal(false);
  const deleteError = useSignal<string | null>(null);
  const notice = useSignal<string | null>(null);

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
    exLoading.value = true;
    try {
      const qs = new URLSearchParams({
        state: exState.value,
        limit: String(EXCEPTION_PAGE_SIZE),
        offset: String(exOffset.value),
      });
      const res = await apiGet<AssuranceException[]>(
        `${EXCEPTIONS_URL}?${qs.toString()}`,
      );
      if (ticket !== exTicket.current) return;
      const rows = Array.isArray(res.data) ? res.data : [];
      const total = res.metadata?.total ?? rows.length;
      if (rows.length === 0 && total > 0 && exOffset.value > 0) {
        // The list shrank below the page being shown (a refresh, a resolved
        // exception or a deleted policy). Move to the last page that exists
        // instead of rendering an empty page as "no exceptions".
        exOffset.value =
          Math.floor((total - 1) / EXCEPTION_PAGE_SIZE) * EXCEPTION_PAGE_SIZE;
        await loadExceptions();
        return;
      }
      exceptions.value = rows;
      exTotal.value = total;
      exFailure.value = null;
    } catch (err) {
      if (ticket !== exTicket.current) return;
      exceptions.value = [];
      exTotal.value = 0;
      exFailure.value = toFailure(err);
    } finally {
      if (ticket === exTicket.current) exLoading.value = false;
    }
  }

  async function loadPolicies(): Promise<void> {
    const ticket = ++policyTicket.current;
    try {
      const res = await apiGet<AssurancePolicy[]>(POLICIES_URL);
      if (ticket !== policyTicket.current) return;
      policies.value = Array.isArray(res.data) ? res.data : [];
      policiesFailure.value = null;
    } catch (err) {
      if (ticket !== policyTicket.current) return;
      policies.value = null;
      policiesFailure.value = toFailure(err);
    }
  }

  async function loadAll(): Promise<void> {
    const ticket = ++statusTicket.current;
    loading.value = true;
    const [statusRes, veleroRes] = await Promise.allSettled([
      apiGet<AssuranceStatus>(STATUS_URL),
      apiGet<VeleroStatus>("/v1/velero/status"),
    ]);
    if (ticket !== statusTicket.current) return;

    veleroDetected.value =
      veleroRes.status === "fulfilled" &&
      typeof veleroRes.value.data?.detected === "boolean"
        ? veleroRes.value.data.detected
        : null;

    if (statusRes.status === "rejected") {
      status.value = null;
      statusFailure.value = toFailure(statusRes.reason);
      // Invalidate any list request still in flight: nothing below the
      // failed status is shown, and none of it may land later.
      exTicket.current++;
      policyTicket.current++;
      exceptions.value = [];
      exTotal.value = 0;
      exLoading.value = false;
      policies.value = null;
      loading.value = false;
      return;
    }
    status.value = statusRes.value.data;
    statusFailure.value = null;

    // `runtime` is projected for admins only, so its presence is the
    // server's own answer to "may this caller manage policies".
    const admin = status.value?.runtime !== undefined;
    await Promise.all([
      loadExceptions(),
      admin ? loadPolicies() : Promise.resolve(),
    ]);
    if (ticket === statusTicket.current) loading.value = false;
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadAll();
  }, []);

  // The SSR placeholder and the hydrated root share their attributes: Preact
  // hydration never re-applies the root's own props (see
  // server/check-placeholder-root-parity.ts).
  if (!IS_BROWSER) return <div class={ROOT_CLASS} data-testid={ROOT_TEST_ID} />;

  const failure = statusFailure.value;
  const isAdmin = status.value?.runtime !== undefined;
  const state = surfaceStateFor(status.value, failure?.status);

  function changePage(delta: number) {
    exOffset.value = Math.max(0, exOffset.value + delta * EXCEPTION_PAGE_SIZE);
    loadExceptions();
  }

  function switchExState(next: AssuranceExceptionState) {
    if (exState.value === next) return;
    exState.value = next;
    exOffset.value = 0;
    loadExceptions();
  }

  function openCreate() {
    editing.value = null;
    form.value = emptyForm();
    fieldErrors.value = {};
    formError.value = null;
    conflict.value = false;
    editorOpen.value = true;
    notice.value = null;
  }

  function openEdit(p: AssurancePolicy) {
    editing.value = p;
    form.value = formFromPolicy(p);
    fieldErrors.value = {};
    formError.value = null;
    conflict.value = false;
    editorOpen.value = true;
    notice.value = null;
  }

  function closeEditor() {
    editorOpen.value = false;
    editing.value = null;
  }

  async function reloadEditedPolicy() {
    const current = editing.value;
    await loadPolicies();
    const fresh = policies.value?.find((p) => p.id === current?.id);
    if (fresh) {
      openEdit(fresh);
      notice.value =
        "Reloaded the current values. Re-apply your changes and save again.";
    } else {
      closeEditor();
      notice.value = "That policy no longer exists.";
    }
  }

  async function submitPolicy() {
    const f = form.value;
    const creating = editing.value === null;
    const local = localFieldErrors(f, creating);
    fieldErrors.value = local;
    formError.value = null;
    if (Object.keys(local).length > 0) return;

    const maxAgeSeconds = minutesToSeconds(f.maxAgeMinutes) ?? 0;
    const graceSeconds = minutesToSeconds(f.graceMinutes) ?? 0;
    saving.value = true;
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
        notice.value =
          "Policy created. It is evaluated on the collector's next pass, within about a minute.";
      } else {
        const p = editing.value as AssurancePolicy;
        const body: AssurancePolicyUpdate = {
          revision: p.revision,
          maxAgeSeconds,
          graceSeconds,
          treatPartialAs: f.treatPartialAs,
          alertOnPaused: f.alertOnPaused,
          enabled: f.enabled,
        };
        await apiPut<AssurancePolicy>(`${POLICIES_URL}/${p.id}`, body);
        notice.value = "Policy saved.";
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
          fieldErrors.value = errs;
          formError.value =
            unplaced.length > 0
              ? unplaced.join("; ")
              : Object.keys(errs).length > 0
                ? "Correct the highlighted fields."
                : fail.message;
          break;
        }
        case ASSURANCE_REASONS.revisionConflict:
          conflict.value = true;
          formError.value =
            "Someone else changed this policy since you opened it. Reload it to see the current values; your edits were not saved.";
          break;
        case ASSURANCE_REASONS.policyExists:
          formError.value =
            "A policy already exists for this scope. Edit that policy instead.";
          break;
        case ASSURANCE_REASONS.policyNotFound:
          formError.value = "This policy no longer exists.";
          await loadPolicies();
          break;
        case ASSURANCE_REASONS.scopeImmutable:
          formError.value =
            "A policy's scope cannot be changed. Create a new policy for a different scope.";
          break;
        default:
          formError.value =
            fail.status === 403
              ? "Only administrators can change freshness policies."
              : fail.message;
      }
    } finally {
      saving.value = false;
    }
  }

  /**
   * Step one of a delete: ask the server what it would discard. Without
   * ?confirm=true the handler deletes nothing and answers 400
   * confirmation_required with extra.openExceptions.
   */
  async function requestDelete(p: AssurancePolicy) {
    deleteError.value = null;
    notice.value = null;
    try {
      await api<AssurancePolicyDeleted>(`${POLICIES_URL}/${p.id}`, {
        method: "DELETE",
      });
      // A server that deleted without confirmation would be a contract
      // break; refresh so the page shows the truth either way.
      notice.value = "Policy deleted.";
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.confirmationRequired) {
        deleteTarget.value = {
          policy: p,
          openExceptions: numberExtra(err, "openExceptions") ?? 0,
        };
      } else if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        notice.value = "That policy no longer exists.";
        await loadPolicies();
      } else {
        notice.value = fail.message;
      }
    }
  }

  async function confirmDelete() {
    const target = deleteTarget.value;
    if (!target) return;
    deleting.value = true;
    deleteError.value = null;
    try {
      const res = await api<AssurancePolicyDeleted>(
        `${POLICIES_URL}/${target.policy.id}?confirm=true`,
        { method: "DELETE" },
      );
      const n = res.data?.discardedOpenExceptions ?? target.openExceptions;
      deleteTarget.value = null;
      notice.value = `Policy deleted. ${n} open ${
        n === 1 ? "exception was" : "exceptions were"
      } discarded with its history.`;
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        deleteTarget.value = null;
        notice.value = "That policy no longer exists.";
        await loadPolicies();
      } else {
        deleteError.value = fail.message;
      }
    } finally {
      deleting.value = false;
    }
  }

  const st = status.value;

  return (
    <div class={ROOT_CLASS} data-testid={ROOT_TEST_ID}>
      {/* Honesty banner — first element, persistent, non-dismissible. */}
      <div
        class="glass-bar rounded-2xl px-5 py-3 text-sm text-text-primary"
        role="note"
        data-testid="assurance-honesty-banner"
      >
        {HONESTY_TEXT}
      </div>

      <header class="glass-bar flex flex-wrap items-start justify-between gap-4 rounded-2xl px-5 py-4">
        <div>
          <h1 class="text-2xl font-bold tracking-tight text-text-primary">
            Backup Assurance
          </h1>
          <p class="mt-1 text-sm text-text-muted">
            Freshness of what Velero reported for the local cluster, judged
            against the policies below. Freshness is not demonstrated
            recoverability and is not a recovery-point or recovery-time
            objective.
          </p>
        </div>
        <Button
          variant="secondary"
          onClick={() => loadAll()}
          loading={loading.value}
        >
          Refresh
        </Button>
      </header>

      {notice.value && (
        <div
          class="rounded-md border border-border-subtle bg-surface px-4 py-2 text-sm text-text-secondary"
          role="status"
          data-testid="assurance-notice"
        >
          {notice.value}
        </div>
      )}

      {loading.value && !st && !failure ? (
        <div class="flex justify-center py-12">
          <Spinner />
        </div>
      ) : failure?.reason === ASSURANCE_REASONS.remote ? (
        <section
          class="rounded-lg border border-border-subtle bg-surface p-6"
          data-testid="assurance-remote-unsupported"
        >
          <h2 class="text-base font-semibold text-text-primary">
            Local cluster only
          </h2>
          <p class="mt-2 text-sm text-text-secondary">
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
            veleroDetected={veleroDetected.value}
          />
          {state !== "forbidden" && state !== "unavailable" && (
            <div
              class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
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
            veleroDetected={veleroDetected.value}
          />

          {st.policyCount === 0 && (
            <section
              class="rounded-lg border border-border-subtle bg-surface p-6"
              data-testid="assurance-empty-policies"
            >
              <p class="text-sm text-text-primary">
                {isAdmin
                  ? "No freshness policies configured. Backup assurance evaluates nothing until you add one."
                  : "No freshness policies apply to namespaces you can see. Backup assurance evaluates nothing for you until an administrator adds one."}
              </p>
            </section>
          )}

          {st.policyCount > 0 && <ConditionCounts status={st} state={state} />}

          {st.runtime && <RuntimePanel runtime={st.runtime} />}

          <ExceptionList
            exState={exState.value}
            exceptions={exceptions.value}
            total={exTotal.value}
            offset={exOffset.value}
            loading={exLoading.value}
            failure={exFailure.value}
            state={state}
            policyCount={st.policyCount}
            onSwitchState={switchExState}
            onChangePage={changePage}
          />

          <section
            aria-label="Freshness policies"
            class="flex flex-col gap-3"
            data-testid="assurance-policies"
          >
            <div class="flex flex-wrap items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-text-primary">
                Freshness policies
              </h2>
              {isAdmin && !editorOpen.value && (
                <Button size="sm" onClick={openCreate}>
                  Add policy
                </Button>
              )}
            </div>
            {!isAdmin ? (
              <p
                class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
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
                {editorOpen.value && (
                  <PolicyEditor
                    editing={editing.value}
                    form={form.value}
                    fieldErrors={fieldErrors.value}
                    formError={formError.value}
                    conflict={conflict.value}
                    saving={saving.value}
                    onChange={(patch) => {
                      form.value = { ...form.value, ...patch };
                    }}
                    onSubmit={submitPolicy}
                    onCancel={closeEditor}
                    onReload={reloadEditedPolicy}
                  />
                )}
                {policiesFailure.value ? (
                  <p
                    class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
                    role="alert"
                  >
                    Policies could not be loaded:{" "}
                    {policiesFailure.value.message}
                  </p>
                ) : policies.value && policies.value.length > 0 ? (
                  <PolicyTable
                    policies={policies.value}
                    busy={saving.value || deleting.value}
                    onEdit={openEdit}
                    onDelete={requestDelete}
                  />
                ) : null}
              </>
            )}
          </section>
        </>
      ) : null}

      {deleteTarget.value && (
        <ConfirmDialog
          title="Delete freshness policy"
          danger
          confirmLabel="Delete policy"
          loading={deleting.value}
          typeToConfirm={
            deleteTarget.value.policy.scopeName ||
            deleteTarget.value.policy.scopeNamespace ||
            "cluster"
          }
          message={
            <>
              Deleting the policy for{" "}
              {scopeLabel(
                deleteTarget.value.policy.scopeKind,
                deleteTarget.value.policy.scopeNamespace,
                deleteTarget.value.policy.scopeName,
              )}{" "}
              discards every exception it opened, open and resolved, with their
              history.{" "}
              {deleteTarget.value.openExceptions === 1
                ? "1 open exception will be discarded."
                : `${deleteTarget.value.openExceptions} open exceptions will be discarded.`}
              {deleteError.value && (
                <span class="mt-2 block text-error" role="alert">
                  {deleteError.value}
                </span>
              )}
            </>
          }
          onConfirm={confirmDelete}
          onCancel={() => {
            deleteTarget.value = null;
            deleteError.value = null;
          }}
        />
      )}
    </div>
  );
}
