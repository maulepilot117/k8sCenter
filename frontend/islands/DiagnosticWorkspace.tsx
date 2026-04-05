import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import DiagnosticChecklist from "@/islands/DiagnosticChecklist.tsx";
import type { DiagnosticResult } from "@/islands/DiagnosticChecklist.tsx";
import BlastRadiusPanel from "@/islands/BlastRadiusPanel.tsx";
import type { AffectedResource } from "@/islands/BlastRadiusPanel.tsx";

interface DiagnosticResponse {
  target: { kind: string; name: string; namespace: string };
  results: DiagnosticResult[];
  blastRadius: {
    directlyAffected: AffectedResource[];
    potentiallyAffected: AffectedResource[];
  };
}

const KIND_OPTIONS = [
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "Pod",
  "Service",
  "Job",
  "CronJob",
  "PersistentVolumeClaim",
];

export default function DiagnosticWorkspace() {
  const namespace = useSignal("");
  const kind = useSignal("");
  const name = useSignal("");

  const loading = useSignal(false);
  const error = useSignal<string | null>(null);

  const results = useSignal<DiagnosticResult[]>([]);
  const directlyAffected = useSignal<AffectedResource[]>([]);
  const potentiallyAffected = useSignal<AffectedResource[]>([]);
  const hasData = useSignal(false);

  const fetchDiagnostics = async (ns: string, k: string, n: string) => {
    loading.value = true;
    error.value = null;
    try {
      const resp = await apiGet<DiagnosticResponse>(
        `/v1/diagnostics/${ns}/${k}/${n}`,
      );
      const data = resp.data;
      results.value = data.results;
      directlyAffected.value = data.blastRadius.directlyAffected;
      potentiallyAffected.value = data.blastRadius.potentiallyAffected;
      hasData.value = true;
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to run diagnostics";
      hasData.value = false;
    } finally {
      loading.value = false;
    }
  };

  // Parse URL params on mount and auto-run if all present
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    const ns = params.get("namespace") ?? "";
    const k = params.get("kind") ?? "";
    const n = params.get("name") ?? "";

    namespace.value = ns;
    kind.value = k;
    name.value = n;

    if (ns && k && n) {
      fetchDiagnostics(ns, k, n);
    }
  }, []);

  const handleInvestigate = () => {
    if (!namespace.value || !kind.value || !name.value) return;
    // Update URL without reload
    const params = new URLSearchParams({
      namespace: namespace.value,
      kind: kind.value,
      name: name.value,
    });
    globalThis.history.replaceState(null, "", `?${params.toString()}`);
    fetchDiagnostics(namespace.value, kind.value, name.value);
  };

  const criticalCount = results.value.filter(
    (r) => r.status === "fail" && r.severity === "critical",
  ).length;
  const warningCount = results.value.filter(
    (r) =>
      r.status === "fail" && r.severity === "warning" ||
      r.status === "warn",
  ).length;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Resource Picker */}
      {!hasData.value && !loading.value && (
        <div
          style={{
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            background: "var(--bg-surface)",
            padding: "24px",
          }}
        >
          <h3
            style={{
              fontSize: "14px",
              fontWeight: 600,
              color: "var(--text-primary)",
              marginBottom: "16px",
            }}
          >
            Select a resource to investigate
          </h3>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr 1fr auto",
              gap: "12px",
              alignItems: "end",
            }}
          >
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-secondary)",
                  marginBottom: "4px",
                }}
              >
                Namespace
              </label>
              <input
                type="text"
                placeholder="default"
                value={namespace.value}
                onInput={(e) =>
                  namespace.value = (e.target as HTMLInputElement).value}
                style={{
                  width: "100%",
                  padding: "7px 10px",
                  fontSize: "13px",
                  borderRadius: "var(--radius-sm)",
                  border: "1px solid var(--border-primary)",
                  background: "var(--bg-base)",
                  color: "var(--text-primary)",
                }}
              />
            </div>
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-secondary)",
                  marginBottom: "4px",
                }}
              >
                Kind
              </label>
              <select
                value={kind.value}
                onChange={(e) =>
                  kind.value = (e.target as HTMLSelectElement).value}
                style={{
                  width: "100%",
                  padding: "7px 10px",
                  fontSize: "13px",
                  borderRadius: "var(--radius-sm)",
                  border: "1px solid var(--border-primary)",
                  background: "var(--bg-base)",
                  color: "var(--text-primary)",
                }}
              >
                <option value="">Select kind...</option>
                {KIND_OPTIONS.map((k) => (
                  <option key={k} value={k}>{k}</option>
                ))}
              </select>
            </div>
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-secondary)",
                  marginBottom: "4px",
                }}
              >
                Name
              </label>
              <input
                type="text"
                placeholder="my-deployment"
                value={name.value}
                onInput={(e) =>
                  name.value = (e.target as HTMLInputElement).value}
                style={{
                  width: "100%",
                  padding: "7px 10px",
                  fontSize: "13px",
                  borderRadius: "var(--radius-sm)",
                  border: "1px solid var(--border-primary)",
                  background: "var(--bg-base)",
                  color: "var(--text-primary)",
                }}
              />
            </div>
            <button
              type="button"
              onClick={handleInvestigate}
              disabled={!namespace.value || !kind.value || !name.value}
              style={{
                padding: "7px 16px",
                fontSize: "13px",
                fontWeight: 600,
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--accent)",
                background: "var(--accent)",
                color: "var(--bg-base)",
                cursor: "pointer",
                opacity: !namespace.value || !kind.value || !name.value
                  ? 0.5
                  : 1,
              }}
            >
              Investigate
            </button>
          </div>
        </div>
      )}

      {/* Loading */}
      {loading.value && (
        <div
          style={{
            textAlign: "center",
            padding: "48px",
            color: "var(--text-muted)",
            fontSize: "14px",
          }}
        >
          Running diagnostics...
        </div>
      )}

      {/* Error */}
      {error.value && (
        <div
          style={{
            padding: "12px 16px",
            borderRadius: "var(--radius)",
            border: "1px solid var(--error-dim)",
            background: "var(--error-dim)",
            color: "var(--error)",
            fontSize: "13px",
          }}
        >
          {error.value}
        </div>
      )}

      {/* Results */}
      {hasData.value && !loading.value && (
        <>
          {/* Status Banner */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "12px 16px",
              borderRadius: "var(--radius)",
              border: `1px solid ${
                criticalCount > 0
                  ? "var(--error-dim)"
                  : warningCount > 0
                  ? "var(--warning-dim)"
                  : "var(--success-dim)"
              }`,
              background: criticalCount > 0
                ? "var(--error-dim)"
                : warningCount > 0
                ? "var(--warning-dim)"
                : "var(--success-dim)",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
              <span
                style={{
                  fontWeight: 600,
                  fontSize: "14px",
                  color: criticalCount > 0
                    ? "var(--error)"
                    : warningCount > 0
                    ? "var(--warning)"
                    : "var(--success)",
                }}
              >
                {criticalCount > 0
                  ? `${criticalCount} critical issue${
                    criticalCount > 1 ? "s" : ""
                  }`
                  : warningCount > 0
                  ? `${warningCount} warning${warningCount > 1 ? "s" : ""}`
                  : "All checks passed"}
              </span>
              <span
                style={{ fontSize: "13px", color: "var(--text-secondary)" }}
              >
                {kind.value}/{name.value} in {namespace.value}
              </span>
            </div>
            <button
              type="button"
              onClick={() =>
                fetchDiagnostics(
                  namespace.value,
                  kind.value,
                  name.value,
                )}
              style={{
                padding: "5px 12px",
                fontSize: "12px",
                fontWeight: 500,
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--border-primary)",
                background: "var(--bg-surface)",
                color: "var(--text-secondary)",
                cursor: "pointer",
              }}
            >
              Re-scan
            </button>
          </div>

          {/* Two-column layout */}
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "3fr 2fr",
              gap: "16px",
              alignItems: "start",
            }}
          >
            <DiagnosticChecklist
              results={results}
              namespace={namespace.value}
            />
            <BlastRadiusPanel
              directlyAffected={directlyAffected}
              potentiallyAffected={potentiallyAffected}
              namespace={namespace.value}
            />
          </div>
        </>
      )}
    </div>
  );
}
