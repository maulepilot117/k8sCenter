/** Rubrik namespace pages — protection per namespace, with snapshots and
 * jobs in a detail panel (U13, F2).
 *
 * Every namespace the user may read is listed with whether it is protected,
 * the SLA domain of each object protecting it, its compliance and its newest
 * snapshot (R11). The server decides visibility (R5): the page shows what
 * GET /v1/rubrik/namespaces returns and never filters or re-derives it.
 *
 * Every view says when its data came from RSC (R13). When RSC fails, the
 * page shows the last good answer marked stale, or no list at all, and never
 * a namespace as unprotected because RSC could not be asked (AE6).
 */

import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge, { type Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { Tabs } from "@/components/ui/Tabs.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import {
  ensureRubrikStatus,
  reloadRubrikStatus,
  rubrikStatus,
} from "@/src/lib/rubrik-status.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

// Full literal: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const NAMESPACES_URL = "/v1/rubrik/namespaces";

// ---------------------------------------------------------------------------
// Wire types (backend/internal/rubrik/reads.go and model.go)

type Compliance = "unknown" | "in_compliance" | "out_of_compliance";
type JobStatus =
  | "running"
  | "succeeded"
  | "failed"
  | "partial"
  | "canceled"
  | "unknown";

/** How current a part of an answer is. `error` is the failed fetch's class. */
interface Freshness {
  fetchedAt: string;
  stale: boolean;
  error?: string;
}

interface SlaRef {
  id: string;
  name: string;
}

interface RubrikObject {
  family: "namespace" | "protectionSet";
  id: string;
  /** The namespace for a per-namespace object, the set name for a set. */
  name: string;
  setType?: string;
  coverage: { all?: boolean; namespaces?: string[]; unresolved?: boolean };
  configuredSla: SlaRef;
  effectiveSla: SlaRef;
  slaAssignment?: string;
  newestSnapshot?: string;
  relic?: boolean;
  compliance: Compliance;
}

interface NamespaceRow {
  namespace: string;
  protected: boolean;
  compliance: Compliance;
  lastSnapshot?: string;
  objectIds: string[];
  /** More objects cover the namespace than the user may see. */
  hiddenObjects?: boolean;
}

interface NamespaceList {
  noAccess: boolean;
  namespaces: NamespaceRow[];
  objects: RubrikObject[];
  freshness?: Freshness;
}

interface Snapshot {
  id: string;
  time: string;
  expires?: string;
  expired?: boolean;
  onDemand?: boolean;
  corrupted?: boolean;
  quarantined?: boolean;
}

interface Job {
  id: string;
  objectId: string;
  type: string;
  status: JobStatus;
  startTime?: string;
  lastUpdated?: string;
  message?: string;
  failureReason?: string;
}

interface ObjectDetail extends RubrikObject {
  snapshots: Snapshot[];
  snapshotsFreshness?: Freshness;
  snapshotsError?: string;
  jobs: Job[];
  jobsFreshness?: Freshness;
  jobsError?: string;
}

interface NamespaceDetail {
  namespace: NamespaceRow;
  objects: ObjectDetail[];
  freshness: Freshness;
}

// ---------------------------------------------------------------------------
// Text

/**
 * What a failed read's reason means, in the page's words. RSC's own classes
 * come first; the rest are k8sCenter's, and say so rather than blame RSC.
 */
const FAILURE_TEXT: Record<string, string> = {
  auth_failed: "RSC refused k8sCenter's client ID or secret",
  unreachable: "RSC could not be reached",
  rate_limited: "RSC is rate limiting requests",
  rsc_error: "RSC returned an answer k8sCenter could not use",
  store_unavailable: "k8sCenter's database could not be read",
  access_check_failed: "Your Rubrik access could not be checked",
  admin_only: "Rubrik data on remote clusters is available to admins only",
};

function failureText(reason: string | undefined): string {
  return (
    (reason && FAILURE_TEXT[reason]) || "The Rubrik data could not be loaded"
  );
}

const SETTINGS_LINK = (
  <a className="text-accent underline" href="/settings/rubrik">
    Rubrik settings
  </a>
);

const COMPLIANCE: Record<Compliance, { label: string; tone: Tone }> = {
  in_compliance: { label: "In compliance", tone: "ok" },
  out_of_compliance: { label: "Out of compliance", tone: "crit" },
  unknown: { label: "Unknown", tone: "neutral" },
};

const JOB_STATUS: Record<JobStatus, { label: string; tone: Tone }> = {
  running: { label: "Running", tone: "info" },
  succeeded: { label: "Succeeded", tone: "ok" },
  failed: { label: "Failed", tone: "crit" },
  partial: { label: "Partial", tone: "warn" },
  canceled: { label: "Canceled", tone: "neutral" },
  unknown: { label: "Unknown", tone: "neutral" },
};

/**
 * Why the cluster has no list to show: a status state other than mapped, or
 * the reason of a read refused as not set up (409). `rsc_cluster_missing`
 * is a read reason only, sent to admins.
 */
function setupText(state: string): ReactNode {
  switch (state) {
    case "admin_only":
      return "Rubrik data on remote clusters is available to admins only.";
    case "not_configured":
      return (
        <>No Rubrik connection is configured. Set it up in {SETTINGS_LINK}.</>
      );
    case "not_mapped":
      return (
        <>
          This cluster is not mapped to an RSC cluster. Map it in{" "}
          {SETTINGS_LINK}.
        </>
      );
    case "mapping_invalid":
      return (
        <>
          This cluster's mapping no longer matches its registration. Confirm it
          again in {SETTINGS_LINK}.
        </>
      );
    case "rsc_cluster_missing":
      return (
        <>
          RSC no longer lists the cluster this one is mapped to. Map it again in{" "}
          {SETTINGS_LINK}.
        </>
      );
    case "unavailable":
      return "This cluster's Rubrik setup could not be read.";
    default:
      return "Rubrik is not set up for this cluster. Ask an admin to set it up.";
  }
}

function when(t: string | undefined): string {
  return t ? new Date(t).toLocaleString() : "—";
}

function slaName(o: RubrikObject): string {
  return o.effectiveSla?.name || "No SLA";
}

// ---------------------------------------------------------------------------
// Page

type ListState =
  | { kind: "idle" }
  | { kind: "loading" }
  | {
      kind: "loaded";
      data: NamespaceList;
      /** A refresh is in flight. */
      refreshing?: boolean;
      /** The last refresh failed; `data` is the list loaded before it. */
      refreshError?: string;
    }
  | { kind: "failed"; message: string; setup?: string };

export default function RubrikDashboard() {
  const isClient = useIsClient();
  const status = useSignalValue(rubrikStatus);
  const [list, setList] = useState<ListState>({ kind: "idle" });
  const [selected, setSelected] = useState<string | null>(null);
  const gen = useRef(0);
  /** The namespace button that opened the panel, focused again on close. */
  const opener = useRef<HTMLElement | null>(null);

  useEffect(() => {
    ensureRubrikStatus();
  }, []);

  const mapped =
    status.kind === "enabled" && status.status.cluster.state === "mapped";

  const load = useCallback(async () => {
    const g = ++gen.current;
    setList((prev) =>
      prev.kind === "loaded"
        ? { ...prev, refreshing: true }
        : { kind: "loading" },
    );
    try {
      const res = await apiGet<NamespaceList>(NAMESPACES_URL);
      if (gen.current !== g) return;
      setList({ kind: "loaded", data: res.data });
    } catch (err) {
      if (gen.current !== g) return;
      const reason = err instanceof ApiError ? err.reason : undefined;
      const setup =
        err instanceof ApiError && err.status === 409 ? reason : undefined;
      setList((prev) => {
        // A failed refresh keeps the list it had, marked as not current;
        // only a setup refusal replaces it, since the list no longer applies.
        if (prev.kind === "loaded" && !setup) {
          return {
            kind: "loaded",
            data: prev.data,
            refreshError: failureText(reason),
          };
        }
        return { kind: "failed", message: failureText(reason), setup };
      });
      if (setup) setSelected(null);
    }
  }, []);

  const open = useCallback((ns: string) => {
    opener.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setSelected(ns);
  }, []);
  const close = useCallback(() => {
    setSelected(null);
    opener.current?.focus();
    opener.current = null;
  }, []);

  // Each status answer (one per cluster switch) starts a fresh list.
  useEffect(() => {
    void status;
    gen.current++;
    setSelected(null);
    setList({ kind: "idle" });
    if (mapped) void load();
  }, [status, mapped, load]);

  let body: ReactNode;
  if (!isClient || status.kind === "loading") {
    body = <p className="text-sm text-text-muted">Loading Rubrik status…</p>;
  } else if (status.kind === "disabled") {
    body = (
      <Alert variant="info">
        The Rubrik provider is not enabled. An operator turns it on with the
        Helm value <code>rubrik.enabled</code>.
      </Alert>
    );
  } else if (status.kind === "error") {
    body = (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="error">The Rubrik status could not be loaded.</Alert>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => void reloadRubrikStatus()}
        >
          Retry
        </Button>
      </div>
    );
  } else if (!mapped) {
    const state = status.status.cluster.state;
    body = (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="info">{setupText(state)}</Alert>
        {state === "unavailable" && (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => void reloadRubrikStatus()}
          >
            Retry
          </Button>
        )}
      </div>
    );
  } else if (list.kind === "idle" || list.kind === "loading") {
    body = <p className="text-sm text-text-muted">Loading namespaces…</p>;
  } else if (list.kind === "failed") {
    const lastSuccess = status.status.connection?.lastSuccessAt;
    body = list.setup ? (
      <Alert variant="info">{setupText(list.setup)}</Alert>
    ) : (
      <div className="flex flex-col items-start gap-2">
        <div role="alert" className="w-full">
          <Alert variant="error">
            {list.message}. No Rubrik data could be fetched, so protection is
            not shown.
            {lastSuccess && ` Last successful contact ${timeAgo(lastSuccess)}.`}
          </Alert>
        </div>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => void load()}
        >
          Retry
        </Button>
      </div>
    );
  } else if (list.data.noAccess) {
    body = (
      <Alert variant="info">
        You do not have Rubrik read access in any namespace of this cluster.
        Access is granted per namespace with a Kubernetes Role allowing{" "}
        <code>get</code> on <code>protections</code> in the API group{" "}
        <code>rubrik.kubecenter.io</code>. Ask a cluster admin to bind one to
        you.
      </Alert>
    );
  } else {
    body = (
      <NamespaceTable
        data={list.data}
        refreshing={!!list.refreshing}
        refreshError={list.refreshError}
        onOpen={open}
        onRefresh={() => void load()}
      />
    );
  }

  return (
    <div data-testid="rubrik-dashboard" className="flex flex-col gap-4 p-1">
      <div className="flex items-center gap-2">
        <h1 className="m-0 text-[18px] font-bold text-text-primary">Rubrik</h1>
        <span className="rounded-md border border-border-subtle px-1.5 py-px text-[11px] font-semibold text-text-muted">
          Preview
        </span>
      </div>
      {body}
      {selected && list.kind === "loaded" && (
        <DetailPanel key={selected} namespace={selected} onClose={close} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Namespace table

function FreshnessNote({
  freshness,
  what,
}: {
  freshness: Freshness | undefined;
  /** What the data are, plural and lower case: "namespaces", "snapshots". */
  what: string;
}) {
  if (!freshness) return null;
  if (freshness.stale) {
    return (
      <div role="alert">
        <Alert variant="warning">
          {failureText(freshness.error)}. Showing the {what} RSC last returned,
          fetched {timeAgo(freshness.fetchedAt)}.
        </Alert>
      </div>
    );
  }
  return (
    <p className="m-0 text-xs text-text-muted">
      {what[0]?.toUpperCase()}
      {what.slice(1)} fetched from RSC {timeAgo(freshness.fetchedAt)}
    </p>
  );
}

const TH = "px-3 py-2 text-left text-[12px] font-semibold text-text-muted";
const TD = "px-3 py-2 align-top text-[13px] text-text-primary";

/** What an object no namespace row lists covers, for admins (KTD4, R7). */
function coverageText(o: RubrikObject): string {
  if (o.relic) return "No longer seen by RSC";
  if (o.coverage.all) return "Every namespace";
  if (o.coverage.unresolved) return "Namespaces RSC does not resolve";
  const ns = o.coverage.namespaces ?? [];
  return ns.length > 0 ? ns.join(", ") : "No namespace";
}

function NamespaceTable({
  data,
  refreshing,
  refreshError,
  onOpen,
  onRefresh,
}: {
  data: NamespaceList;
  refreshing: boolean;
  refreshError: string | undefined;
  onOpen: (ns: string) => void;
  onRefresh: () => void;
}) {
  const objects = new Map(data.objects.map((o) => [o.id, o]));
  const rows = [...data.namespaces].sort((a, b) =>
    a.namespace.localeCompare(b.namespace),
  );
  const listed = new Set(data.namespaces.flatMap((r) => r.objectIds));
  const others = data.objects
    .filter((o) => !listed.has(o.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  // While the data may be out of date, "not protected" may only mean RSC
  // could not be asked about a namespace, so it is never asserted (AE6).
  const uncertain = !!data.freshness?.stale || !!refreshError;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <FreshnessNote freshness={data.freshness} what="namespaces" />
        <Button
          type="button"
          variant="secondary"
          size="sm"
          disabled={refreshing}
          onClick={onRefresh}
        >
          {refreshing ? "Refreshing…" : "Refresh"}
        </Button>
      </div>
      {refreshError && (
        <div role="alert">
          <Alert variant="error">
            {refreshError}. The list below is the one loaded before.
          </Alert>
        </div>
      )}
      {rows.length === 0 && others.length === 0 && (
        <p className="m-0 text-sm text-text-muted">
          No namespaces to show on this cluster.
        </p>
      )}
      {rows.length > 0 && (
        <div className="overflow-x-auto rounded-xl border border-border-subtle bg-surface">
          <table className="w-full border-collapse">
            <thead>
              <tr className="border-b border-border-subtle">
                <th className={TH}>Namespace</th>
                <th className={TH}>Protection</th>
                <th className={TH}>SLA domain</th>
                <th className={TH}>Compliance</th>
                <th className={TH}>Last snapshot</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const covering = row.objectIds
                  .map((id) => objects.get(id))
                  .filter((o): o is RubrikObject => !!o);
                const compliance =
                  COMPLIANCE[row.compliance] ?? COMPLIANCE.unknown;
                return (
                  <tr
                    key={row.namespace}
                    data-testid={`rubrik-ns-${row.namespace}`}
                    className="border-b border-border-subtle last:border-b-0"
                  >
                    <td className={TD}>
                      <button
                        type="button"
                        className="cursor-pointer border-0 bg-transparent p-0 font-mono text-[13px] text-accent underline"
                        onClick={() => onOpen(row.namespace)}
                      >
                        {row.namespace}
                      </button>
                    </td>
                    <td className={TD}>
                      {row.protected ? (
                        <StatusBadge label="Protected" tone="ok" />
                      ) : uncertain ? (
                        <StatusBadge label="Unknown" tone="warn" />
                      ) : (
                        <StatusBadge label="Not protected" tone="neutral" />
                      )}
                    </td>
                    <td className={TD}>
                      <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                        {covering.map((o) => (
                          <li key={o.id}>
                            {o.family === "protectionSet"
                              ? `${o.name}: ${slaName(o)}`
                              : slaName(o)}
                          </li>
                        ))}
                        {covering.length === 0 && !row.hiddenObjects && (
                          <li className="text-text-muted">—</li>
                        )}
                        {row.hiddenObjects && (
                          <li className="text-xs text-text-muted">
                            Also covered by objects you cannot see
                          </li>
                        )}
                      </ul>
                    </td>
                    <td className={TD}>
                      {row.protected ? (
                        <StatusBadge
                          label={compliance.label}
                          tone={compliance.tone}
                        />
                      ) : (
                        <span className="text-text-muted">—</span>
                      )}
                    </td>
                    <td className={TD}>
                      {row.lastSnapshot
                        ? timeAgo(row.lastSnapshot)
                        : row.hiddenObjects
                          ? "Not visible to you"
                          : "None"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {others.length > 0 && (
        <section
          aria-labelledby="rubrik-other-objects"
          className="flex flex-col gap-2"
        >
          <h2
            id="rubrik-other-objects"
            className="m-0 text-[14px] font-semibold text-text-primary"
          >
            Other Rubrik objects
          </h2>
          <p className="m-0 text-xs text-text-muted">
            Objects that no namespace above lists: sets covering every
            namespace, sets whose namespaces RSC does not resolve, and objects
            RSC no longer sees. Only admins see them.
          </p>
          <div className="overflow-x-auto rounded-xl border border-border-subtle bg-surface">
            <table className="w-full border-collapse">
              <thead>
                <tr className="border-b border-border-subtle">
                  <th className={TH}>Object</th>
                  <th className={TH}>Covers</th>
                  <th className={TH}>SLA domain</th>
                  <th className={TH}>Compliance</th>
                </tr>
              </thead>
              <tbody>
                {others.map((o) => {
                  const compliance =
                    COMPLIANCE[o.compliance] ?? COMPLIANCE.unknown;
                  return (
                    <tr
                      key={o.id}
                      data-testid={`rubrik-object-${o.id}`}
                      className="border-b border-border-subtle last:border-b-0"
                    >
                      <td className={TD}>
                        {o.family === "protectionSet"
                          ? `Protection set ${o.name}`
                          : `Namespace ${o.name}`}
                      </td>
                      <td className={TD}>{coverageText(o)}</td>
                      <td className={TD}>{slaName(o)}</td>
                      <td className={TD}>
                        <StatusBadge
                          label={compliance.label}
                          tone={compliance.tone}
                        />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Detail panel

type DetailState =
  | { kind: "loading" }
  | { kind: "loaded"; data: NamespaceDetail }
  | { kind: "gone" }
  | { kind: "failed"; message: ReactNode };

function DetailPanel({
  namespace,
  onClose,
}: {
  namespace: string;
  onClose: () => void;
}) {
  const [state, setState] = useState<DetailState>({ kind: "loading" });
  const [tab, setTab] = useState("snapshots");
  const gen = useRef(0);
  const heading = useRef<HTMLHeadingElement>(null);

  // The panel takes focus when it opens, so keyboard and screen-reader users
  // land in it; closing returns focus to the namespace button (see close).
  useEffect(() => {
    heading.current?.focus();
  }, []);

  const load = useCallback(async () => {
    const g = ++gen.current;
    setState({ kind: "loading" });
    try {
      const res = await apiGet<NamespaceDetail>(
        `${NAMESPACES_URL}/${encodeURIComponent(namespace)}`,
      );
      if (gen.current !== g) return;
      setState({ kind: "loaded", data: res.data });
    } catch (err) {
      if (gen.current !== g) return;
      if (err instanceof ApiError && err.status === 404) {
        setState({ kind: "gone" });
        return;
      }
      const reason = err instanceof ApiError ? err.reason : undefined;
      setState({
        kind: "failed",
        // A setup refusal (409) explains itself; anything else is a failure.
        message:
          err instanceof ApiError && err.status === 409 ? (
            setupText(reason ?? "")
          ) : (
            <>{failureText(reason)}.</>
          ),
      });
    }
  }, [namespace]);

  useEffect(() => {
    void load();
    return () => {
      gen.current++;
    };
  }, [load]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  let content: ReactNode;
  if (state.kind === "loading") {
    content = <p className="m-0 text-sm text-text-muted">Loading…</p>;
  } else if (state.kind === "gone") {
    content = (
      <Alert variant="info">
        This namespace is no longer visible to you in Rubrik.
      </Alert>
    );
  } else if (state.kind === "failed") {
    content = (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="error">{state.message}</Alert>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => void load()}
        >
          Retry
        </Button>
      </div>
    );
  } else {
    const { data } = state;
    content = (
      <div className="flex flex-col gap-3">
        <FreshnessNote freshness={data.freshness} what="objects" />
        {data.objects.length === 0 ? (
          <p className="m-0 text-sm text-text-muted">
            No Rubrik object you can see protects this namespace.
          </p>
        ) : (
          <Tabs
            activeTab={tab}
            onTabChange={setTab}
            tabs={[
              {
                id: "snapshots",
                label: "Snapshots",
                content: () => (
                  <ObjectSections objects={data.objects} part="snapshots" />
                ),
              },
              {
                id: "jobs",
                label: "Jobs",
                content: () => (
                  <ObjectSections objects={data.objects} part="jobs" />
                ),
              },
            ]}
          />
        )}
      </div>
    );
  }

  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-label={`Rubrik details for ${namespace}`}
      className="fixed inset-y-0 right-0 z-40 flex w-full max-w-[640px] flex-col gap-3 overflow-y-auto border-l border-border-subtle bg-surface p-5 shadow-xl"
    >
      <div className="flex items-center justify-between gap-2">
        <h2
          ref={heading}
          tabIndex={-1}
          className="m-0 font-mono text-[15px] font-semibold text-text-primary outline-none"
        >
          {namespace}
        </h2>
        <Button type="button" variant="secondary" size="sm" onClick={onClose}>
          Close
        </Button>
      </div>
      {content}
    </aside>
  );
}

function ObjectSections({
  objects,
  part,
}: {
  objects: ObjectDetail[];
  part: "snapshots" | "jobs";
}) {
  return (
    <div className="flex flex-col gap-4 pt-3">
      {objects.map((o) => (
        <section key={o.id} className="flex flex-col gap-2">
          <h3 className="m-0 text-[13.5px] font-semibold text-text-primary">
            {o.family === "protectionSet"
              ? `Protection set ${o.name}`
              : `Namespace ${o.name}`}
            <span className="font-normal text-text-muted">
              {" "}
              · SLA {slaName(o)}
              {o.relic ? " · no longer seen by RSC" : ""}
            </span>
          </h3>
          {part === "snapshots" ? (
            <SnapshotList object={o} />
          ) : (
            <JobList object={o} />
          )}
        </section>
      ))}
    </div>
  );
}

function PartError({ reason, what }: { reason: string; what: string }) {
  return (
    <Alert variant="warning">
      {what} could not be fetched: {failureText(reason)}.
    </Alert>
  );
}

function snapshotState(s: Snapshot): { label: string; tone: Tone } {
  if (s.corrupted) return { label: "Corrupted", tone: "crit" };
  if (s.quarantined) return { label: "Quarantined", tone: "crit" };
  if (s.expired) return { label: "Expired", tone: "neutral" };
  return { label: "Available", tone: "ok" };
}

function SnapshotList({ object }: { object: ObjectDetail }) {
  if (object.snapshotsError) {
    return <PartError reason={object.snapshotsError} what="Snapshots" />;
  }
  return (
    <>
      <FreshnessNote freshness={object.snapshotsFreshness} what="snapshots" />
      {object.snapshots.length === 0 ? (
        <p className="m-0 text-sm text-text-muted">No snapshots.</p>
      ) : (
        <table className="w-full border-collapse">
          <thead>
            <tr className="border-b border-border-subtle">
              <th className={TH}>Time</th>
              <th className={TH}>Status</th>
              <th className={TH}>Expires</th>
            </tr>
          </thead>
          <tbody>
            {object.snapshots.map((s) => {
              const st = snapshotState(s);
              return (
                <tr key={s.id} className="border-b border-border-subtle">
                  <td className={TD}>
                    {when(s.time)}
                    {s.onDemand && (
                      <span className="text-text-muted"> · on demand</span>
                    )}
                  </td>
                  <td className={TD}>
                    <StatusBadge label={st.label} tone={st.tone} />
                  </td>
                  <td className={TD}>{when(s.expires)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </>
  );
}

function JobList({ object }: { object: ObjectDetail }) {
  if (object.jobsError) {
    return <PartError reason={object.jobsError} what="Jobs" />;
  }
  return (
    <>
      <FreshnessNote freshness={object.jobsFreshness} what="jobs" />
      {object.jobs.length === 0 ? (
        <p className="m-0 text-sm text-text-muted">No recent jobs.</p>
      ) : (
        <table className="w-full border-collapse">
          <thead>
            <tr className="border-b border-border-subtle">
              <th className={TH}>Type</th>
              <th className={TH}>Status</th>
              <th className={TH}>Started</th>
              <th className={TH}>Details</th>
            </tr>
          </thead>
          <tbody>
            {object.jobs.map((j) => {
              const st = JOB_STATUS[j.status] ?? JOB_STATUS.unknown;
              return (
                <tr key={j.id} className="border-b border-border-subtle">
                  <td className={TD}>{j.type}</td>
                  <td className={TD}>
                    <StatusBadge label={st.label} tone={st.tone} />
                  </td>
                  <td className={TD}>{when(j.startTime)}</td>
                  <td className={`${TD} text-text-secondary`}>
                    {j.failureReason || j.message || "—"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </>
  );
}
