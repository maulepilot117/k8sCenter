import { useSignal } from "@preact/signals";
import type * as preact from "preact";
import { useCallback, useEffect, useState } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { ValidationResults } from "@/components/ui/ValidationResults.tsx";
import { apiGet } from "@/lib/api.ts";
import {
  type CapabilityExplanation,
  type CapabilityTone,
  capabilityFor,
  explain,
  fetchCapabilities,
} from "@/lib/capabilities.ts";
import type { CapabilitiesResponse } from "@/lib/capability-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { type ApplyResponse, useYamlApply } from "@/lib/yaml-apply.ts";
import YamlEditor from "@/src/islands/YamlEditor.tsx";
import {
  clusterEpoch,
  currentTarget,
  LOCAL_CLUSTER_ID,
  selectedCluster,
} from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

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

/** The YAML operations this page performs, in the order it performs them. */
const PAGE_OPERATIONS = ["yaml.validate", "yaml.apply"] as const;

type CapabilityState =
  | { status: "loading" }
  | { status: "ready"; caps: CapabilitiesResponse }
  | { status: "error" };

interface ClusterRecord {
  id: string;
  name: string;
  displayName?: string;
}

export default function YamlApplyPage() {
  const forceConflicts = useSignal(false);
  const {
    yamlContent,
    applying,
    validating,
    error,
    result: results,
    preview,
    pin,
    pinStale,
    clearPin,
    handleValidate,
    handleApply,
  } = useYamlApply(PLACEHOLDER_YAML, {
    forceConflicts,
    pinApplyToPreview: true,
  });
  const capability = useSignal<CapabilityState>({ status: "loading" });
  const clusterNames = useSignal<ReadonlyMap<string, string>>(new Map());

  // Reading the epoch here subscribes the page to cluster switches, so the
  // capability check below re-runs for whichever cluster is now selected.
  const epoch = clusterEpoch.value;

  // Set document title
  useEffect(() => {
    if (!IS_BROWSER) return;
    document.title = "YAML Apply - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  // Resolve what this cluster allows before anything is typed, so an
  // operation that cannot succeed here is explained up front rather than
  // after a failed apply. Effects never run during SSR, so no IS_BROWSER
  // guard is needed here or below.
  useEffect(() => {
    const target = currentTarget();
    const controller = new AbortController();
    capability.value = { status: "loading" };
    fetchCapabilities(target, controller.signal)
      .then((caps) => {
        if (clusterEpoch.peek() === target.epoch) {
          capability.value = { status: "ready", caps };
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) capability.value = { status: "error" };
      });
    return () => controller.abort();
  }, [epoch]);

  // Remote cluster ids are opaque, so name the ones on screen from the
  // registry. Best effort: an id is still an unambiguous label.
  const pinnedClusterId = pin.value?.target.clusterId;
  useEffect(() => {
    const known = clusterNames.peek();
    const wanted = [selectedCluster.peek(), pinnedClusterId].filter(
      (id): id is string => !!id && id !== LOCAL_CLUSTER_ID && !known.has(id),
    );
    const controller = new AbortController();
    for (const id of new Set(wanted)) {
      apiGet<ClusterRecord>(`/v1/clusters/${encodeURIComponent(id)}`, {
        signal: controller.signal,
      })
        .then((res) => {
          const c = res.data;
          if (!c) return;
          const next = new Map(clusterNames.peek());
          next.set(id, c.displayName || c.name || id);
          clusterNames.value = next;
        })
        .catch(() => {});
    }
    return () => controller.abort();
  }, [epoch, pinnedClusterId]);

  const clusterLabel = (id: string) =>
    id === LOCAL_CLUSTER_ID
      ? "the local cluster"
      : (clusterNames.value.get(id) ?? id);

  const handleRepreview = useCallback(() => {
    clearPin();
    void handleValidate();
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
        error.value = `File is too large (${(file.size / 1024 / 1024).toFixed(
          1,
        )} MB). Maximum size is 2 MB.`;
        return;
      }
      const text = await file.text();
      yamlContent.value = text;
      results.value = null;
      clearPin();
      error.value = null;
    };
    input.click();
  }, []);

  const isWorking = applying.value || validating.value;
  const isEmpty = yamlContent.value === PLACEHOLDER_YAML;
  const applyDisabled = isWorking || isEmpty || pin.value === null;
  const capabilityNotices =
    capability.value.status === "ready"
      ? noticesFor(capability.value.caps)
      : capability.value.status === "error"
        ? [
            {
              tone: "unknown" as const,
              message:
                "Could not check what this cluster allows. Validate and Apply will still report any problem.",
            },
          ]
        : [];

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

      {capabilityNotices.map((n) => (
        <CapabilityNotice key={n.message} notice={n} />
      ))}

      {error.value && <ErrorBanner message={error.value} />}

      {pin.value && pinStale.value && (
        <div role="status">
          <Alert
            variant="warning"
            class="flex flex-wrap items-center justify-between gap-3"
          >
            <span>
              You are now viewing{" "}
              <strong>{clusterLabel(selectedCluster.value)}</strong>. Apply
              still targets{" "}
              <strong>{clusterLabel(pin.value.target.clusterId)}</strong>.
            </span>
            <button
              type="button"
              onClick={handleRepreview}
              disabled={isWorking || isEmpty}
              style={ghostButtonStyle(isWorking || isEmpty)}
            >
              Re-preview on {clusterLabel(selectedCluster.value)}
            </button>
          </Alert>
        </div>
      )}

      {/* Toolbar — glass chrome */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "12px",
          flexWrap: "wrap",
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
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            flexWrap: "wrap",
          }}
        >
          <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
            {pin.value ? (
              <>
                Applies to{" "}
                <strong style={{ color: "var(--text-primary)" }}>
                  {clusterLabel(pin.value.target.clusterId)}
                </strong>{" "}
                — previewed <PinAge pinnedAt={pin.value.pinnedAt} />
              </>
            ) : (
              "Validate to choose the cluster Apply targets"
            )}
          </span>
          <button
            type="button"
            onClick={handleValidate}
            disabled={isWorking || isEmpty}
            style={ghostButtonStyle(isWorking || isEmpty)}
          >
            {validating.value ? "Validating…" : "Validate"}
          </button>
          <button
            type="button"
            onClick={handleApply}
            disabled={applyDisabled}
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
              cursor: applyDisabled ? "not-allowed" : "pointer",
              opacity: applyDisabled ? 0.5 : 1,
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
            // Editors can echo programmatic value changes (e.g. re-applying
            // the same value); ignore those so a valid pin isn't dropped for
            // content that never actually changed. A real edit invalidates
            // the previous verdict, so clear the pin until re-validated.
            if (v === yamlContent.value) return;
            yamlContent.value = v;
            clearPin();
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

      {preview.value && !results.value && (
        <ValidationResults response={preview.value} />
      )}
      {results.value && <ApplyResults response={results.value} />}
    </div>
  );
}

/**
 * The page's capability rows worth showing, one per distinct explanation.
 * Plain "ok" says nothing the operator needs to read.
 */
function noticesFor(caps: CapabilitiesResponse): CapabilityExplanation[] {
  const seen = new Set<string>();
  const out: CapabilityExplanation[] = [];
  for (const op of PAGE_OPERATIONS) {
    const cap = capabilityFor(caps, op);
    if (!cap) continue;
    const e = explain(cap);
    if (e.tone === "ok" || seen.has(e.message)) continue;
    seen.add(e.message);
    out.push(e);
  }
  return out;
}

/**
 * Each tone gets its own variant and its own leading word, so "the cluster is
 * down" or "you lack RBAC" can never be read as "k8sCenter cannot do this"
 * (D3).
 */
const NOTICE_STYLE: Record<
  Exclude<CapabilityTone, "ok">,
  { variant: "error" | "warning" | "info"; heading: string }
> = {
  blocked: { variant: "error", heading: "Blocked right now" },
  unsupported: { variant: "info", heading: "Not supported" },
  unknown: { variant: "warning", heading: "Could not confirm" },
};

function CapabilityNotice({ notice }: { notice: CapabilityExplanation }) {
  if (notice.tone === "ok") return null;
  const { variant, heading } = NOTICE_STYLE[notice.tone];
  return (
    <div role="status" data-tone={notice.tone}>
      <Alert variant={variant}>
        <strong>{heading}:</strong> {notice.message}
      </Alert>
    </div>
  );
}

/**
 * "previewed 2m ago", refreshed every 30s. Owns its own tick so the refresh
 * re-renders this text alone, not the page and its editor.
 */
function PinAge({ pinnedAt }: { pinnedAt: number }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = globalThis.setInterval(() => setTick((t) => t + 1), 30_000);
    return () => globalThis.clearInterval(id);
  }, []);
  return <>{timeAgo(new Date(pinnedAt).toISOString())}</>;
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
                {r.action === "failed" ? (
                  <span style={{ color: "var(--error)" }} title={r.error}>
                    failed: {r.error}
                  </span>
                ) : (
                  <span
                    style={{
                      color:
                        r.action === "created"
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
