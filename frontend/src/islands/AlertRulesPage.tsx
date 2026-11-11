import { useCallback, useEffect, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface RuleSummary {
  name: string;
  namespace: string;
  rulesCount: number;
  createdAt: string;
  managedBy: string;
}

const DEFAULT_RULE_JSON = JSON.stringify(
  {
    apiVersion: "monitoring.coreos.com/v1",
    kind: "PrometheusRule",
    metadata: {
      name: "kubecenter-example",
    },
    spec: {
      groups: [
        {
          name: "example.rules",
          rules: [
            {
              alert: "HighPodCPU",
              expr: 'sum(rate(container_cpu_usage_seconds_total{container!=""}[5m])) by (pod, namespace) > 0.8',
              for: "5m",
              labels: {
                severity: "warning",
              },
              annotations: {
                summary: "High CPU usage on {{ $labels.pod }}",
                description:
                  "Pod {{ $labels.pod }} in {{ $labels.namespace }} is using > 80% CPU for 5 minutes.",
              },
            },
          ],
        },
      ],
    },
  },
  null,
  2,
);

export default function AlertRulesPage() {
  const [rules, setRules] = useState<RuleSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null); // "new" or "{namespace}/{name}"
  const [editorContent, setEditorContent] = useState("");
  const [editorNamespace, setEditorNamespace] = useState("default");
  const [saving, setSaving] = useState(false);
  const selected = useSignalValue(selectedCluster);
  // The selection is restored from localStorage, which the server cannot
  // read, so the remote-cluster notice waits for hydration.
  const isClient = useIsClient();

  const fetchRules = useCallback(() => {
    setLoading(true);
    apiGet<RuleSummary[]>("/v1/alerts/rules")
      .then((res) => {
        setRules(res.data ?? []);
        setError(null);
      })
      .catch((err) => {
        setError(err.message ?? "Failed to fetch alert rules");
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchRules();
  }, [fetchRules]);

  function handleNew() {
    setEditing("new");
    setEditorContent(DEFAULT_RULE_JSON);
  }

  function handleEdit(ns: string, name: string) {
    setLoading(true);
    apiGet<Record<string, unknown>>(`/v1/alerts/rules/${ns}/${name}`)
      .then((res) => {
        setEditorContent(JSON.stringify(res.data, null, 2));
        setEditing(`${ns}/${name}`);
        setEditorNamespace(ns);
      })
      .catch((err) => {
        setError(err.message ?? "Failed to fetch rule");
      })
      .finally(() => {
        setLoading(false);
      });
  }

  function handleSave() {
    setSaving(true);
    setError(null);

    let content: Record<string, unknown>;
    try {
      content = JSON.parse(editorContent);
    } catch {
      setError("Invalid JSON content");
      setSaving(false);
      return;
    }

    const isNew = editing === "new";
    const promise = isNew
      ? apiPost("/v1/alerts/rules", {
          namespace: editorNamespace,
          content,
        })
      : apiPut(`/v1/alerts/rules/${editing}`, content);

    promise
      .then(() => {
        setEditing(null);
        fetchRules();
      })
      .catch((err) => {
        setError(err.message ?? "Failed to save rule");
      })
      .finally(() => {
        setSaving(false);
      });
  }

  function handleDelete(ns: string, name: string) {
    if (!confirm(`Delete PrometheusRule ${ns}/${name}?`)) return;

    apiDelete(`/v1/alerts/rules/${ns}/${name}`)
      .then(() => fetchRules())
      .catch((err) => {
        setError(err.message ?? "Failed to delete rule");
      });
  }

  if (editing) {
    return (
      <GlassCard padding={24}>
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          {/* Editor header */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <h2
              style={{
                margin: 0,
                fontSize: "17px",
                fontWeight: 650,
                color: "var(--text-primary)",
              }}
            >
              {editing === "new" ? "Create Alert Rule" : `Edit ${editing}`}
            </h2>
            <Button variant="ghost" onClick={() => setEditing(null)}>
              Cancel
            </Button>
          </div>

          {editing === "new" && (
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.05em",
                  color: "var(--text-muted)",
                  marginBottom: "6px",
                }}
              >
                Namespace
              </label>
              <input
                type="text"
                value={editorNamespace}
                onChange={(e) => setEditorNamespace(e.currentTarget.value)}
                style={{
                  width: "256px",
                  padding: "7px 10px",
                  fontSize: "13px",
                  borderRadius: "9px",
                  border: "1px solid var(--border-subtle)",
                  background: "var(--bg-elevated)",
                  color: "var(--text-primary)",
                  outline: "none",
                }}
              />
            </div>
          )}

          {error && <ErrorBanner message={error} />}

          {/* Solid surface for the code editor */}
          <textarea
            value={editorContent}
            onChange={(e) => setEditorContent(e.currentTarget.value)}
            spellCheck={false}
            style={{
              width: "100%",
              height: "384px",
              fontFamily: "monospace",
              fontSize: "13px",
              padding: "12px 16px",
              borderRadius: "9px",
              border: "1px solid var(--border-subtle)",
              background: "var(--bg-surface)",
              color: "var(--text-primary)",
              resize: "vertical",
              outline: "none",
              boxSizing: "border-box",
            }}
          />

          <div
            style={{
              display: "flex",
              justifyContent: "flex-end",
              gap: "8px",
            }}
          >
            <Button variant="secondary" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving ? "Saving..." : "Save"}
            </Button>
          </div>
        </div>
      </GlassCard>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* The rules listed here are the remote cluster's PrometheusRules, but
          the firing and history feeds always read the local Alertmanager
          (R-8 KTD13). Say so, or a quiet local feed reads as remote rules
          that are not firing. */}
      {isClient && selected !== LOCAL_CLUSTER_ID && (
        <Alert variant="info">
          These are the selected remote cluster's alert rules. Whether they fire
          depends on that cluster running prometheus-operator; the firing alerts
          on the Alerts page come from the local cluster's Alertmanager.
        </Alert>
      )}
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <Button onClick={handleNew}>Create Rule</Button>
      </div>

      {error && <ErrorBanner message={error} />}

      {/* Solid data surface wrapping the list */}
      <GlassCard padding={0}>
        {loading ? (
          <div
            style={{
              textAlign: "center",
              padding: "48px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Loading...
          </div>
        ) : rules.length === 0 ? (
          <div
            style={{
              textAlign: "center",
              padding: "48px 20px",
              color: "var(--text-muted)",
            }}
          >
            <p
              style={{
                fontSize: "15px",
                fontWeight: 600,
                color: "var(--text-primary)",
                margin: "0 0 6px",
              }}
            >
              No alert rules
            </p>
            <p style={{ fontSize: "13px", margin: 0 }}>
              Create PrometheusRule resources to define alerting conditions.
              Requires Prometheus Operator.
            </p>
          </div>
        ) : (
          <div style={{ overflowX: "auto" }}>
            <table
              style={{
                width: "100%",
                borderCollapse: "collapse",
                fontSize: "13px",
              }}
            >
              <thead>
                <tr
                  style={{
                    borderBottom: "1px solid var(--border-subtle)",
                    background: "var(--bg-surface)",
                  }}
                >
                  {["Name", "Namespace", "Rules", "Created", ""].map((h, i) => (
                    <th
                      key={h || "actions"}
                      style={{
                        padding: "10px 16px",
                        textAlign: i === 4 ? "right" : "left",
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rules.map((rule) => (
                  <tr
                    key={`${rule.namespace}/${rule.name}`}
                    style={{ borderBottom: "1px solid var(--border-subtle)" }}
                  >
                    <td
                      style={{
                        padding: "12px 16px",
                        fontWeight: 500,
                        color: "var(--text-primary)",
                      }}
                    >
                      {rule.name}
                    </td>
                    <td
                      style={{
                        padding: "12px 16px",
                        color: "var(--text-muted)",
                      }}
                    >
                      {rule.namespace}
                    </td>
                    <td
                      style={{
                        padding: "12px 16px",
                        color: "var(--text-muted)",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {rule.rulesCount}
                    </td>
                    <td
                      style={{
                        padding: "12px 16px",
                        color: "var(--text-muted)",
                      }}
                    >
                      {new Date(rule.createdAt).toLocaleDateString()}
                    </td>
                    <td
                      style={{
                        padding: "12px 16px",
                        textAlign: "right",
                        whiteSpace: "nowrap",
                      }}
                    >
                      <button
                        type="button"
                        onClick={() => handleEdit(rule.namespace, rule.name)}
                        style={{
                          marginRight: "12px",
                          fontSize: "13px",
                          color: "var(--accent)",
                          background: "none",
                          border: "none",
                          cursor: "pointer",
                          padding: 0,
                        }}
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDelete(rule.namespace, rule.name)}
                        style={{
                          fontSize: "13px",
                          color: "var(--error)",
                          background: "none",
                          border: "none",
                          cursor: "pointer",
                          padding: 0,
                        }}
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </GlassCard>
    </div>
  );
}
