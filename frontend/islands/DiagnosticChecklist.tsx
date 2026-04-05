import type { Signal } from "@preact/signals";
import { KIND_ROUTE_MAP } from "@/lib/types/diagnostics.ts";

export interface DiagnosticResult {
  ruleName: string;
  status: "pass" | "warn" | "fail";
  severity: "critical" | "warning" | "info";
  message: string;
  detail?: string;
  remediation?: string;
  links?: { label: string; kind: string; name: string }[];
}

interface DiagnosticChecklistProps {
  results: Signal<DiagnosticResult[]>;
  namespace: string;
}

function severityColor(severity: string): string {
  switch (severity) {
    case "critical":
      return "var(--error)";
    case "warning":
      return "var(--warning)";
    default:
      return "var(--text-muted)";
  }
}

function severityBg(severity: string): string {
  switch (severity) {
    case "critical":
      return "var(--error-dim)";
    case "warning":
      return "var(--warning-dim)";
    default:
      return "var(--bg-elevated)";
  }
}

function linkHref(
  link: { label: string; kind: string; name: string },
  namespace: string,
): string {
  if (link.label === "View Logs") {
    return `/observability/logs?namespace=${namespace}&pod=${link.name}`;
  }
  const route = KIND_ROUTE_MAP[link.kind] ?? link.kind.toLowerCase() + "s";
  return `/workloads/${route}/${namespace}/${link.name}`;
}

function FailedCheck(
  { result, namespace }: { result: DiagnosticResult; namespace: string },
) {
  const icon = result.status === "fail" ? "✗" : "⚠";
  const iconColor = result.status === "fail"
    ? "var(--error)"
    : "var(--warning)";

  return (
    <div
      style={{
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        background: "var(--bg-surface)",
        padding: "14px 16px",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "10px",
          marginBottom: "8px",
        }}
      >
        <span
          style={{ color: iconColor, fontWeight: 700, fontSize: "14px" }}
        >
          {icon}
        </span>
        <span
          style={{
            fontWeight: 600,
            fontSize: "13px",
            color: "var(--text-primary)",
            flex: 1,
          }}
        >
          {result.ruleName}
        </span>
        <span
          style={{
            fontSize: "11px",
            fontWeight: 500,
            padding: "2px 8px",
            borderRadius: "var(--radius-sm)",
            background: severityBg(result.severity),
            color: severityColor(result.severity),
          }}
        >
          {result.severity}
        </span>
      </div>

      <p
        style={{
          margin: "0 0 6px",
          fontSize: "13px",
          color: "var(--text-primary)",
        }}
      >
        {result.message}
      </p>

      {result.detail && (
        <p
          style={{
            margin: "0 0 6px",
            fontSize: "12px",
            color: "var(--text-muted)",
          }}
        >
          {result.detail}
        </p>
      )}

      {result.remediation && (
        <p
          style={{
            margin: "0 0 8px",
            fontSize: "12px",
            fontStyle: "italic",
            color: "var(--text-secondary)",
          }}
        >
          {result.remediation}
        </p>
      )}

      {result.links && result.links.length > 0 && (
        <div style={{ display: "flex", gap: "6px", flexWrap: "wrap" }}>
          {result.links.map((link) => (
            <a
              key={`${link.kind}-${link.name}-${link.label}`}
              href={linkHref(link, namespace)}
              style={{
                fontSize: "11px",
                fontWeight: 500,
                padding: "3px 8px",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--border-primary)",
                color: "var(--accent)",
                textDecoration: "none",
                background: "transparent",
              }}
            >
              {link.label}
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function PassedCheck({ result }: { result: DiagnosticResult }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "10px",
        padding: "8px 16px",
        borderBottom: "1px solid var(--border-primary)",
      }}
    >
      <span
        style={{ color: "var(--success)", fontWeight: 700, fontSize: "14px" }}
      >
        ✓
      </span>
      <span
        style={{
          fontSize: "13px",
          fontWeight: 500,
          color: "var(--success)",
          flex: 1,
        }}
      >
        {result.ruleName}
      </span>
      <span
        style={{ fontSize: "12px", color: "var(--text-muted)" }}
      >
        {result.message}
      </span>
    </div>
  );
}

export default function DiagnosticChecklist(
  { results, namespace }: DiagnosticChecklistProps,
) {
  const failed = results.value.filter((r) =>
    r.status === "fail" || r.status === "warn"
  );
  const passed = results.value.filter((r) => r.status === "pass");

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Failed / Warned Checks */}
      <div>
        <h3
          style={{
            fontSize: "13px",
            fontWeight: 600,
            color: "var(--text-secondary)",
            marginBottom: "10px",
            textTransform: "uppercase",
            letterSpacing: "0.05em",
          }}
        >
          Failed Checks ({failed.length})
        </h3>
        {failed.length === 0
          ? (
            <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
              No issues found
            </p>
          )
          : (
            <div
              style={{ display: "flex", flexDirection: "column", gap: "8px" }}
            >
              {failed.map((r) => (
                <FailedCheck
                  key={r.ruleName}
                  result={r}
                  namespace={namespace}
                />
              ))}
            </div>
          )}
      </div>

      {/* Passed Checks */}
      <div>
        <h3
          style={{
            fontSize: "13px",
            fontWeight: 600,
            color: "var(--text-secondary)",
            marginBottom: "10px",
            textTransform: "uppercase",
            letterSpacing: "0.05em",
          }}
        >
          Passed Checks ({passed.length})
        </h3>
        {passed.length === 0
          ? (
            <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
              No checks passed
            </p>
          )
          : (
            <div
              style={{
                border: "1px solid var(--border-primary)",
                borderRadius: "var(--radius)",
                background: "var(--bg-surface)",
                overflow: "hidden",
              }}
            >
              {passed.map((r) => <PassedCheck key={r.ruleName} result={r} />)}
            </div>
          )}
      </div>
    </div>
  );
}
