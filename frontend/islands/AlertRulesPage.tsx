import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";

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
              expr:
                'sum(rate(container_cpu_usage_seconds_total{container!=""}[5m])) by (pod, namespace) > 0.8',
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
  const rules = useSignal<RuleSummary[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const editing = useSignal<string | null>(null); //"new" or"{namespace}/{name}"
  const editorContent = useSignal("");
  const editorNamespace = useSignal("default");
  const saving = useSignal(false);

  function fetchRules() {
    loading.value = true;
    apiGet<RuleSummary[]>("/v1/alerts/rules")
      .then((res) => {
        rules.value = res.data ?? [];
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch alert rules";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchRules();
  }, []);

  function handleNew() {
    editing.value = "new";
    editorContent.value = DEFAULT_RULE_JSON;
  }

  function handleEdit(ns: string, name: string) {
    loading.value = true;
    apiGet<Record<string, unknown>>(`/v1/alerts/rules/${ns}/${name}`)
      .then((res) => {
        editorContent.value = JSON.stringify(res.data, null, 2);
        editing.value = `${ns}/${name}`;
        editorNamespace.value = ns;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch rule";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  function handleSave() {
    saving.value = true;
    error.value = null;

    let content: Record<string, unknown>;
    try {
      content = JSON.parse(editorContent.value);
    } catch {
      error.value = "Invalid JSON content";
      saving.value = false;
      return;
    }

    const isNew = editing.value === "new";
    const promise = isNew
      ? apiPost("/v1/alerts/rules", {
        namespace: editorNamespace.value,
        content,
      })
      : apiPut(
        `/v1/alerts/rules/${editing.value}`,
        content,
      );

    promise
      .then(() => {
        editing.value = null;
        fetchRules();
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to save rule";
      })
      .finally(() => {
        saving.value = false;
      });
  }

  function handleDelete(ns: string, name: string) {
    if (!confirm(`Delete PrometheusRule ${ns}/${name}?`)) return;

    apiDelete(`/v1/alerts/rules/${ns}/${name}`)
      .then(() => fetchRules())
      .catch((err) => {
        error.value = err.message ?? "Failed to delete rule";
      });
  }

  if (editing.value) {
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
              {editing.value === "new"
                ? "Create Alert Rule"
                : `Edit ${editing.value}`}
            </h2>
            <Button variant="ghost" onClick={() => editing.value = null}>
              Cancel
            </Button>
          </div>

          {editing.value === "new" && (
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
                value={editorNamespace.value}
                onInput={(e) =>
                  editorNamespace.value = (e.target as HTMLInputElement).value}
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

          {error.value && <ErrorBanner message={error.value} />}

          {/* Solid surface for the code editor */}
          <textarea
            value={editorContent.value}
            onInput={(e) =>
              editorContent.value = (e.target as HTMLTextAreaElement).value}
            spellcheck={false}
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
            <Button variant="secondary" onClick={() => editing.value = null}>
              Cancel
            </Button>
            <Button onClick={handleSave} disabled={saving.value}>
              {saving.value ? "Saving..." : "Save"}
            </Button>
          </div>
        </div>
      </GlassCard>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <Button onClick={handleNew}>Create Rule</Button>
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      {/* Solid data surface wrapping the list */}
      <GlassCard padding={0}>
        {loading.value
          ? (
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
          )
          : rules.value.length === 0
          ? (
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
          )
          : (
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
                    {["Name", "Namespace", "Rules", "Created", ""].map(
                      (h, i) => (
                        <th
                          key={h + i}
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
                      ),
                    )}
                  </tr>
                </thead>
                <tbody>
                  {rules.value.map((rule) => (
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
                          onClick={() =>
                            handleDelete(rule.namespace, rule.name)}
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
