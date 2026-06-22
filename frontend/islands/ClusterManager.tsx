import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api, apiGet, apiPost } from "@/lib/api.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import type { Column, Row } from "@/components/ui/ResourceTable.tsx";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";

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
  const clusters = useSignal<ClusterInfo[]>([]);
  const loading = useSignal(true);
  const step = useSignal<WizardStep>("list");
  const error = useSignal("");
  const saving = useSignal(false);

  // Wizard form state
  const name = useSignal("");
  const displayName = useSignal("");
  const apiServerUrl = useSignal("");
  const token = useSignal("");
  const caCert = useSignal("");
  // F#2 — admin-only opt-in for unverified TLS. Bearer tokens travel over
  // the connection, so default-false. Backend re-validates admin role and
  // rejects registrations missing both CA and this opt-in.
  const allowInsecureTLS = useSignal(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadClusters();
  }, []);

  async function loadClusters() {
    loading.value = true;
    try {
      const res = await apiGet<ClusterInfo[]>("/v1/clusters");
      clusters.value = Array.isArray(res.data) ? res.data : [];
    } catch {
      // May not have database configured
      clusters.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function addCluster() {
    error.value = "";
    saving.value = true;
    try {
      await api("/v1/clusters", {
        method: "POST",
        body: JSON.stringify({
          name: name.value,
          displayName: displayName.value,
          apiServerUrl: apiServerUrl.value,
          token: token.value,
          caCert: caCert.value,
          allowInsecureTLS: allowInsecureTLS.value,
        }),
      });
      // Reset wizard and reload
      step.value = "list";
      name.value = "";
      displayName.value = "";
      apiServerUrl.value = "";
      token.value = "";
      caCert.value = "";
      allowInsecureTLS.value = false;
      await loadClusters();
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to register cluster";
    } finally {
      saving.value = false;
    }
  }

  async function deleteCluster(id: string) {
    if (!confirm("Remove this cluster?")) return;
    try {
      await api(`/v1/clusters/${id}`, { method: "DELETE" });
      await loadClusters();
    } catch {
      // ignore
    }
  }

  if (loading.value) {
    return (
      <div
        style={{ display: "flex", justifyContent: "center", padding: "48px 0" }}
      >
        <Spinner class="text-accent" />
      </div>
    );
  }

  // Add Cluster Wizard
  if (step.value === "connect") {
    return (
      <WidgetShell title="Add Cluster">
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          {error.value && <ErrorBanner message={error.value} />}

          <Field label="Cluster Name">
            <TextField
              value={name.value}
              onInput={(v) => {
                name.value = v;
              }}
              placeholder="production-us-east"
            />
          </Field>

          <Field label="Display Name" hint="Optional human-readable label">
            <TextField
              value={displayName.value}
              onInput={(v) => {
                displayName.value = v;
              }}
              placeholder="Production US East"
            />
          </Field>

          <Field label="API Server URL">
            <TextField
              value={apiServerUrl.value}
              onInput={(v) => {
                apiServerUrl.value = v;
              }}
              placeholder="https://k8s-api.example.com:6443"
            />
          </Field>

          <Field label="Service Account Token">
            <textarea
              value={token.value}
              onInput={(e) => {
                token.value = (e.target as HTMLTextAreaElement).value;
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
              value={caCert.value}
              onInput={(e) => {
                caCert.value = (e.target as HTMLTextAreaElement).value;
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
                checked={allowInsecureTLS.value}
                onChange={(v) => {
                  allowInsecureTLS.value = v;
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
              loading={saving.value}
              onClick={addCluster}
              disabled={!name.value || !apiServerUrl.value || !token.value ||
                (!caCert.value && !allowInsecureTLS.value)}
            >
              Register Cluster
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                step.value = "list";
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

  const rows: Row[] = clusters.value.map((c) => ({
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
                background: c.status === "connected"
                  ? "var(--success)"
                  : "var(--error)",
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
      actions: !c.isLocal
        ? (
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
                  clusters.value = clusters.value.map((cl) =>
                    cl.id === c.id ? { ...cl, ...res.data } : cl
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
              variant="danger"
              size="sm"
              onClick={() => deleteCluster(c.id)}
            >
              Remove
            </Button>
          </div>
        )
        : null,
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
          {clusters.value.length}{" "}
          cluster{clusters.value.length !== 1 ? "s" : ""} registered
        </span>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            step.value = "connect";
          }}
        >
          Add Cluster
        </Button>
      </div>

      {clusters.value.length === 0
        ? (
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
        )
        : (
          <ResourceTable
            columns={columns}
            rows={rows}
            chevron={false}
          />
        )}
    </div>
  );
}
