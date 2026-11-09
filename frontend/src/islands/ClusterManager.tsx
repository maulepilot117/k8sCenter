/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import type { Column, Row } from "@/components/ui/ResourceTable.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { api, apiGet, apiPost } from "@/lib/api.ts";
import ComplianceIdentityForm, {
  type ComplianceIdentityState,
} from "@/src/islands/ComplianceIdentityForm.tsx";
import LokiBindingForm, {
  type LokiState,
} from "@/src/islands/LokiBindingForm.tsx";
import MetricsBindingForm, {
  type MetricsState,
} from "@/src/islands/MetricsBindingForm.tsx";
import {
  LOCAL_CLUSTER_ID,
  LOCAL_GENERATION,
  selectedCluster,
  switchCluster,
} from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";

interface ClusterInfo {
  id: string;
  name: string;
  displayName: string;
  apiServerUrl: string;
  status: string;
  statusMessage: string;
  k8sVersion: string;
  nodeCount: number;
  isLocal: boolean;
  // F#10 — surface the operator-opted-in insecure TLS state in the cluster
  // list so admins can spot homelab-only registrations at a glance and so
  // any unexpected `true` (e.g. after a CA-less rolling-upgrade backfill)
  // gets noticed before it's used to send bearer tokens over an unverified
  // connection. Optional because older backend versions omit the field.
  allowInsecureTLS?: boolean;
  lastProbedAt?: string;
}

type WizardStep = "list" | "connect";

/**
 * Cluster management island — list clusters + add cluster wizard.
 */
export default function ClusterManager() {
  const [clusters, setClusters] = useState<ClusterInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [step, setStep] = useState<WizardStep>("list");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  // Wizard form state
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [apiServerUrl, setApiServerUrl] = useState("");
  const [token, setToken] = useState("");
  const [caCert, setCaCert] = useState("");
  // F#2 — admin-only opt-in for unverified TLS. Bearer tokens travel over
  // the connection, so default-false. Backend re-validates admin role and
  // rejects registrations missing both CA and this opt-in.
  const [allowInsecureTLS, setAllowInsecureTLS] = useState(false);

  // Per-cluster metrics binding form (one open at a time); the form owns its
  // fields, this island keeps which cluster is open and each cluster's badge.
  const [metricsFor, setMetricsFor] = useState<string | null>(null);
  const [metricsState, setMetricsStates] = useState<
    Record<string, MetricsState>
  >({});
  // Same shape for the optional Loki binding form.
  const [logsFor, setLogsFor] = useState<string | null>(null);
  const [logsState, setLogsStates] = useState<Record<string, LokiState>>({});
  // Same shape for the compliance-history identity form.
  const [identityFor, setIdentityFor] = useState<string | null>(null);
  const [identityState, setIdentityStates] = useState<
    Record<string, ComplianceIdentityState>
  >({});

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `loadClusters` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    loadClusters();
  }, []);

  // The binding forms report after their own requests finish, so these merge
  // into the latest map rather than the one from the render that opened them.
  function setMetricsState(id: string, state: MetricsState) {
    setMetricsStates((prev) => ({ ...prev, [id]: state }));
  }

  function setLogsState(id: string, state: LokiState) {
    setLogsStates((prev) => ({ ...prev, [id]: state }));
  }

  function setIdentityState(id: string, state: ComplianceIdentityState) {
    setIdentityStates((prev) => ({ ...prev, [id]: state }));
  }

  function toggleIdentity(id: string) {
    setIdentityFor((cur) => (cur === id ? null : id));
  }

  function toggleMetrics(id: string) {
    setMetricsFor((cur) => (cur === id ? null : id));
  }

  function toggleLogs(id: string) {
    setLogsFor((cur) => (cur === id ? null : id));
  }

  async function loadClusters() {
    setLoading(true);
    try {
      const res = await apiGet<ClusterInfo[]>("/v1/clusters");
      setClusters(Array.isArray(res.data) ? res.data : []);
    } catch {
      // May not have database configured
      setClusters([]);
    } finally {
      setLoading(false);
    }
  }

  async function addCluster() {
    setError("");
    setSaving(true);
    try {
      await api("/v1/clusters", {
        method: "POST",
        body: JSON.stringify({
          name,
          displayName,
          apiServerUrl,
          token,
          caCert,
          allowInsecureTLS,
        }),
      });
      // Reset wizard and reload
      setStep("list");
      setName("");
      setDisplayName("");
      setApiServerUrl("");
      setToken("");
      setCaCert("");
      setAllowInsecureTLS(false);
      await loadClusters();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to register cluster",
      );
    } finally {
      setSaving(false);
    }
  }

  async function deleteCluster(id: string) {
    if (!confirm("Remove this cluster?")) return;
    try {
      await api(`/v1/clusters/${id}`, { method: "DELETE" });
      // Deleting the cluster you are currently on must not leave the whole
      // app addressing a cluster the registry no longer knows. Handling it
      // here, at the source, is why the switcher's mount-time reconcile does
      // not need to silently retarget an unknown selection.
      if (selectedCluster.peek() === id) {
        switchCluster(LOCAL_CLUSTER_ID, LOCAL_GENERATION);
      }
      await loadClusters();
    } catch {
      // ignore
    }
  }

  if (loading) {
    return (
      <div
        style={{ display: "flex", justifyContent: "center", padding: "48px 0" }}
      >
        <Spinner className="text-accent" />
      </div>
    );
  }

  // Add Cluster Wizard
  if (step === "connect") {
    return (
      <WidgetShell title="Add Cluster">
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          {error && <ErrorBanner message={error} />}

          <Field label="Cluster Name">
            <TextField
              value={name}
              onInput={(v) => {
                setName(v);
              }}
              placeholder="production-us-east"
            />
          </Field>

          <Field label="Display Name" hint="Optional human-readable label">
            <TextField
              value={displayName}
              onInput={(v) => {
                setDisplayName(v);
              }}
              placeholder="Production US East"
            />
          </Field>

          <Field label="API Server URL">
            <TextField
              value={apiServerUrl}
              onInput={(v) => {
                setApiServerUrl(v);
              }}
              placeholder="https://k8s-api.example.com:6443"
            />
          </Field>

          <Field label="Service Account Token">
            <textarea
              value={token}
              onChange={(e) => {
                setToken(e.currentTarget.value);
              }}
              rows={3}
              placeholder="eyJhbGciOiJSUzI1NiIs..."
              style={{
                width: "100%",
                padding: "10px 12px",
                borderRadius: "9px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
                color: "var(--text-primary)",
                fontSize: "13.5px",
                fontFamily: "var(--font-mono)",
                outline: "none",
                resize: "vertical",
                boxSizing: "border-box",
              }}
            />
          </Field>

          <Field
            label="CA Certificate"
            hint="Optional when insecure TLS opt-in is enabled below"
          >
            <textarea
              value={caCert}
              onChange={(e) => {
                setCaCert(e.currentTarget.value);
              }}
              rows={3}
              placeholder="-----BEGIN CERTIFICATE-----"
              style={{
                width: "100%",
                padding: "10px 12px",
                borderRadius: "9px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
                color: "var(--text-primary)",
                fontSize: "13.5px",
                fontFamily: "var(--font-mono)",
                outline: "none",
                resize: "vertical",
                boxSizing: "border-box",
              }}
            />
          </Field>

          {/* F#2 — admin-only opt-in for unverified TLS. Default-off. */}
          <div
            style={{
              borderRadius: "9px",
              border: "1px solid var(--warning)",
              background: "var(--warning-dim)",
              padding: "12px",
            }}
          >
            <label
              style={{
                display: "flex",
                alignItems: "flex-start",
                gap: "10px",
                cursor: "pointer",
              }}
            >
              <Toggle
                checked={allowInsecureTLS}
                onChange={(v) => {
                  setAllowInsecureTLS(v);
                }}
              />
              <span>
                <span
                  style={{
                    display: "block",
                    fontSize: "13px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  Allow insecure TLS (self-signed CA, homelab only)
                </span>
                <span
                  style={{
                    display: "block",
                    marginTop: "4px",
                    fontSize: "12px",
                    color: "var(--warning)",
                  }}
                >
                  Bearer tokens will be sent over an unverified TLS connection.
                  Only enable for trusted homelab self-signed certs. Admin role
                  required.
                </span>
              </span>
            </label>
          </div>

          <div style={{ display: "flex", gap: "12px", paddingTop: "4px" }}>
            <Button
              type="button"
              variant="primary"
              loading={saving}
              onClick={addCluster}
              disabled={
                !name ||
                !apiServerUrl ||
                !token ||
                (!caCert && !allowInsecureTLS)
              }
            >
              Register Cluster
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setStep("list");
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      </WidgetShell>
    );
  }

  // Cluster List
  const columns: Column[] = [
    { key: "name", label: "Cluster", width: "2fr" },
    { key: "version", label: "Version", width: "120px" },
    { key: "nodes", label: "Nodes", width: "80px", align: "right" },
    { key: "status", label: "Status", width: "140px" },
    { key: "actions", label: "", width: "160px", align: "right" },
  ];

  const rows: Row[] = clusters.map((c) => ({
    id: c.id,
    cells: {
      name: (
        <div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "6px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-primary)",
            }}
          >
            <span
              style={{
                width: "8px",
                height: "8px",
                borderRadius: "50%",
                flexShrink: 0,
                background:
                  c.status === "connected" ? "var(--success)" : "var(--error)",
              }}
            />
            {c.displayName || c.name}
            {c.isLocal && (
              <span
                style={{
                  borderRadius: "6px",
                  padding: "1px 6px",
                  fontSize: "11px",
                  background: "var(--accent-dim)",
                  color: "var(--accent)",
                }}
              >
                local
              </span>
            )}
            {c.allowInsecureTLS && !c.isLocal && (
              <span
                style={{
                  borderRadius: "6px",
                  padding: "1px 6px",
                  fontSize: "11px",
                  background: "var(--error-dim)",
                  color: "var(--error)",
                }}
                title="TLS verification disabled — bearer tokens are sent over an unverified connection"
              >
                ⚠ Insecure TLS
              </span>
            )}
          </div>
          <div
            style={{
              marginTop: "2px",
              fontSize: "12px",
              color: "var(--text-muted)",
            }}
          >
            {c.apiServerUrl || c.id}
            {c.lastProbedAt && (
              <span>
                {` · checked ${new Date(c.lastProbedAt).toLocaleTimeString()}`}
              </span>
            )}
          </div>
          {!c.isLocal && metricsState[c.id] && (
            <div style={{ marginTop: "4px" }}>
              <StatusBadge
                label={
                  metricsState[c.id] === "configured"
                    ? "metrics: configured"
                    : "metrics: not configured"
                }
                tone={metricsState[c.id] === "configured" ? "ok" : "warn"}
              />
            </div>
          )}
          {!c.isLocal && logsState[c.id] && (
            <div style={{ marginTop: "4px" }}>
              <StatusBadge
                label={
                  logsState[c.id] === "configured"
                    ? "logs: configured Loki URL"
                    : "logs: in-cluster Loki"
                }
                tone={logsState[c.id] === "configured" ? "ok" : "neutral"}
              />
            </div>
          )}
          {!c.isLocal && identityState[c.id] && (
            <div style={{ marginTop: "4px" }}>
              <StatusBadge
                label={
                  identityState[c.id] === "configured"
                    ? "compliance history: configured"
                    : "compliance history: not configured"
                }
                tone={identityState[c.id] === "configured" ? "ok" : "warn"}
              />
            </div>
          )}
          {!c.isLocal && identityFor === c.id && (
            <ComplianceIdentityForm
              key={c.id}
              clusterId={c.id}
              onIdentity={setIdentityState}
            />
          )}
          {!c.isLocal && metricsFor === c.id && (
            <MetricsBindingForm
              key={c.id}
              clusterId={c.id}
              onBinding={setMetricsState}
            />
          )}
          {!c.isLocal && logsFor === c.id && (
            <LokiBindingForm
              key={c.id}
              clusterId={c.id}
              onBinding={setLogsState}
            />
          )}
          {c.status !== "connected" && c.statusMessage && (
            <div
              style={{
                marginTop: "2px",
                fontSize: "12px",
                color: "var(--error)",
              }}
            >
              {c.statusMessage}
            </div>
          )}
        </div>
      ),
      version: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {c.k8sVersion || "—"}
        </span>
      ),
      nodes: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-primary)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {c.nodeCount}
        </span>
      ),
      status: (
        <StatusBadge
          label={c.status}
          tone={c.status === "connected" ? "ok" : "crit"}
        />
      ),
      actions: !c.isLocal ? (
        <div
          style={{
            display: "flex",
            justifyContent: "flex-end",
            gap: "6px",
          }}
        >
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={async () => {
              try {
                const res = await apiPost<ClusterInfo>(
                  `/v1/clusters/${c.id}/test`,
                );
                setClusters((prev) =>
                  prev.map((cl) =>
                    cl.id === c.id ? { ...cl, ...res.data } : cl,
                  ),
                );
                showToast(
                  res.data.status === "connected"
                    ? "Connection successful"
                    : "Connection failed",
                  res.data.status === "connected" ? "success" : "error",
                );
              } catch {
                showToast("Test failed", "error");
              }
            }}
          >
            Test
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => toggleMetrics(c.id)}
          >
            Metrics
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => toggleLogs(c.id)}
          >
            Logs
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => toggleIdentity(c.id)}
          >
            Compliance identity
          </Button>
          <Button
            type="button"
            variant="danger"
            size="sm"
            onClick={() => deleteCluster(c.id)}
          >
            Remove
          </Button>
        </div>
      ) : null,
    },
  }));

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
          {clusters.length} cluster
          {clusters.length !== 1 ? "s" : ""} registered
        </span>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setStep("connect");
          }}
        >
          Add Cluster
        </Button>
      </div>

      {clusters.length === 0 ? (
        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "16px",
            padding: "48px",
            textAlign: "center",
            fontSize: "13px",
            color: "var(--text-muted)",
          }}
        >
          No clusters registered. Add a cluster to get started.
        </div>
      ) : (
        <ResourceTable columns={columns} rows={rows} chevron={false} />
      )}
    </div>
  );
}
