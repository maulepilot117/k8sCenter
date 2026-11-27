/** Rubrik settings — the RSC connection and cluster mappings (U12, F1).
 *
 * Admins register the installation's one RSC connection (tenant URL, client
 * ID, client secret), see its health, and map each k8sCenter cluster to the
 * RSC Kubernetes cluster that protects it. Everyone else is told the page is
 * for admins and is shown no form.
 *
 * The client secret is write-only. The API never returns it, so the form
 * shows only whether one is stored, and the field is emptied after every
 * save. A stored secret is never carried to another tenant: moving the URL
 * to another origin needs the secret entered again, and the save clears
 * every mapping, since RSC cluster ids belong to the old tenant.
 *
 * Whether the provider is on comes from the shared status store
 * (src/lib/rubrik-status.ts): only a 404 means "not enabled", any other
 * failure is "could not load".
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
import { ApiError, apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { sameOrigin } from "@/lib/origin.ts";
import type {
  RubrikClusterState,
  RubrikConnection,
  RubrikConnectionHealth,
  RubrikMapping,
  RubrikStatus,
} from "@/lib/rubrik-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";
import {
  ensureRubrikStatus,
  reloadRubrikStatus,
  rubrikStatus,
} from "@/src/lib/rubrik-status.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

// Full literals: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const CONNECTION_URL = "/v1/rubrik/connection";
const MAPPINGS_URL = "/v1/rubrik/mappings";
const RSC_CLUSTERS_URL = "/v1/rubrik/rsc-clusters";
const CLUSTERS_URL = "/v1/clusters";

/** PUT /v1/rubrik/connection's answer. */
interface ConnectionSaved extends RubrikConnection {
  mappingsCleared: boolean;
}

/** One RSC Kubernetes cluster for the mapping picker. */
interface RscCluster {
  family: "namespace" | "protectionSet";
  id: string;
  name: string;
  status?: string;
  /** The k8sCenter cluster already mapped to it, if any. */
  mappedTo?: string;
}

/** The fields of GET /v1/clusters this page reads. */
interface ClusterRecord {
  id: string;
  name: string;
  displayName: string;
  isLocal: boolean;
}

const INPUT_CLASS =
  "w-full rounded-[9px] border border-border-subtle bg-elevated px-3 py-2.5 font-mono text-[13.5px] text-text-primary outline-none focus:border-accent";
const LABEL_CLASS =
  "mb-1.5 block text-[12.5px] font-semibold text-text-secondary";
const SECTION_CLASS =
  "rounded-xl border border-border-subtle bg-surface p-5 flex flex-col gap-3";
const HEADING_CLASS = "m-0 text-[15px] font-semibold text-text-primary";

const HEALTH: Record<
  RubrikConnectionHealth,
  { label: string; tone: "success" | "warning" | "error" | "muted" }
> = {
  ok: { label: "Connected", tone: "success" },
  unknown: { label: "Saved, not contacted yet", tone: "muted" },
  auth_failed: { label: "Authentication failed", tone: "error" },
  unreachable: { label: "Unreachable", tone: "error" },
  failing: { label: "Failing", tone: "error" },
  secret_required: { label: "Client secret required", tone: "warning" },
  unavailable: { label: "Could not be read", tone: "warning" },
  not_configured: { label: "Not configured", tone: "muted" },
};

const TONE_CLASS = {
  success: "text-success",
  warning: "text-warning",
  error: "text-danger",
  muted: "text-text-muted",
};

/**
 * The server's own message for a refusal it explains (400, 404, 409) and its
 * fixed sentence for an RSC or store failure (502, 503), which never carries
 * RSC's own text. Anything else gets the fallback.
 */
function errorMessage(err: unknown, fallback: string): string {
  if (
    err instanceof ApiError &&
    [400, 404, 409, 502, 503].includes(err.status) &&
    err.detail
  ) {
    return err.detail;
  }
  return fallback;
}

/** The id the Rubrik API uses for a cluster record: "local" for the local one. */
function mappingId(c: ClusterRecord): string {
  return c.isLocal ? LOCAL_CLUSTER_ID : c.id;
}

/** The newer of two views of the one connection. */
function newer(
  a: RubrikConnection | undefined,
  b: RubrikConnection | undefined,
): RubrikConnection | undefined {
  if (!a) return b;
  if (!b) return a;
  return (b.revision ?? 0) > (a.revision ?? 0) ? b : a;
}

export default function RubrikSettings() {
  const isClient = useIsClient();
  const auth = useAuth();
  const user = useSignalValue(auth.user);
  const authLoading = useSignalValue(auth.loading);
  const authLoadAttempted = useSignalValue(auth.loadAttempted);
  const status = useSignalValue(rubrikStatus);
  /** The connection as the last save returned it. */
  const [saved, setSaved] = useState<RubrikConnection | undefined>();
  /**
   * The last enabled answer. A reload (after a save, a conflict or a cluster
   * switch) puts the store back to "loading"; the page keeps showing this
   * meanwhile instead of unmounting the forms and what they show.
   */
  const [lastEnabled, setLastEnabled] = useState<RubrikStatus | null>(null);
  /**
   * Why the last save was refused because the connection changed meanwhile.
   * Held here, not in the form: the reload that follows brings a new
   * revision, which remounts the forms.
   */
  const [conflict, setConflict] = useState<string | null>(null);

  useEffect(() => {
    ensureRubrikStatus();
  }, []);
  useEffect(() => {
    if (status.kind === "enabled") setLastEnabled(status.status);
  }, [status]);

  const onConflict = useCallback(() => {
    setConflict(
      "The Rubrik connection changed while you were editing, so nothing was saved. The page now shows the saved connection: check it and try again.",
    );
    void reloadRubrikStatus();
  }, []);
  const clearConflict = useCallback(() => setConflict(null), []);

  const shown =
    status.kind === "enabled"
      ? status.status
      : status.kind === "loading"
        ? lastEnabled
        : null;

  let body: ReactNode;
  if (!isClient || (!user && (authLoading || !authLoadAttempted))) {
    body = <p className="text-sm text-text-muted">Loading Rubrik settings…</p>;
  } else if (!user) {
    body = (
      <Alert variant="error">
        Your sign-in details could not be loaded. Reload the page.
      </Alert>
    );
  } else if (!user.roles?.includes("admin")) {
    body = (
      <Alert variant="info">
        Only admins can see and change the Rubrik connection and cluster
        mappings.
      </Alert>
    );
  } else if (shown === null && status.kind === "loading") {
    body = <p className="text-sm text-text-muted">Loading Rubrik status…</p>;
  } else if (status.kind === "disabled") {
    body = (
      <Alert variant="info">
        The Rubrik provider is not enabled. An operator turns it on with the
        Helm value <code>rubrik.enabled</code>; see the Rubrik operator guide.
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
  } else if (shown !== null) {
    const connection = newer(shown.connection, saved) ?? {
      configured: false,
      hasSecret: false,
      health: "not_configured" as const,
    };
    body = (
      <>
        <p className="m-0 text-[13px] text-text-muted">
          Writes (snapshots, SLA changes, restores) are{" "}
          {shown.writesEnabled ? "on" : "off"}. They are set with the Helm value{" "}
          <code>rubrik.writesEnabled</code>.
        </p>
        {conflict && (
          <div role="alert">
            <Alert variant="warning">{conflict}</Alert>
          </div>
        )}
        <ConnectionForm
          key={`connection-${connection.revision ?? 0}`}
          connection={connection}
          onConflict={onConflict}
          onSaved={(view, mappingsCleared) => {
            setConflict(null);
            setSaved(view);
            showToast("Rubrik connection saved", "success");
            if (mappingsCleared) {
              showToast(
                "The tenant changed, so every cluster mapping was cleared. Map the clusters again.",
                "info",
              );
            }
            void reloadRubrikStatus();
          }}
        />
        {connection.configured && connection.hasSecret ? (
          <Mappings
            key={`mappings-${connection.revision ?? 0}`}
            connection={connection}
            onConflict={onConflict}
            onSaved={clearConflict}
          />
        ) : (
          <section className={SECTION_CLASS}>
            <h2 className={HEADING_CLASS}>Cluster mappings</h2>
            <p className="m-0 text-sm text-text-muted">
              Save the connection first. The mappings list the RSC clusters the
              connection can see.
            </p>
          </section>
        )}
      </>
    );
  }

  return (
    <div data-testid="rubrik-settings" className="flex flex-col gap-4 p-1">
      <div className="flex items-center gap-2">
        <h2 className="m-0 text-[18px] font-bold text-text-primary">Rubrik</h2>
        <span className="rounded-md border border-border-subtle px-1.5 py-px text-[11px] font-semibold text-text-muted">
          Preview
        </span>
      </div>
      {body}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Connection

function ConnectionForm({
  connection,
  onSaved,
  onConflict,
}: {
  connection: RubrikConnection;
  onSaved: (view: RubrikConnection, mappingsCleared: boolean) => void;
  /** The save was refused because the connection changed meanwhile. */
  onConflict: () => void;
}) {
  // Helm's rubrik.tenantUrl pre-fills an unsaved connection; a saved one wins.
  const suggested = connection.configured
    ? ""
    : (connection.suggestedTenantUrl ?? "");
  const [tenantUrl, setTenantUrl] = useState(connection.tenantUrl ?? suggested);
  const showSuggested = suggested !== "" && tenantUrl.trim() === suggested;
  const [clientId, setClientId] = useState(connection.clientId ?? "");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);
  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );

  // The same origin rule the server applies (lib/origin.ts).
  const originMoved =
    connection.configured &&
    tenantUrl.trim() !== "" &&
    !sameOrigin(connection.tenantUrl ?? "", tenantUrl.trim());
  const needsSecret = !connection.hasSecret || originMoved;
  const health = HEALTH[connection.health] ?? HEALTH.unknown;

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, string> = {
        tenantUrl: tenantUrl.trim(),
        clientId: clientId.trim(),
      };
      // Omitted keeps the stored secret (same origin only); a value replaces it.
      if (secret !== "") body.clientSecret = secret;
      const res = await apiPut<ConnectionSaved>(CONNECTION_URL, body);
      // The secret leaves the page's state the moment the save lands.
      setSecret("");
      const { mappingsCleared, ...view } = res.data;
      onSaved(view, mappingsCleared);
    } catch (err) {
      if (err instanceof ApiError && err.reason === "connection_changed") {
        // The page shows it: the reload that follows remounts this form.
        onConflict();
        return;
      }
      if (!mounted.current) return;
      setError(errorMessage(err, "The connection could not be saved."));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <section className={SECTION_CLASS} aria-labelledby="rubrik-connection">
      <h2 id="rubrik-connection" className={HEADING_CLASS}>
        Connection
      </h2>
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-[13px]">
        <span data-testid="rubrik-health">
          Health:{" "}
          <span className={`font-semibold ${TONE_CLASS[health.tone]}`}>
            {health.label}
          </span>
        </span>
        {connection.lastSuccessAt && (
          <span className="text-text-muted">
            Last successful contact {timeAgo(connection.lastSuccessAt)}
          </span>
        )}
        {connection.lastFailureAt && (
          <span className="text-text-muted">
            Last failure {timeAgo(connection.lastFailureAt)}
          </span>
        )}
        {connection.updatedBy && connection.updatedAt && (
          <span className="text-text-muted">
            Saved by {connection.updatedBy} {timeAgo(connection.updatedAt)}
          </span>
        )}
      </div>
      <div className="flex max-w-[560px] flex-col gap-2.5">
        <div>
          <label className={LABEL_CLASS} htmlFor="rubrik-tenant-url">
            Tenant URL
          </label>
          <input
            id="rubrik-tenant-url"
            type="text"
            className={INPUT_CLASS}
            value={tenantUrl}
            autoComplete="off"
            placeholder="https://<tenant>.my.rubrik.com"
            aria-describedby={
              showSuggested ? "rubrik-tenant-url-note" : undefined
            }
            onChange={(e) => setTenantUrl(e.currentTarget.value)}
          />
          {showSuggested && (
            <p
              id="rubrik-tenant-url-note"
              className="mt-1.5 text-xs text-text-muted"
            >
              Filled in from the Helm value <code>rubrik.tenantUrl</code>.
              Nothing is saved until you enter the client ID and secret and
              save.
            </p>
          )}
        </div>
        <div>
          <label className={LABEL_CLASS} htmlFor="rubrik-client-id">
            Client ID
          </label>
          <input
            id="rubrik-client-id"
            type="text"
            className={INPUT_CLASS}
            value={clientId}
            autoComplete="off"
            onChange={(e) => setClientId(e.currentTarget.value)}
          />
        </div>
        <div>
          <label className={LABEL_CLASS} htmlFor="rubrik-client-secret">
            Client secret
          </label>
          <input
            id="rubrik-client-secret"
            type="password"
            className={INPUT_CLASS}
            value={secret}
            autoComplete="new-password"
            required={needsSecret}
            aria-required={needsSecret}
            aria-describedby="rubrik-secret-note"
            placeholder={
              needsSecret
                ? "enter the client secret"
                : "leave blank to keep the stored secret"
            }
            onChange={(e) => setSecret(e.currentTarget.value)}
          />
          <p id="rubrik-secret-note" className="mt-1.5 text-xs">
            {originMoved ? (
              <span className="text-warning">
                This is a different tenant. Enter its client secret: the stored
                secret is never sent to another tenant. Saving also clears every
                cluster mapping.
              </span>
            ) : connection.hasSecret ? (
              <span className="text-text-muted">Secret set.</span>
            ) : (
              <span className="text-text-muted">No secret is stored.</span>
            )}
          </p>
        </div>
        {error && (
          <div role="alert">
            <Alert variant="error">{error}</Alert>
          </div>
        )}
        <div>
          <Button
            type="button"
            variant="primary"
            size="sm"
            disabled={busy || tenantUrl.trim() === "" || clientId.trim() === ""}
            onClick={save}
          >
            Save connection
          </Button>
        </div>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Mappings

interface Lists {
  clusters: ClusterRecord[];
  mappings: RubrikMapping[];
  rsc: RscCluster[] | null;
  rscError: string | null;
}

const STATE_TEXT: Partial<Record<RubrikClusterState, string>> = {
  unavailable: "The mapping's state could not be read.",
  not_configured: "No Rubrik connection is configured.",
};

/** How the page names a cluster record. */
function clusterLabel(c: ClusterRecord): string {
  return `${c.displayName || c.name}${c.isLocal ? " (local)" : ""}`;
}

function Mappings({
  connection,
  onConflict,
  onSaved,
}: {
  connection: RubrikConnection;
  onConflict: () => void;
  onSaved: () => void;
}) {
  const [lists, setLists] = useState<Lists | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const gen = useRef(0);

  const load = useCallback(async () => {
    const g = ++gen.current;
    setLoadError(null);
    const [clusters, mappings, rsc] = await Promise.allSettled([
      apiGet<ClusterRecord[]>(CLUSTERS_URL),
      apiGet<RubrikMapping[]>(MAPPINGS_URL),
      apiGet<RscCluster[]>(RSC_CLUSTERS_URL),
    ]);
    if (gen.current !== g) return;
    if (clusters.status === "rejected" || mappings.status === "rejected") {
      const err =
        clusters.status === "rejected"
          ? clusters.reason
          : (mappings as PromiseRejectedResult).reason;
      setLoadError(
        errorMessage(err, "The cluster mappings could not be loaded."),
      );
      return;
    }
    setLists({
      clusters: clusters.value.data ?? [],
      mappings: mappings.value.data ?? [],
      rsc: rsc.status === "fulfilled" ? (rsc.value.data ?? []) : null,
      rscError:
        rsc.status === "rejected"
          ? errorMessage(rsc.reason, "RSC's clusters could not be listed.")
          : null,
    });
  }, []);

  useEffect(() => {
    void load();
    return () => {
      gen.current++;
    };
  }, [load]);

  // Names for the "mapped to" text, keyed by the id the Rubrik API uses.
  const labels = new Map(
    (lists?.clusters ?? []).map((c) => [mappingId(c), clusterLabel(c)]),
  );

  return (
    <section className={SECTION_CLASS} aria-labelledby="rubrik-mappings">
      <h2 id="rubrik-mappings" className={HEADING_CLASS}>
        Cluster mappings
      </h2>
      <p className="m-0 text-[13px] text-text-muted">
        Map each k8sCenter cluster to the RSC Kubernetes cluster that protects
        it. Nothing is matched by name.
      </p>
      {loadError ? (
        <div className="flex flex-col items-start gap-2">
          <Alert variant="error">{loadError}</Alert>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => void load()}
          >
            Retry
          </Button>
        </div>
      ) : !lists ? (
        <p className="m-0 text-sm text-text-muted">Loading mappings…</p>
      ) : (
        <>
          {lists.rscError && (
            <div className="flex flex-col items-start gap-2">
              <Alert variant="error">
                RSC's clusters could not be listed: {lists.rscError}
              </Alert>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => void load()}
              >
                Retry
              </Button>
            </div>
          )}
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {lists.clusters.map((c) => (
              <MappingRow
                key={mappingId(c)}
                cluster={c}
                mapping={lists.mappings.find(
                  (m) => m.clusterId === mappingId(c),
                )}
                rsc={lists.rsc}
                labels={labels}
                revision={connection.revision ?? 0}
                onConflict={onConflict}
                onChanged={() => {
                  onSaved();
                  void load();
                }}
              />
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

function MappingRow({
  cluster,
  mapping,
  rsc,
  labels,
  revision,
  onConflict,
  onChanged,
}: {
  cluster: ClusterRecord;
  mapping: RubrikMapping | undefined;
  rsc: RscCluster[] | null;
  /** Cluster names keyed by the id the Rubrik API uses. */
  labels: Map<string, string>;
  revision: number;
  onConflict: () => void;
  onChanged: () => void;
}) {
  const id = mappingId(cluster);
  const [choice, setChoice] = useState(mapping?.rscClusterId ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const label = clusterLabel(cluster);
  const selectId = `rubrik-map-${id}`;
  const invalid = mapping?.state === "mapping_invalid";
  // A mapping whose RSC cluster RSC no longer lists.
  const missing =
    !!mapping &&
    rsc !== null &&
    !rsc.some((r) => r.id.toLowerCase() === mapping.rscClusterId.toLowerCase());
  const unchanged =
    !!mapping &&
    !invalid &&
    choice.toLowerCase() === mapping.rscClusterId.toLowerCase();

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await apiPut(`/v1/rubrik/mappings/${encodeURIComponent(id)}`, {
        rscClusterId: choice,
        connectionRevision: revision,
      });
      showToast(`${label} mapped`, "success");
      onChanged();
    } catch (err) {
      if (err instanceof ApiError && err.reason === "connection_changed") {
        // The page shows it: the reload that follows remounts the mappings.
        onConflict();
        return;
      }
      setError(errorMessage(err, "The mapping could not be saved."));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!confirm(`Remove the Rubrik mapping for ${label}?`)) return;
    setBusy(true);
    setError(null);
    try {
      await apiDelete(`/v1/rubrik/mappings/${encodeURIComponent(id)}`);
      showToast(`Mapping for ${label} removed`, "success");
      onChanged();
    } catch (err) {
      setError(errorMessage(err, "The mapping could not be removed."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li
      data-testid={`rubrik-mapping-${id}`}
      className="flex flex-col gap-2 rounded-lg border border-border-subtle p-3"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-[13.5px] font-semibold text-text-primary">
          {label}
        </span>
        <span className="text-[12.5px] text-text-muted">
          {!mapping
            ? "Not mapped"
            : invalid
              ? `Was mapped to ${mapping.rscClusterName}`
              : `Mapped to ${mapping.rscClusterName}`}
        </span>
      </div>
      {invalid && (
        <Alert variant="warning">
          This mapping no longer matches the cluster's registration: the cluster
          was removed or registered again. Choose its RSC cluster and confirm
          the mapping again. Writes are refused until then.
        </Alert>
      )}
      {missing && !invalid && (
        <Alert variant="warning">
          RSC no longer lists {mapping.rscClusterName}. Choose the RSC cluster
          that protects this cluster now.
        </Alert>
      )}
      {mapping && STATE_TEXT[mapping.state] && (
        <p className="m-0 text-xs text-warning">{STATE_TEXT[mapping.state]}</p>
      )}
      {rsc !== null && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-[260px] flex-1">
            <label className={LABEL_CLASS} htmlFor={selectId}>
              RSC cluster for {label}
            </label>
            <select
              id={selectId}
              className={INPUT_CLASS}
              value={choice}
              onChange={(e) => setChoice(e.currentTarget.value)}
            >
              <option value="">Choose an RSC cluster</option>
              {rsc.map((r) => {
                const takenBy =
                  r.mappedTo && r.mappedTo !== id ? r.mappedTo : undefined;
                return (
                  <option key={r.id} value={r.id} disabled={!!takenBy}>
                    {r.name}
                    {r.family === "protectionSet" ? " (protection set)" : ""}
                    {takenBy
                      ? ` — mapped to ${labels.get(takenBy) ?? takenBy}`
                      : ""}
                  </option>
                );
              })}
            </select>
          </div>
          <Button
            type="button"
            variant="primary"
            size="sm"
            disabled={busy || choice === "" || unchanged}
            onClick={save}
          >
            {invalid ? "Confirm mapping" : "Save mapping"}
          </Button>
          {mapping && (
            <Button
              type="button"
              variant="danger"
              size="sm"
              disabled={busy}
              onClick={remove}
            >
              Remove
            </Button>
          )}
        </div>
      )}
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </li>
  );
}
