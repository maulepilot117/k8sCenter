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
import {
  FreshnessNote,
  failureText,
  RubrikNamespacePanel,
  setupText,
  slaName,
  TD,
  TH,
} from "@/components/rubrik/RubrikNamespacePanel.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge, { type Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type {
  RubrikCompliance as Compliance,
  RubrikNamespaceList as NamespaceList,
  RubrikObject,
} from "@/lib/rubrik-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
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
// Text

const COMPLIANCE: Record<Compliance, { label: string; tone: Tone }> = {
  in_compliance: { label: "In compliance", tone: "ok" },
  out_of_compliance: { label: "Out of compliance", tone: "crit" },
  unknown: { label: "Unknown", tone: "neutral" },
};

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

/** A Kubernetes namespace name (an RFC 1123 label). */
const NAMESPACE_NAME = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;

/** A notification link, as the page's query gave it. */
interface PendingLink {
  /** A valid namespace name, or absent. */
  namespace?: string;
  /** The cluster the link is about. */
  cluster: string;
}

type LinkNotice =
  | { kind: "cluster"; cluster: string }
  | { kind: "namespace"; namespace: string };

/**
 * The link the query describes, or null. A namespace that is not a valid
 * name is dropped, so a crafted query cannot put its own words on the page.
 */
function pendingFrom(
  namespace: string | undefined,
  cluster: string | undefined,
): PendingLink | null {
  const ns =
    namespace && NAMESPACE_NAME.test(namespace) ? namespace : undefined;
  if (!ns && !cluster) return null;
  return { namespace: ns, cluster: cluster || LOCAL_CLUSTER_ID };
}

/** Drops the link's query from the address, keeping the page. */
function forgetLinkQuery(): void {
  const { pathname, search, hash } = globalThis.location;
  if (!search) return;
  globalThis.history.replaceState(
    globalThis.history.state,
    "",
    pathname + hash,
  );
}

/** One row of GET /v1/clusters, the fields the notice names a cluster by. */
interface ClusterName {
  id: string;
  name: string;
  displayName?: string;
  isLocal?: boolean;
}

/**
 * Why a link is not being followed. A cluster is named as the picker names
 * it, read from /v1/clusters (admin-only, as remote links are); when that
 * fails it is "another cluster", never the query's own text.
 */
function LinkNoticeAlert({ notice }: { notice: LinkNotice }) {
  const [label, setLabel] = useState<string | null>(null);
  const cluster = notice.kind === "cluster" ? notice.cluster : null;
  useEffect(() => {
    setLabel(null);
    if (!cluster) return;
    let live = true;
    apiGet<ClusterName[]>("/v1/clusters", { clusterId: LOCAL_CLUSTER_ID })
      .then((res) => {
        const c = (Array.isArray(res.data) ? res.data : []).find((r) =>
          cluster === LOCAL_CLUSTER_ID ? r.isLocal : r.id === cluster,
        );
        if (live && c) setLabel(c.displayName || c.name || null);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [cluster]);
  if (notice.kind === "namespace") {
    return (
      <Alert variant="info">
        Namespace <code>{notice.namespace}</code> is not in your Rubrik list for
        this cluster. It may no longer be mapped to Rubrik, or you may not have
        Rubrik read access there.
      </Alert>
    );
  }
  return (
    <Alert variant="info">
      {label ? (
        <>
          This link is about the cluster <strong>{label}</strong>, not the one
          selected now. Choose it in the cluster picker to see its Rubrik data.
        </>
      ) : (
        <>
          This link is about another cluster, not the one selected now. Choose
          it in the cluster picker to see its Rubrik data.
        </>
      )}
    </Alert>
  );
}

interface RubrikDashboardProps {
  /**
   * The page's `?namespace=`, set by a notification link: its details open
   * once the first list shows it.
   */
  linkNamespace?: string;
  /** The page's `?cluster=`: the cluster the link is about (absent: local). */
  linkCluster?: string;
}

export default function RubrikDashboard({
  linkNamespace,
  linkCluster,
}: RubrikDashboardProps = {}) {
  const isClient = useIsClient();
  const status = useSignalValue(rubrikStatus);
  const [list, setList] = useState<ListState>({ kind: "idle" });
  const [selected, setSelected] = useState<string | null>(null);
  const gen = useRef(0);
  /** The namespace button that opened the panel, focused again on close. */
  const opener = useRef<HTMLElement | null>(null);
  /** The link, until a loaded list of its own cluster answers it. */
  const pendingLink = useRef<PendingLink | null>(
    pendingFrom(linkNamespace, linkCluster),
  );
  /** Why a link is not being followed. */
  const [linkNotice, setLinkNotice] = useState<LinkNotice | null>(null);

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
    // Opening a namespace by hand answers a "not in your list" notice.
    setLinkNotice((n) => (n?.kind === "namespace" ? null : n));
    setSelected(ns);
  }, []);
  const close = useCallback(() => {
    // A panel a link opened has no opener: focus its namespace's row button.
    const back =
      opener.current ??
      (selected
        ? document.querySelector<HTMLElement>(
            `[data-testid="rubrik-ns-${selected}"] button`,
          )
        : null);
    setSelected(null);
    back?.focus();
    opener.current = null;
  }, [selected]);

  // Each status answer (one per cluster switch) starts a fresh list.
  useEffect(() => {
    void status;
    gen.current++;
    setSelected(null);
    setList({ kind: "idle" });
    if (mapped) void load();
  }, [status, mapped, load]);

  // A pending link is checked against the selected cluster on every status
  // answer, since the selection can change without a reload (the switcher
  // falls back to local; deleting the selected cluster does too). A link
  // about another cluster waits: its namespace belongs to that cluster, and
  // switching there with the picker reloads this URL and follows it.
  useEffect(() => {
    if (status.kind === "loading") return;
    const link = pendingLink.current;
    if (!link) {
      setLinkNotice((n) => (n?.kind === "namespace" ? null : n));
      return;
    }
    setLinkNotice(
      link.cluster === selectedCluster.value
        ? null
        : { kind: "cluster", cluster: link.cluster },
    );
  }, [status]);

  // The first loaded list of the link's own cluster answers it: open the
  // namespace when the list shows it, else say why not. The server decides
  // visibility, so a namespace missing from the list is never opened. Once
  // answered, the query leaves the URL, so a later reload does not follow
  // the link again on whatever cluster is selected then.
  useEffect(() => {
    const link = pendingLink.current;
    if (!link || list.kind !== "loaded") return;
    if (link.cluster !== selectedCluster.value) return;
    pendingLink.current = null;
    forgetLinkQuery();
    const ns = link.namespace;
    if (!ns) return;
    if (
      !list.data.noAccess &&
      list.data.namespaces.some((r) => r.namespace === ns)
    ) {
      opener.current = null;
      setSelected(ns);
    } else {
      setLinkNotice({ kind: "namespace", namespace: ns });
    }
  }, [list]);

  let body: ReactNode;
  if (!isClient || status.kind === "loading") {
    body = <p className="text-sm text-text-muted">Loading Rubrik status…</p>;
  } else if (status.kind === "disabled") {
    body = (
      <Alert variant="info">
        The Rubrik provider is not enabled. It needs the k8sCenter PostgreSQL
        database, which this install does not have.
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
      {linkNotice && <LinkNoticeAlert notice={linkNotice} />}
      {body}
      {selected && list.kind === "loaded" && (
        <RubrikNamespacePanel
          key={selected}
          namespace={selected}
          writesEnabled={
            status.kind === "enabled" && status.status.writesEnabled
          }
          onClose={close}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Namespace table

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
