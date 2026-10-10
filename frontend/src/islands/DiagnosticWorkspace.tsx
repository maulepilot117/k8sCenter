import { useSignal } from "@preact/signals";
import { useEffect, useId } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type { AffectedResource } from "@/src/islands/BlastRadiusPanel.tsx";
import BlastRadiusPanel from "@/src/islands/BlastRadiusPanel.tsx";
import CaptureToIncidentButton, {
  earliestObservedAt,
} from "@/src/islands/CaptureToIncidentButton.tsx";
import type { DiagnosticResult } from "@/src/islands/DiagnosticChecklist.tsx";
import DiagnosticChecklist from "@/src/islands/DiagnosticChecklist.tsx";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface DiagnosticResponse {
  target: { kind: string; name: string; namespace: string };
  results: DiagnosticResult[];
  blastRadius: {
    directlyAffected: AffectedResource[];
    potentiallyAffected: AffectedResource[];
    // Set when the graph was built from a partial read: some kinds were
    // capped or timed out, so the lists are not the whole radius.
    truncated?: boolean;
    errors?: Record<string, string>;
  };
}

const KIND_OPTIONS = [
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "Pod",
  "Service",
  "PersistentVolumeClaim",
];

const LABEL_CLASS = "mb-1 block text-xs font-medium text-text-secondary";
const FIELD_CLASS =
  "w-full rounded-md border border-border-primary bg-base px-2.5 py-1.5 text-sm text-text-primary";

// Status banner colors, keyed by the worst result. Full class names (not
// built from fragments) so Tailwind's scanner sees every one.
const TONES = {
  critical: { banner: "border-error-dim bg-error-dim", text: "text-error" },
  warning: {
    banner: "border-warning-dim bg-warning-dim",
    text: "text-warning",
  },
  passed: { banner: "border-success-dim bg-success-dim", text: "text-success" },
};

export default function DiagnosticWorkspace() {
  const namespace = useSignal("");
  const kind = useSignal("");
  const name = useSignal("");
  const fieldId = useId();

  const loading = useSignal(false);
  const error = useSignal<string | null>(null);
  // The backend refuses diagnostics under a remote cluster selection with
  // reason unsupported_platform (#532): the target, its pods and the blast
  // radius come from the local cluster's informers. That is a fixed fact
  // about the feature, not a failure, so it is not shown as an error.
  const remoteUnsupported = useSignal(false);

  const results = useSignal<DiagnosticResult[]>([]);
  const directlyAffected = useSignal<AffectedResource[]>([]);
  const potentiallyAffected = useSignal<AffectedResource[]>([]);
  const truncated = useSignal(false);
  const truncationErrors = useSignal<Record<string, string>>({});
  const hasData = useSignal(false);

  const fetchDiagnostics = async (ns: string, k: string, n: string) => {
    loading.value = true;
    error.value = null;
    remoteUnsupported.value = false;
    truncated.value = false;
    truncationErrors.value = {};
    try {
      const resp = await apiGet<DiagnosticResponse>(
        `/v1/diagnostics/${ns}/${k}/${n}`,
      );
      const data = resp.data;
      results.value = data.results;
      directlyAffected.value = data.blastRadius.directlyAffected;
      potentiallyAffected.value = data.blastRadius.potentiallyAffected;
      truncated.value = data.blastRadius.truncated === true;
      truncationErrors.value = data.blastRadius.errors ?? {};
      hasData.value = true;
    } catch (err) {
      hasData.value = false;
      if (err instanceof ApiError && err.reason === "unsupported_platform") {
        remoteUnsupported.value = true;
        return;
      }
      error.value =
        err instanceof Error ? err.message : "Failed to run diagnostics";
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
      (r.status === "fail" && r.severity === "warning") || r.status === "warn",
  ).length;
  const tone =
    criticalCount > 0
      ? TONES.critical
      : warningCount > 0
        ? TONES.warning
        : TONES.passed;

  return (
    <div class="flex flex-col gap-4">
      {/* Resource Picker */}
      {!hasData.value && !loading.value && (
        <div class="rounded-lg border border-border-primary bg-surface p-6">
          <h3 class="mb-4 text-sm font-semibold text-text-primary">
            Select a resource to investigate
          </h3>
          <div class="grid grid-cols-[1fr_1fr_1fr_auto] items-end gap-3">
            <div>
              <label for={`${fieldId}-namespace`} class={LABEL_CLASS}>
                Namespace
              </label>
              <input
                type="text"
                id={`${fieldId}-namespace`}
                placeholder="default"
                value={namespace.value}
                onInput={(e) =>
                  (namespace.value = (e.target as HTMLInputElement).value)
                }
                class={FIELD_CLASS}
              />
            </div>
            <div>
              <label for={`${fieldId}-kind`} class={LABEL_CLASS}>
                Kind
              </label>
              <select
                id={`${fieldId}-kind`}
                value={kind.value}
                onChange={(e) =>
                  (kind.value = (e.target as HTMLSelectElement).value)
                }
                class={FIELD_CLASS}
              >
                <option value="">Select kind...</option>
                {KIND_OPTIONS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label for={`${fieldId}-name`} class={LABEL_CLASS}>
                Name
              </label>
              <input
                type="text"
                id={`${fieldId}-name`}
                placeholder="my-deployment"
                value={name.value}
                onInput={(e) =>
                  (name.value = (e.target as HTMLInputElement).value)
                }
                class={FIELD_CLASS}
              />
            </div>
            <button
              type="button"
              onClick={handleInvestigate}
              disabled={!namespace.value || !kind.value || !name.value}
              class="cursor-pointer rounded-md border border-accent bg-accent px-4 py-1.5 text-sm font-semibold text-(--bg-base) disabled:cursor-not-allowed disabled:opacity-50"
            >
              Investigate
            </button>
          </div>
        </div>
      )}

      {/* Loading */}
      {loading.value && (
        <div class="p-12 text-center text-sm text-text-muted">
          Running diagnostics...
        </div>
      )}

      {/* Unsupported on a remote cluster */}
      {remoteUnsupported.value && (
        <div data-diagnostics-state="remote-unsupported">
          <Alert variant="info">
            Resource diagnostics are not available for remote clusters. The
            checks and the blast radius are built from the local cluster's live
            resource cache, which remote clusters do not have. Switch to the
            local cluster to investigate this resource.
          </Alert>
        </div>
      )}

      {/* Error */}
      {error.value && (
        <div class="rounded-lg border border-error-dim bg-error-dim px-4 py-3 text-sm text-error">
          {error.value}
        </div>
      )}

      {/* Results */}
      {hasData.value && !loading.value && (
        <>
          {/* Status Banner */}
          <div
            class={`flex items-center justify-between rounded-lg border px-4 py-3 ${tone.banner}`}
          >
            <div class="flex items-center gap-3">
              <span class={`text-sm font-semibold ${tone.text}`}>
                {criticalCount > 0
                  ? `${criticalCount} critical issue${
                      criticalCount > 1 ? "s" : ""
                    }`
                  : warningCount > 0
                    ? `${warningCount} warning${warningCount > 1 ? "s" : ""}`
                    : "All checks passed"}
              </span>
              <span class="text-sm text-text-secondary">
                {kind.value}/{name.value} in {namespace.value}
              </span>
            </div>
            <CaptureToIncidentButton
              class="ml-auto mr-2"
              clusterId={selectedCluster.value}
              namespace={namespace.value}
              kind={kind.value}
              name={name.value}
              windowStart={earliestObservedAt(results.value)}
            />
            <button
              type="button"
              onClick={() =>
                fetchDiagnostics(namespace.value, kind.value, name.value)
              }
              class="cursor-pointer rounded-md border border-border-primary bg-surface px-3 py-1 text-xs font-medium text-text-secondary"
            >
              Re-scan
            </button>
          </div>

          {/* Two-column layout */}
          <div class="grid grid-cols-[3fr_2fr] items-start gap-4">
            <DiagnosticChecklist
              results={results}
              namespace={namespace.value}
            />
            <div class="flex flex-col gap-3">
              {truncated.value && (
                <div
                  data-testid="blast-radius-truncated"
                  class={`rounded-lg border px-3 py-2 text-xs ${TONES.warning.banner} ${TONES.warning.text}`}
                >
                  <p class="font-semibold">
                    {Object.keys(truncationErrors.value).length > 0
                      ? "Blast radius is incomplete: some kinds could not be read from the selected cluster."
                      : "Blast radius is incomplete: the namespace graph was capped, so some resources are left out."}
                  </p>
                  {Object.entries(truncationErrors.value).map(([k, msg]) => (
                    <p key={k}>{msg}</p>
                  ))}
                </div>
              )}
              <BlastRadiusPanel
                directlyAffected={directlyAffected}
                potentiallyAffected={potentiallyAffected}
                namespace={namespace.value}
              />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
