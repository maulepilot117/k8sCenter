import type * as preact from "preact";
import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import YamlEditor from "@/islands/YamlEditor.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { type ApplyResponse, useYamlApply } from "@/lib/yaml-apply.ts";

const PLACEHOLDER_YAML = `# Paste or type your Kubernetes YAML here.
# Multi-document YAML (separated by ---) is supported.
#
# Example:
# apiVersion: v1
# kind: ConfigMap
# metadata:
# name: my-config
# namespace: default
# data:
# key: value
`;

export default function YamlApplyPage() {
  const forceConflicts = useSignal(false);
  const {
    yamlContent,
    applying,
    validating,
    error,
    result: results,
    handleValidate,
    handleApply,
  } = useYamlApply(PLACEHOLDER_YAML, { forceConflicts });

  // Set document title
  useEffect(() => {
    if (!IS_BROWSER) return;
    document.title = "YAML Apply - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  const handleFileUpload = useCallback(() => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".yaml,.yml,.json";
    input.onchange = async () => {
      const file = input.files?.[0];
      if (!file) return;
      // 2 MB limit matches backend MaxBodySize
      const MAX_FILE_SIZE = 2 * 1024 * 1024;
      if (file.size > MAX_FILE_SIZE) {
        error.value = `File is too large (${
          (file.size / 1024 / 1024).toFixed(1)
        } MB). Maximum size is 2 MB.`;
        return;
      }
      const text = await file.text();
      yamlContent.value = text;
      results.value = null;
      error.value = null;
    };
    input.click();
  }, []);

  const isWorking = applying.value || validating.value;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "20px" }}>
      {/* Page header */}
      <div>
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          YAML Apply
        </h1>
        <p
          style={{
            marginTop: "4px",
            fontSize: "13px",
            color: "var(--text-muted)",
          }}
        >
          Apply Kubernetes resources from YAML. Supports multi-document YAML
          with server-side apply.
        </p>
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      {/* Toolbar — glass chrome */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "12px",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
          <button
            type="button"
            onClick={handleFileUpload}
            disabled={isWorking}
            style={ghostButtonStyle(isWorking)}
          >
            Upload File
          </button>
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: "6px",
              fontSize: "13px",
              color: "var(--text-muted)",
              cursor: "pointer",
            }}
          >
            <input
              type="checkbox"
              checked={forceConflicts.value}
              onChange={(e) => {
                forceConflicts.value = (e.target as HTMLInputElement).checked;
              }}
              style={{ accentColor: "var(--accent)" }}
            />
            Force conflicts
          </label>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <button
            type="button"
            onClick={handleValidate}
            disabled={isWorking || yamlContent.value === PLACEHOLDER_YAML}
            style={ghostButtonStyle(
              isWorking || yamlContent.value === PLACEHOLDER_YAML,
            )}
          >
            {validating.value ? "Validating…" : "Validate"}
          </button>
          <button
            type="button"
            onClick={handleApply}
            disabled={isWorking || yamlContent.value === PLACEHOLDER_YAML}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 16px",
              borderRadius: "9px",
              border: "none",
              background: "var(--accent)",
              color: "var(--bg-base)",
              fontSize: "13px",
              fontWeight: 600,
              fontFamily: "inherit",
              cursor: isWorking || yamlContent.value === PLACEHOLDER_YAML
                ? "not-allowed"
                : "pointer",
              opacity: isWorking || yamlContent.value === PLACEHOLDER_YAML
                ? 0.5
                : 1,
              transition: "opacity 0.15s",
            }}
          >
            {applying.value ? "Applying…" : "Apply"}
          </button>
        </div>
      </div>

      {/* Editor — SOLID surface, keep as-is */}
      <div
        style={{
          borderRadius: "9px",
          border: "1px solid var(--border-primary)",
          background: "var(--bg-surface)",
          overflow: "hidden",
        }}
      >
        <YamlEditor
          value={yamlContent.value}
          onChange={(v) => {
            yamlContent.value = v;
          }}
          readOnly={isWorking}
          height="calc(100vh - 320px)"
        />
      </div>

      {/* Results */}
      {(applying.value || validating.value) && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "16px 0",
          }}
        >
          <LoadingSpinner />
        </div>
      )}

      {results.value && <ApplyResults response={results.value} />}
    </div>
  );
}

function ApplyResults({ response }: { response: ApplyResponse }) {
  const { summary, results } = response;

  const summaryParts: string[] = [];
  if (summary.created > 0) summaryParts.push(`${summary.created} created`);
  if (summary.configured > 0) {
    summaryParts.push(`${summary.configured} configured`);
  }
  if (summary.unchanged > 0) {
    summaryParts.push(`${summary.unchanged} unchanged`);
  }
  if (summary.failed > 0) summaryParts.push(`${summary.failed} failed`);

  const hasFailed = summary.failed > 0;
  const accentTone = hasFailed ? "var(--warning)" : "var(--success)";

  return (
    <div
      style={{
        borderRadius: "12px",
        border: `1px solid color-mix(in srgb, ${accentTone} 30%, transparent)`,
        background: `color-mix(in srgb, ${accentTone} 8%, transparent)`,
        padding: "16px",
      }}
    >
      <p
        style={{
          fontSize: "13px",
          fontWeight: 600,
          color: accentTone,
          margin: "0 0 12px",
        }}
      >
        {summary.total} resource{summary.total !== 1 ? "s" : ""} processed
        {summaryParts.length > 0 ? `: ${summaryParts.join(", ")}` : ""}
      </p>

      {results.length > 0 && (
        <div
          style={{
            background: "var(--bg-surface)",
            borderRadius: "9px",
            border: "1px solid var(--border-subtle)",
            overflow: "hidden",
          }}
        >
          {/* Header */}
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "120px 1fr 120px 1fr",
              gap: "12px",
              padding: "8px 14px",
              borderBottom: "1px solid var(--border-subtle)",
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.05em",
              color: "var(--text-muted)",
            }}
          >
            <span>Kind</span>
            <span>Name</span>
            <span>Namespace</span>
            <span>Result</span>
          </div>
          {/* Rows */}
          {results.map((r) => (
            <div
              key={`${r.index}-${r.kind}-${r.name}`}
              style={{
                display: "grid",
                gridTemplateColumns: "120px 1fr 120px 1fr",
                gap: "12px",
                padding: "9px 14px",
                borderBottom: "1px solid var(--border-subtle)",
                fontSize: "13px",
                alignItems: "center",
              }}
            >
              <span style={{ color: "var(--text-muted)" }}>{r.kind}</span>
              <span
                style={{
                  color: "var(--text-primary)",
                  fontFamily: "var(--font-mono)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {r.name}
              </span>
              <span style={{ color: "var(--text-muted)" }}>
                {r.namespace || "—"}
              </span>
              <span>
                {r.action === "failed"
                  ? (
                    <span
                      style={{ color: "var(--error)" }}
                      title={r.error}
                    >
                      failed: {r.error}
                    </span>
                  )
                  : (
                    <span
                      style={{
                        color: r.action === "created"
                          ? "var(--success)"
                          : r.action === "configured"
                          ? "var(--accent)"
                          : "var(--text-muted)",
                      }}
                    >
                      {r.action}
                    </span>
                  )}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ── Shared style helpers ──────────────────────────────────────────────────────

function ghostButtonStyle(disabled: boolean): preact.JSX.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: "6px",
    padding: "7px 14px",
    borderRadius: "9px",
    border: "1px solid var(--border-primary)",
    background: "transparent",
    color: "var(--text-muted)",
    fontSize: "13px",
    fontWeight: 600,
    fontFamily: "inherit",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.5 : 1,
    transition: "background 0.15s",
  };
}
