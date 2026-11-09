/** @jsxImportSource react */
import { type CSSProperties, useEffect, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { ValidationResults } from "@/components/ui/ValidationResults.tsx";
import {
  type CapabilityExplanation,
  type CapabilityTone,
  capabilityFor,
  explain,
  fetchCapabilities,
} from "@/lib/capabilities.ts";
import type { CapabilitiesResponse } from "@/lib/capability-types.ts";
import {
  managedSummary,
  receiptHref,
  shortId,
  trackedRefusalText,
} from "@/lib/change-copy.ts";
import {
  managedCount,
  useOwnershipPreview,
  useRepairLink,
  useTrackingAvailability,
} from "@/lib/change-tracking.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { type ApplyResponse, useYamlApply } from "@/lib/yaml-apply.ts";
import { OwnershipSection } from "@/src/components/changes/OwnershipSection.tsx";
import { RepairBanner } from "@/src/components/changes/RepairBanner.tsx";
import { TrackedApplyPanel } from "@/src/components/changes/TrackedApplyPanel.tsx";
import YamlEditor from "@/src/islands/YamlEditor.tsx";
import {
  clusterEpoch,
  currentTarget,
  selectedCluster,
} from "@/src/lib/cluster.ts";
import {
  clusterLabel as labelCluster,
  useClusterNames,
} from "@/src/lib/cluster-label.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

const TRACKING_NOTE_ID = "yaml-apply-tracking-note";

type CapabilityState =
  | { status: "loading" }
  | { status: "ready"; caps: CapabilitiesResponse }
  | { status: "error" };

export default function YamlApplyPage() {
  const forceConflicts = useSignal(false);
  // Change tracking starts off and is switched on only once GET /v1/changes
  // proves the server can keep the record (see the probe below).
  const tracked = useSignal(false);
  const repairOf = useSignal<string | null>(null);
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
    lastOperationId,
    attemptOutcomeUnknown,
    trackedRefusal,
  } = useYamlApply(PLACEHOLDER_YAML, {
    forceConflicts,
    pinApplyToPreview: true,
    tracked,
    repairOf,
  });
  const [capability, setCapability] = useState<CapabilityState>({
    status: "loading",
  });

  // Reading the epoch here subscribes the page to cluster switches, so the
  // capability check below re-runs for whichever cluster is now selected.
  const epoch = useSignalValue(clusterEpoch);
  const selected = useSignalValue(selectedCluster);
  const yamlText = useSignalValue(yamlContent);
  const applyingNow = useSignalValue(applying);
  const validatingNow = useSignalValue(validating);
  const errorText = useSignalValue(error);
  const resultNow = useSignalValue(results);
  const previewNow = useSignalValue(preview);
  const pinNow = useSignalValue(pin);
  const stale = useSignalValue(pinStale);
  const lastOpId = useSignalValue(lastOperationId);
  const outcomeUnknown = useSignalValue(attemptOutcomeUnknown);
  const refusal = useSignalValue(trackedRefusal);
  const forceNow = useSignalValue(forceConflicts);
  const trackedNow = useSignalValue(tracked);

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
    setCapability({ status: "loading" });
    fetchCapabilities(target, controller.signal)
      .then((caps) => {
        if (clusterEpoch.peek() === epoch) {
          setCapability({ status: "ready", caps });
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setCapability({ status: "error" });
      });
    return () => controller.abort();
  }, [epoch]);

  // Remote cluster ids are opaque, so name the ones on screen from the
  // registry. Best effort: an id is still an unambiguous label.
  // The repair link (Release E) is read first: the cluster its receipt names
  // is one of the clusters this page may have to name.
  const { repair: repairNow, stopRepair } = useRepairLink((id) => {
    repairOf.value = id;
  });
  const repairClusterId =
    repairNow.status === "valid" ? repairNow.clusterId : undefined;
  const pinnedClusterId = pinNow?.target.clusterId;
  const clusterNames = useClusterNames([
    selected,
    pinnedClusterId,
    repairClusterId,
  ]);
  const namesNow = useSignalValue(clusterNames);

  // Change tracking (Release E): whether the server can keep a record, and
  // who manages the previewed objects.
  const trackingNow = useTrackingAvailability((on) => {
    tracked.value = on;
  });
  const ownershipNow = useOwnershipPreview(previewNow, pinNow, yamlText);

  const clusterLabel = (id: string) => labelCluster(id, namesNow);

  const handleRepreview = () => {
    clearPin();
    void handleValidate();
  };

  const handleFileUpload = () => {
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
  };

  const isWorking = applyingNow || validatingNow;
  const isEmpty = yamlText === PLACEHOLDER_YAML;
  const applyDisabled = isWorking || isEmpty || pinNow === null;
  const capabilityNotices =
    capability.status === "ready"
      ? noticesFor(capability.caps)
      : capability.status === "error"
        ? [
            {
              tone: "unknown" as const,
              message:
                "Could not check what this cluster allows. Validate and Apply will still report any problem.",
            },
          ]
        : [];
  const managedText = managedSummary(managedCount(ownershipNow));
  // Only a tracked apply whose failure may still have changed the cluster
  // (the hook decides: an unknown outcome, or a store failure that does not
  // rule it out) points at its receipt. Definite refusals do not, and a
  // later Validate or Apply press clears it.
  const attemptReceiptId = outcomeUnknown && !resultNow ? lastOpId : null;
  const availability = trackingNow;
  const trackingDisabled =
    availability.status === "checking" ||
    availability.status === "unsupported" ||
    availability.status === "unavailable";
  const trackingNote =
    availability.status === "unsupported" ||
    availability.status === "unavailable" ||
    availability.status === "unconfirmed"
      ? availability.reason
      : null;

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

      {errorText && (
        <div role="alert">
          <ErrorBanner
            message={refusal ? trackedRefusalText(refusal) : errorText}
          />
        </div>
      )}

      {refusal?.receiptId && (
        <p className="m-0 text-sm">
          <a
            href={receiptHref(refusal.receiptId)}
            className="font-medium text-accent"
          >
            Open the change receipt of the apply still running
          </a>
        </p>
      )}

      {attemptReceiptId && (
        <p className="m-0 text-sm text-text-secondary">
          This attempt may have changed the cluster. If the server recorded it,
          its receipt shows what was applied:{" "}
          <a
            href={receiptHref(attemptReceiptId)}
            className="font-medium text-accent"
          >
            change receipt {shortId(attemptReceiptId)}
          </a>
          . It may not exist if the request never reached the server.
        </p>
      )}

      <RepairBanner
        repair={repairNow}
        tracked={trackedNow}
        onStop={stopRepair}
        selectedClusterId={selected}
        clusterLabel={clusterLabel}
      />

      {managedText && !resultNow && (
        <div role="status">
          <Alert variant="warning">{managedText}</Alert>
        </div>
      )}

      {pinNow && stale && (
        <div role="status">
          <Alert
            variant="warning"
            className="flex flex-wrap items-center justify-between gap-3"
          >
            <span>
              You are now viewing <strong>{clusterLabel(selected)}</strong>.
              Apply still targets{" "}
              <strong>{clusterLabel(pinNow.target.clusterId)}</strong>.
            </span>
            <button
              type="button"
              onClick={handleRepreview}
              disabled={isWorking || isEmpty}
              style={ghostButtonStyle(isWorking || isEmpty)}
            >
              Re-preview on {clusterLabel(selected)}
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
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "10px",
            flexWrap: "wrap",
          }}
        >
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
              checked={forceNow}
              onChange={(e) => {
                forceConflicts.value = e.currentTarget.checked;
              }}
              style={{ accentColor: "var(--accent)" }}
            />
            Force conflicts
          </label>
          <label
            className={`flex items-center gap-1.5 text-[13px] text-text-muted ${
              trackingDisabled
                ? "cursor-not-allowed opacity-60"
                : "cursor-pointer"
            }`}
          >
            <input
              type="checkbox"
              checked={trackedNow}
              disabled={trackingDisabled || isWorking}
              aria-describedby={trackingNote ? TRACKING_NOTE_ID : undefined}
              onChange={(e) => {
                tracked.value = e.currentTarget.checked;
              }}
              className="accent-accent"
            />
            Keep a record of this change
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
            {pinNow ? (
              <>
                Applies to{" "}
                <strong style={{ color: "var(--text-primary)" }}>
                  {clusterLabel(pinNow.target.clusterId)}
                </strong>{" "}
                — previewed <PinAge pinnedAt={pinNow.pinnedAt} />
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
            {validatingNow ? "Validating…" : "Validate"}
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
            {applyingNow ? "Applying…" : "Apply"}
          </button>
        </div>
      </div>

      {trackingNote && (
        <p id={TRACKING_NOTE_ID} className="-mt-3 mb-0 text-xs text-text-muted">
          {trackingNote}
        </p>
      )}

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
          value={yamlText}
          onChange={(v) => {
            // Editors can echo programmatic value changes (e.g. re-applying
            // the same value); ignore those so a valid pin isn't dropped for
            // content that never actually changed. A real edit invalidates
            // the previous verdict, so clear the pin until re-validated.
            if (v === yamlContent.peek()) return;
            yamlContent.value = v;
            clearPin();
          }}
          readOnly={isWorking}
          height="calc(100vh - 320px)"
        />
      </div>

      {/* Results */}
      {(applyingNow || validatingNow) && (
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

      {previewNow && !resultNow && <ValidationResults response={previewNow} />}
      {resultNow && <ApplyResults response={resultNow} />}
      {resultNow && (
        <TrackedApplyPanel
          tracking={resultNow.tracking}
          sentOperationId={lastOpId}
        />
      )}
      <OwnershipSection state={ownershipNow} />
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

function ghostButtonStyle(disabled: boolean): CSSProperties {
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
