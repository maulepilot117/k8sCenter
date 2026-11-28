/** Protection overview — one row per namespace with each backup provider's
 * last good recovery point (U15, R20–R22, KTD14).
 *
 * The rows are the namespaces the user can list, so a namespace no provider
 * protects still shows. Velero data come from the Velero backups endpoint
 * and Rubrik data from the Rubrik namespaces endpoint, each under its own
 * access rules (R21); lib/protection-overview.ts merges them. The Rubrik
 * column shows only while the Rubrik provider is on.
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
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type { APIResponse } from "@/lib/k8s-types.ts";
import {
  overviewRows,
  providerNamespaces,
  type RubrikCell,
  type RubrikInput,
  type VeleroCell,
  type VeleroInput,
} from "@/lib/protection-overview.ts";
import type { RubrikNamespaceList } from "@/lib/rubrik-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import type { Backup, VeleroStatus } from "@/lib/velero-types.ts";
import { clusterEpoch, selectedCluster } from "@/src/lib/cluster.ts";
import {
  ensureRubrikStatus,
  rubrikNavVisible,
  rubrikStatus,
} from "@/src/lib/rubrik-status.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

// Full literals: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const NAMESPACES_URL = "/v1/resources/namespaces";
const VELERO_STATUS_URL = "/v1/velero/status";
const VELERO_BACKUPS_URL = "/v1/velero/backups";
const RUBRIK_NAMESPACES_URL = "/v1/rubrik/namespaces";

/** RSC's own failure classes: everything else is k8sCenter's. */
const RSC_FAILURE: Record<string, string> = {
  auth_failed: "RSC refused k8sCenter's credentials",
  unreachable: "RSC could not be reached",
  rate_limited: "RSC is rate limiting requests",
  rsc_error: "RSC returned an answer k8sCenter could not use",
};

/** Setup reasons only admins are sent (non-admins get not_set_up). */
const ADMIN_SETUP = new Set([
  "not_configured",
  "not_mapped",
  "mapping_invalid",
  "rsc_cluster_missing",
]);

/** /v1/velero/status also carries why Velero was not detected (remote). */
type VeleroStatusWire = VeleroStatus & { reason?: string };

/**
 * Velero as its status says: not detected means not installed only when
 * discovery found no Velero; a remote cluster that could not be asked
 * (unreachable, forbidden) is an error, not an absence.
 */
function veleroFrom(
  st: VeleroStatusWire,
  backups: PromiseSettledResult<APIResponse<Backup[]>>,
): VeleroInput {
  if (!st.detected) {
    return !st.reason || st.reason === "discovery_missing"
      ? { kind: "not_installed" }
      : { kind: "error" };
  }
  if (backups.status === "rejected") return { kind: "error" };
  if (backups.value.metadata?.accessDenied) return { kind: "denied" };
  const data = backups.value.data;
  return { kind: "loaded", backups: Array.isArray(data) ? data : [] };
}

/** At most this many pages of 500 namespaces are read. */
const MAX_NAMESPACE_PAGES = 40;

/**
 * Every namespace the user can list. The list endpoint pages (100 by
 * default), so this follows the continue token. `truncated` is set when a
 * remote read hit its paging cap or the page bound was reached.
 */
async function listNamespaces(): Promise<{
  names: string[];
  truncated: boolean;
}> {
  const names: string[] = [];
  let token = "";
  let truncated = false;
  for (let page = 0; page < MAX_NAMESPACE_PAGES; page++) {
    const query = token
      ? `?limit=500&continue=${encodeURIComponent(token)}`
      : "?limit=500";
    const res = await apiGet<Array<{ metadata: { name: string } }>>(
      `${NAMESPACES_URL}${query}`,
    );
    if (!Array.isArray(res.data)) throw new Error("no namespace list");
    names.push(...res.data.map((n) => n.metadata.name));
    truncated ||= !!res.metadata?.truncated;
    token = res.metadata?.continue ?? "";
    if (!token) return { names, truncated };
  }
  return { names, truncated: true };
}

type Base =
  | { kind: "loading" }
  | { kind: "failed" }
  | {
      kind: "loaded";
      namespaces: string[];
      velero: VeleroInput;
      /**
       * The user may not list namespaces (403): the rows are the namespaces
       * the providers' own answers name instead.
       */
      restricted?: boolean;
      /** The namespace list was cut short. */
      truncated?: boolean;
    };

export default function ProtectionOverview() {
  const isClient = useIsClient();
  const cluster = useSignalValue(selectedCluster);
  const epoch = useSignalValue(clusterEpoch);
  const status = useSignalValue(rubrikStatus);
  const rubrikKnownOn = useSignalValue(rubrikNavVisible);
  const [base, setBase] = useState<Base>({ kind: "loading" });
  /** Bumped by Refresh to read Rubrik again. */
  const [rubrikTick, setRubrikTick] = useState(0);
  /**
   * The fetch time of the last Rubrik list this page loaded, per cluster:
   * what an RSC failure with no earlier answer reports as the last fetch.
   */
  const lastFetched = useRef(new Map<string, string>());
  const [rubrikList, setRubrikList] = useState<RubrikInput>({
    kind: "loading",
  });
  const baseGen = useRef(0);
  const rubrikGen = useRef(0);

  useEffect(() => {
    ensureRubrikStatus();
  }, []);

  // The namespaces and Velero, once per cluster.
  const loadBase = useCallback(async () => {
    const g = ++baseGen.current;
    setBase({ kind: "loading" });
    const [ns, veleroStatus, backups] = await Promise.allSettled([
      listNamespaces(),
      apiGet<VeleroStatusWire>(VELERO_STATUS_URL),
      apiGet<Backup[]>(VELERO_BACKUPS_URL),
    ]);
    if (baseGen.current !== g) return;
    const restricted =
      ns.status === "rejected" &&
      ns.reason instanceof ApiError &&
      ns.reason.status === 403;
    if (ns.status === "rejected" && !restricted) {
      setBase({ kind: "failed" });
      return;
    }
    const velero: VeleroInput =
      veleroStatus.status === "rejected" || !veleroStatus.value.data
        ? { kind: "error" }
        : veleroFrom(veleroStatus.value.data, backups);
    setBase({
      kind: "loaded",
      namespaces: ns.status === "fulfilled" ? ns.value.names : [],
      velero,
      restricted,
      truncated: ns.status === "fulfilled" && ns.value.truncated,
    });
  }, []);

  useEffect(() => {
    void cluster;
    void epoch;
    void loadBase();
    return () => {
      baseGen.current++;
    };
  }, [cluster, epoch, loadBase]);

  // Rubrik: each status answer (one per cluster switch) decides what to ask.
  useEffect(() => {
    const g = ++rubrikGen.current;
    if (status.kind !== "enabled") {
      setRubrikList(
        status.kind === "disabled"
          ? { kind: "disabled" }
          : status.kind === "error"
            ? { kind: "error" }
            : { kind: "loading" },
      );
      return;
    }
    const state = status.status.cluster.state;
    if (state !== "mapped") {
      setRubrikList(
        state === "admin_only"
          ? { kind: "not_applicable" }
          : state === "unavailable"
            ? { kind: "error" }
            : { kind: "not_set_up", setupLink: ADMIN_SETUP.has(state) },
      );
      return;
    }
    void rubrikTick;
    setRubrikList({ kind: "loading" });
    const forCluster = selectedCluster.peek();
    apiGet<RubrikNamespaceList>(RUBRIK_NAMESPACES_URL)
      .then((res) => {
        if (rubrikGen.current !== g) return;
        const fetchedAt = res.data.freshness?.fetchedAt;
        if (fetchedAt) lastFetched.current.set(forCluster, fetchedAt);
        setRubrikList({ kind: "loaded", list: res.data });
      })
      .catch((err) => {
        if (rubrikGen.current !== g) return;
        const reason = err instanceof ApiError ? err.reason : undefined;
        const code = err instanceof ApiError ? err.status : 0;
        if (code === 409) {
          setRubrikList({
            kind: "not_set_up",
            setupLink: ADMIN_SETUP.has(reason ?? ""),
          });
        } else if (code === 403) {
          setRubrikList({ kind: "not_applicable" });
        } else if (reason && RSC_FAILURE[reason]) {
          setRubrikList({
            kind: "rsc_failing",
            reason,
            // The last answer this page saw, else the connection's own
            // record (admins only, when the server keeps one).
            lastFetchedAt:
              lastFetched.current.get(forCluster) ??
              status.status.connection?.lastSuccessAt,
          });
        } else {
          setRubrikList({ kind: "error" });
        }
      });
  }, [status, rubrikTick]);

  let body: ReactNode;
  if (!isClient || base.kind === "loading") {
    body = <p className="text-sm text-text-muted">Loading namespaces…</p>;
  } else if (base.kind === "failed") {
    body = (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="error">The namespaces could not be loaded.</Alert>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => void loadBase()}
        >
          Retry
        </Button>
      </div>
    );
  } else {
    const names = base.restricted
      ? providerNamespaces(base.velero, rubrikList)
      : base.namespaces;
    const rows = overviewRows(names, base.velero, rubrikList);
    // While the status is still loading the column shows only if Rubrik was
    // on at the last answer, so it does not flash in when the provider is off.
    const showRubrik =
      rubrikList.kind !== "disabled" &&
      (rubrikList.kind !== "loading" ||
        status.kind !== "loading" ||
        rubrikKnownOn);
    const fresh =
      rubrikList.kind === "loaded" ? rubrikList.list.freshness : undefined;
    body = (
      <div className="flex flex-col gap-3">
        <div className="flex justify-end">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => {
              void loadBase();
              setRubrikTick((t) => t + 1);
            }}
          >
            Refresh
          </Button>
        </div>
        {base.restricted && (
          <Alert variant="info">
            You may not list this cluster's namespaces, so the overview shows
            the namespaces the backup providers show you.
          </Alert>
        )}
        {base.truncated && (
          <Alert variant="warning">
            The namespace list was cut short, so some namespaces may be missing.
          </Alert>
        )}
        {base.velero.kind === "denied" && (
          <Alert variant="info">
            You may not list Velero backups on this cluster, so the overview
            cannot show any.
          </Alert>
        )}
        {fresh?.stale && (
          <div role="alert">
            <Alert variant="warning">
              {RSC_FAILURE[fresh.error ?? ""] ?? "RSC could not be asked"}. The
              Rubrik column shows what RSC last returned, fetched{" "}
              {timeAgo(fresh.fetchedAt)}.
            </Alert>
          </div>
        )}
        {rows.length === 0 ? (
          <p className="m-0 text-sm text-text-muted">
            No namespaces to show on this cluster.
          </p>
        ) : (
          <div className="overflow-x-auto rounded-xl border border-border-subtle bg-surface">
            <table className="w-full border-collapse">
              <thead>
                <tr className="border-b border-border-subtle">
                  <th className={TH}>Namespace</th>
                  <th className={TH}>
                    <a
                      className="text-text-muted underline"
                      href="/backup/backups"
                    >
                      Velero
                    </a>
                  </th>
                  {showRubrik && (
                    <th className={TH}>
                      <a
                        className="text-text-muted underline"
                        href="/backup/rubrik"
                      >
                        Rubrik
                      </a>{" "}
                      <span className="rounded-md border border-border-subtle px-1 py-px text-[10.5px]">
                        Preview
                      </span>
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr
                    key={row.namespace}
                    data-testid={`overview-${row.namespace}`}
                    className="border-b border-border-subtle last:border-b-0"
                  >
                    <td className={`${TD} font-mono`}>{row.namespace}</td>
                    <td className={TD} data-testid="velero-cell">
                      <VeleroCellView cell={row.velero} />
                    </td>
                    {showRubrik && row.rubrik && (
                      <td className={TD} data-testid="rubrik-cell">
                        <RubrikCellView cell={row.rubrik} />
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    );
  }

  return (
    <div data-testid="protection-overview" className="flex flex-col gap-4 p-1">
      <div>
        <h1 className="m-0 text-[18px] font-bold text-text-primary">
          Protection overview
        </h1>
        <p className="m-0 mt-1 text-[13px] text-text-muted">
          Every namespace you can see, with each backup provider's last good
          recovery point.
        </p>
      </div>
      {body}
    </div>
  );
}

const TH = "px-3 py-2 text-left text-[12px] font-semibold text-text-muted";
const TD = "px-3 py-2 align-top text-[13px] text-text-primary";

function Muted({ children }: { children: ReactNode }) {
  return <span className="text-text-muted">{children}</span>;
}

function Detail({ children }: { children: ReactNode }) {
  return (
    <span className="mt-0.5 block text-xs text-text-muted">{children}</span>
  );
}

function VeleroCellView({ cell }: { cell: VeleroCell }) {
  switch (cell.kind) {
    case "loading":
      return <Muted>Loading…</Muted>;
    case "error":
      return <Muted>Could not load</Muted>;
    case "not_installed":
      return <Muted>Velero not installed</Muted>;
    case "denied":
      return <Muted>You may not list Velero backups</Muted>;
    case "none":
      return <Muted>No backup</Muted>;
    case "none_visible":
      return <Muted>No backups visible to you</Muted>;
    case "protected":
      return (
        <>
          <StatusBadge label="Protected" tone="ok" />
          <Detail>
            Last backup {cell.last.at ? timeAgo(cell.last.at) : "—"} (
            {cell.last.name})
          </Detail>
        </>
      );
    case "failed":
      return (
        <>
          <StatusBadge label="Last backup failed" tone="crit" />
          <Detail>
            {cell.lastGood
              ? `Last good backup ${cell.lastGood.at ? timeAgo(cell.lastGood.at) : "—"} (${cell.lastGood.name})`
              : "No completed backup"}
          </Detail>
        </>
      );
  }
}

function RubrikCellView({ cell }: { cell: RubrikCell }) {
  switch (cell.kind) {
    case "loading":
      return <Muted>Loading…</Muted>;
    case "error":
      return <Muted>Could not load</Muted>;
    case "not_applicable":
      // One rendering for "not covered" and "no access" alike (R22).
      return <Muted>Not applicable</Muted>;
    case "not_set_up":
      return (
        <>
          <Muted>Not set up for this cluster</Muted>
          {cell.setupLink && (
            <Detail>
              <a
                className="text-accent underline"
                href="/settings/general#rubrik"
              >
                Set up Rubrik
              </a>
            </Detail>
          )}
        </>
      );
    case "rsc_failing":
      return (
        <>
          <StatusBadge label="RSC unavailable" tone="warn" />
          <Detail>
            {RSC_FAILURE[cell.reason ?? ""] ?? "RSC could not be asked"}
            {cell.fetchedAt
              ? `; last fetched ${timeAgo(cell.fetchedAt)}`
              : "; no earlier answer to show"}
          </Detail>
        </>
      );
    case "protected":
      return (
        <>
          <StatusBadge label="Protected" tone="ok" />
          <Detail>
            {cell.lastSnapshot
              ? `Last snapshot ${timeAgo(cell.lastSnapshot)}`
              : "Last snapshot not visible to you"}
            {cell.stale ? `, as of ${timeAgo(cell.stale.fetchedAt)}` : ""}
          </Detail>
        </>
      );
    case "unprotected":
      return <Muted>Not protected</Muted>;
  }
}
