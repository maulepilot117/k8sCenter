/** The Rubrik detail of one namespace (U13, U14): its objects with their
 * snapshots and recent jobs, Take snapshot and Change SLA, and a restore from
 * a snapshot, with the status of each write started here.
 *
 * It reads GET /v1/rubrik/namespaces/{namespace}. The Backups page opens it
 * for a Rubrik row; the Rubrik dashboard opened it for a namespace. The
 * helpers it exports are the wording the Rubrik pages share.
 */

import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { RubrikJobStatus } from "@/components/rubrik/RubrikJobStatus.tsx";
import {
  RubrikRestoreDialog,
  RubrikWriteControls,
  restorable,
  useOperationIds,
} from "@/components/rubrik/RubrikWriteDialogs.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge, { type Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { Tabs } from "@/components/ui/Tabs.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type {
  RubrikFreshness as Freshness,
  RubrikJobState as JobStatus,
  RubrikNamespaceDetail as NamespaceDetail,
  RubrikObjectDetail as ObjectDetail,
  RubrikObject,
  RubrikOperation,
  RubrikSnapshot as Snapshot,
} from "@/lib/rubrik-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

// Full literal: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const NAMESPACES_URL = "/v1/rubrik/namespaces";

// ---------------------------------------------------------------------------
// Shared wording

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

export function failureText(reason: string | undefined): string {
  return (
    (reason && FAILURE_TEXT[reason]) || "The Rubrik data could not be loaded"
  );
}

const SETTINGS_LINK = (
  <a className="text-accent underline" href="/settings/rubrik">
    Rubrik settings
  </a>
);

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
export function setupText(state: string): ReactNode {
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

export function when(t: string | undefined): string {
  return t ? new Date(t).toLocaleString() : "—";
}

export function slaName(o: RubrikObject): string {
  return o.effectiveSla?.name || "No SLA";
}

export function FreshnessNote({
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

export const TH =
  "px-3 py-2 text-left text-[12px] font-semibold text-text-muted";
export const TD = "px-3 py-2 align-top text-[13px] text-text-primary";

// ---------------------------------------------------------------------------
// Detail panel

type DetailState =
  | { kind: "loading" }
  | { kind: "loaded"; data: NamespaceDetail }
  | { kind: "gone" }
  | { kind: "failed"; message: ReactNode };

export function RubrikNamespacePanel({
  namespace,
  writesEnabled,
  onClose,
}: {
  namespace: string;
  writesEnabled: boolean;
  onClose: () => void;
}) {
  const [state, setState] = useState<DetailState>({ kind: "loading" });
  const [tab, setTab] = useState("snapshots");
  /** Writes started from this panel, newest first, shown with their status. */
  const [ops, setOps] = useState<RubrikOperation[]>([]);
  const [restoring, setRestoring] = useState<{
    object: ObjectDetail;
    snapshot: Snapshot;
  } | null>(null);
  const gen = useRef(0);
  const heading = useRef<HTMLHeadingElement>(null);
  // Held here, so a request re-sent from a dialog opened again keeps its id.
  const operationIds = useOperationIds();

  // The panel takes focus when it opens, so keyboard and screen-reader users
  // land in it; closing returns focus to the namespace button (see close).
  useEffect(() => {
    heading.current?.focus();
  }, []);

  /** Reads the namespace; `quiet` keeps what is shown while it reads. */
  const load = useCallback(
    async (quiet = false) => {
      const g = ++gen.current;
      if (!quiet) setState({ kind: "loading" });
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
    },
    [namespace],
  );

  useEffect(() => {
    void load();
    return () => {
      gen.current++;
    };
  }, [load]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // A dialog opened from the panel takes Escape for itself.
      if (e.key !== "Escape") return;
      if (document.querySelector('[aria-modal="true"]')) return;
      onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  // A started write shows its status here; when it settles the namespace is
  // read again, so the new snapshot, job or SLA shows.
  const started = useCallback((op: RubrikOperation) => {
    setRestoring(null);
    setOps((prev) => [op, ...prev.filter((o) => o.id !== op.id)]);
  }, []);
  const settled = useCallback(() => {
    void load(true);
  }, [load]);

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
        <RubrikWriteControls
          namespace={namespace}
          detail={data}
          writesEnabled={writesEnabled}
          operationIds={operationIds}
          onStarted={started}
        />
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
                  <ObjectSections
                    objects={data.objects}
                    part="snapshots"
                    onRestore={(object, snapshot) =>
                      setRestoring({ object, snapshot })
                    }
                  />
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
      {ops.length > 0 && (
        <div className="flex flex-col gap-2" aria-label="Rubrik operations">
          {ops.map((op) => (
            <RubrikJobStatus key={op.id} operation={op} onSettled={settled} />
          ))}
        </div>
      )}
      {content}
      {restoring && (
        <RubrikRestoreDialog
          namespace={namespace}
          object={restoring.object}
          snapshot={restoring.snapshot}
          operationIds={operationIds}
          onStarted={started}
          onClose={() => setRestoring(null)}
        />
      )}
    </aside>
  );
}

function ObjectSections({
  objects,
  part,
  onRestore,
}: {
  objects: ObjectDetail[];
  part: "snapshots" | "jobs";
  onRestore?: (object: ObjectDetail, snapshot: Snapshot) => void;
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
            <SnapshotList object={o} onRestore={onRestore} />
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

function SnapshotList({
  object,
  onRestore,
}: {
  object: ObjectDetail;
  onRestore?: (object: ObjectDetail, snapshot: Snapshot) => void;
}) {
  // The restore control follows the server's flag for the object; a
  // snapshot RSC marks expired, corrupted or quarantined is never offered.
  const canRestore = object.canRestore && !!onRestore;
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
              {canRestore && (
                <th className={TH}>
                  <span className="sr-only">Actions</span>
                </th>
              )}
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
                  {canRestore && (
                    <td className={TD}>
                      {restorable(s) && (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          aria-label={`Restore the snapshot from ${when(s.time)}`}
                          onClick={() => onRestore?.(object, s)}
                        >
                          Restore
                        </Button>
                      )}
                    </td>
                  )}
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
