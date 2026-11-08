/** @jsxImportSource react */
import { useCallback, useEffect, useId, useRef, useState } from "react";
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
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

const fromGlobal = (g: string) => (g && g !== "all" ? g : "");

export default function DiagnosticWorkspace() {
  const [namespace, setNamespace] = useState("");
  const [kind, setKind] = useState("");
  const [name, setName] = useState("");
  const fieldId = useId();

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The backend refuses diagnostics under a remote cluster selection with
  // reason unsupported_platform (#532): the target, its pods and the blast
  // radius come from the local cluster's informers. That is a fixed fact
  // about the feature, not a failure, so it is not shown as an error.
  const [remoteUnsupported, setRemoteUnsupported] = useState(false);

  const [results, setResults] = useState<DiagnosticResult[]>([]);
  const [directlyAffected, setDirectlyAffected] = useState<AffectedResource[]>(
    [],
  );
  const [potentiallyAffected, setPotentiallyAffected] = useState<
    AffectedResource[]
  >([]);
  const [truncated, setTruncated] = useState(false);
  const [truncationErrors, setTruncationErrors] = useState<
    Record<string, string>
  >({});
  const [hasData, setHasData] = useState(false);
  // The resource the displayed results are for. The picker fields can change
  // after a run (the user typing, or the namespace following the top bar), so
  // the results' heading, Re-scan and Capture to incident read this, never
  // the live fields.
  const [shown, setShown] = useState<{
    namespace: string;
    kind: string;
    name: string;
  }>({ namespace: "", kind: "", name: "" });

  const fetchDiagnostics = useCallback(
    async (ns: string, k: string, n: string) => {
      setLoading(true);
      setError(null);
      setRemoteUnsupported(false);
      setTruncated(false);
      setTruncationErrors({});
      try {
        const resp = await apiGet<DiagnosticResponse>(
          `/v1/diagnostics/${ns}/${k}/${n}`,
        );
        const data = resp.data;
        setResults(data.results);
        setDirectlyAffected(data.blastRadius.directlyAffected);
        setPotentiallyAffected(data.blastRadius.potentiallyAffected);
        setTruncated(data.blastRadius.truncated === true);
        setTruncationErrors(data.blastRadius.errors ?? {});
        setShown({ namespace: ns, kind: k, name: n });
        setHasData(true);
      } catch (err) {
        setHasData(false);
        if (err instanceof ApiError && err.reason === "unsupported_platform") {
          setRemoteUnsupported(true);
          return;
        }
        setError(
          err instanceof Error ? err.message : "Failed to run diagnostics",
        );
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  // The namespace field follows the top-bar selection until the user types
  // their own: this holds the value it was last filled with from the top bar
  // (null once a URL or the user has set it).
  const prefilled = useRef<string | null>(null);
  // The field's current value for the effect below, which compares it with
  // what the top bar last filled in. Written when the field is filled from
  // the URL or the top bar so the comparison does not wait for a render.
  const namespaceRef = useRef(namespace);
  namespaceRef.current = namespace;
  const globalNs = useSignalValue(selectedNamespace);

  // Parse URL params on mount and auto-run if all present
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    const fromUrl = params.get("namespace");
    const ns = fromUrl ?? fromGlobal(selectedNamespace.value);
    prefilled.current = fromUrl === null ? ns : null;
    const k = params.get("kind") ?? "";
    const n = params.get("name") ?? "";

    namespaceRef.current = ns;
    setNamespace(ns);
    setKind(k);
    setName(n);

    if (ns && k && n) {
      fetchDiagnostics(ns, k, n);
    }
  }, [fetchDiagnostics]);

  useEffect(() => {
    if (!IS_BROWSER || prefilled.current === null) return;
    if (namespaceRef.current !== prefilled.current) {
      prefilled.current = null; // the user typed a namespace of their own
      return;
    }
    const next = fromGlobal(globalNs);
    namespaceRef.current = next;
    setNamespace(next);
    prefilled.current = next;
  }, [globalNs]);

  const handleInvestigate = () => {
    if (!namespace || !kind || !name) return;
    // Update URL without reload
    const params = new URLSearchParams({ namespace, kind, name });
    globalThis.history.replaceState(null, "", `?${params.toString()}`);
    fetchDiagnostics(namespace, kind, name);
  };

  const clusterId = useSignalValue(selectedCluster);
  const criticalCount = results.filter(
    (r) => r.status === "fail" && r.severity === "critical",
  ).length;
  const warningCount = results.filter(
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
    <div className="flex flex-col gap-4">
      {/* Resource Picker */}
      {!hasData && !loading && (
        <div className="rounded-lg border border-border-primary bg-surface p-6">
          <h3 className="mb-4 text-sm font-semibold text-text-primary">
            Select a resource to investigate
          </h3>
          <div className="grid grid-cols-[1fr_1fr_1fr_auto] items-end gap-3">
            <div>
              <label htmlFor={`${fieldId}-namespace`} className={LABEL_CLASS}>
                Namespace
              </label>
              <input
                type="text"
                id={`${fieldId}-namespace`}
                placeholder="default"
                value={namespace}
                onChange={(e) => setNamespace(e.currentTarget.value)}
                className={FIELD_CLASS}
              />
            </div>
            <div>
              <label htmlFor={`${fieldId}-kind`} className={LABEL_CLASS}>
                Kind
              </label>
              <select
                id={`${fieldId}-kind`}
                value={kind}
                onChange={(e) => setKind(e.currentTarget.value)}
                className={FIELD_CLASS}
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
              <label htmlFor={`${fieldId}-name`} className={LABEL_CLASS}>
                Name
              </label>
              <input
                type="text"
                id={`${fieldId}-name`}
                placeholder="my-deployment"
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                className={FIELD_CLASS}
              />
            </div>
            <button
              type="button"
              onClick={handleInvestigate}
              disabled={!namespace || !kind || !name}
              className="cursor-pointer rounded-md border border-accent bg-accent px-4 py-1.5 text-sm font-semibold text-(--bg-base) disabled:cursor-not-allowed disabled:opacity-50"
            >
              Investigate
            </button>
          </div>
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div className="p-12 text-center text-sm text-text-muted">
          Running diagnostics...
        </div>
      )}

      {/* Unsupported on a remote cluster */}
      {remoteUnsupported && (
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
      {error && (
        <div className="rounded-lg border border-error-dim bg-error-dim px-4 py-3 text-sm text-error">
          {error}
        </div>
      )}

      {/* Results */}
      {hasData && !loading && (
        <>
          {/* Status Banner */}
          <div
            className={`flex items-center justify-between rounded-lg border px-4 py-3 ${tone.banner}`}
          >
            <div className="flex items-center gap-3">
              <span className={`text-sm font-semibold ${tone.text}`}>
                {criticalCount > 0
                  ? `${criticalCount} critical issue${
                      criticalCount > 1 ? "s" : ""
                    }`
                  : warningCount > 0
                    ? `${warningCount} warning${warningCount > 1 ? "s" : ""}`
                    : "All checks passed"}
              </span>
              <span className="text-sm text-text-secondary">
                {shown.kind}/{shown.name} in {shown.namespace}
              </span>
            </div>
            <CaptureToIncidentButton
              className="ml-auto mr-2"
              clusterId={clusterId}
              namespace={shown.namespace}
              kind={shown.kind}
              name={shown.name}
              windowStart={earliestObservedAt(results)}
            />
            <button
              type="button"
              onClick={() =>
                fetchDiagnostics(shown.namespace, shown.kind, shown.name)
              }
              className="cursor-pointer rounded-md border border-border-primary bg-surface px-3 py-1 text-xs font-medium text-text-secondary"
            >
              Re-scan
            </button>
          </div>

          {/* Two-column layout */}
          <div className="grid grid-cols-[3fr_2fr] items-start gap-4">
            <DiagnosticChecklist
              results={results}
              namespace={shown.namespace}
            />
            <div className="flex flex-col gap-3">
              {truncated && (
                <div
                  data-testid="blast-radius-truncated"
                  className={`rounded-lg border px-3 py-2 text-xs ${TONES.warning.banner} ${TONES.warning.text}`}
                >
                  <p className="font-semibold">
                    {Object.keys(truncationErrors).length > 0
                      ? "Blast radius is incomplete: some kinds could not be read from the selected cluster."
                      : "Blast radius is incomplete: the namespace graph was capped, so some resources are left out."}
                  </p>
                  {Object.entries(truncationErrors).map(([k, msg]) => (
                    <p key={k}>{msg}</p>
                  ))}
                </div>
              )}
              <BlastRadiusPanel
                directlyAffected={directlyAffected}
                potentiallyAffected={potentiallyAffected}
                namespace={shown.namespace}
              />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
