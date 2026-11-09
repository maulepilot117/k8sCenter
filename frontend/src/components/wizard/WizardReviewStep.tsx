/** @jsxImportSource react */
import {
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiPostRaw } from "@/lib/api.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";
import { wizardBusy } from "@/lib/wizard-busy.ts";
import { WizardPrimaryActionContext } from "@/lib/wizard-primary-action.ts";
import { MonacoEditor } from "@/src/components/ui/MonacoEditor.tsx";
import { useLatest } from "@/src/lib/use-latest.ts";

interface ApplyResult {
  action: string;
  kind: string;
  name: string;
  namespace?: string;
  error?: string;
}

interface ApplyResponse {
  results: ApplyResult[];
  summary: {
    created: number;
    configured: number;
    unchanged: number;
    failed: number;
  };
}

interface WizardReviewStepProps {
  yaml: string;
  onYamlChange: (yaml: string) => void;
  loading: boolean;
  error: string | null;
  /** Base path for building the detail link (e.g."/workloads/deployments") */
  detailBasePath: string;
  /**
   * Sets the wizard's unsaved-changes flag (the one its `useDirtyGuard`
   * watches). Called with false once an apply finishes with nothing failed,
   * so leaving for the new resource does not prompt.
   */
  onDirtyChange?: (dirty: boolean) => void;
  /**
   * Masks values for display (the Secret wizard's `maskSecretYaml`). While
   * masked the preview is read-only, so an edit can never write the mask
   * into the manifest; "Show values" turns the editable preview back on.
   * The YAML applied is always `yaml`, never the masked text.
   */
  maskValues?: (yaml: string) => string;
}

/**
 * The last step of every wizard: an editable preview of the generated YAML,
 * applied from the wizard footer's primary button (registered through
 * `WizardPrimaryActionContext`), then a per-object result.
 */
export function WizardReviewStep({
  yaml,
  onYamlChange,
  loading,
  error,
  detailBasePath,
  onDirtyChange,
  maskValues,
}: WizardReviewStepProps) {
  const [revealed, setRevealed] = useState(false);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [applyResult, setApplyResult] = useState<ApplyResponse | null>(null);
  const applyGuard = useRef(false);
  const primary = useContext(WizardPrimaryActionContext);
  const onDirtyChangeRef = useLatest(onDirtyChange);

  // Reset the shared wizard-busy flag if this step unmounts mid-apply, so a
  // stale `true` can never wedge the next wizard's close controls.
  useEffect(
    () => () => {
      wizardBusy.value = false;
    },
    [],
  );

  const handleApply = async () => {
    if (!yaml.trim() || applyGuard.current) return;
    applyGuard.current = true;
    setApplying(true);
    wizardBusy.value = true;
    setApplyError(null);
    setApplyResult(null);

    try {
      const resp = await apiPostRaw<ApplyResponse>(
        "/v1/yaml/apply",
        yaml,
        "text/yaml",
      );
      setApplyResult(resp.data);
      if (resp.data.summary.failed === 0) onDirtyChangeRef.current?.(false);
    } catch (err) {
      setApplyError(
        err instanceof Error ? err.message : "Failed to apply resource",
      );
    } finally {
      setApplying(false);
      wizardBusy.value = false;
      applyGuard.current = false;
    }
  };

  // The footer calls the latest handleApply (it closes over the current yaml)
  // through this ref, so the registered action only changes with its label
  // and state, not on every render.
  const applyRef = useLatest(handleApply);

  const result = applyResult;
  const firstResult = result?.results[0];
  // A cluster-scoped object has no namespace segment in its detail URL.
  const detailPath =
    firstResult && !firstResult.error
      ? [detailBasePath, firstResult.namespace, firstResult.name]
          .filter(Boolean)
          .map((seg, i) => (i === 0 ? seg : encodeURIComponent(seg as string)))
          .join("/")
      : null;
  const label = result ? (detailPath ? "View resource" : "Done") : "Apply";
  const href = result ? (detailPath ?? undefined) : undefined;
  const disabled = !result && (loading || !!error || !yaml.trim() || applying);
  const hasResult = result !== null;

  useLayoutEffect(() => {
    if (!primary) return;
    primary({
      label,
      href,
      disabled,
      loading: applying,
      // After a result, a click with no href falls through to the wizard's
      // own last-step action, which closes it.
      onClick: hasResult ? undefined : () => applyRef.current(),
    });
  }, [primary, label, href, disabled, applying, hasResult]);

  useEffect(
    () => () => {
      primary?.(null);
    },
    [primary],
  );

  if (loading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="flex items-center gap-3 text-text-muted">
          <Spinner size="md" />
          Generating YAML preview...
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-md bg-danger/10 border border-danger/30 p-4 text-danger text-sm">
        <p className="font-medium">Failed to generate preview</p>
        <p className="mt-1">{error}</p>
      </div>
    );
  }

  // Show apply results
  if (result) {
    const hasFailures = result.summary.failed > 0;

    return (
      <div className="space-y-4">
        <div
          className={`rounded-md p-4 text-sm ${
            hasFailures
              ? "bg-danger/10 border border-danger/30"
              : "bg-success/10 border border-success/30"
          }`}
        >
          <p
            className={`font-medium ${
              hasFailures ? "text-danger" : "text-success"
            }`}
          >
            {hasFailures
              ? "Apply completed with errors"
              : "Applied successfully"}
          </p>
          <div className="mt-2 space-y-1">
            {withUniqueKeys(
              result.results,
              (r) => `${r.kind}/${r.namespace ?? ""}/${r.name}/${r.action}`,
            ).map(({ item: r, key }) => (
              <div
                key={key}
                className={`flex items-center gap-2 ${
                  r.error ? "text-danger" : "text-text-secondary"
                }`}
              >
                {r.error ? (
                  <svg
                    className="w-4 h-4 text-danger"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth="2"
                      d="M6 18L18 6M6 6l12 12"
                    />
                  </svg>
                ) : (
                  <svg
                    className="w-4 h-4 text-success"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth="2"
                      d="M5 13l4 4L19 7"
                    />
                  </svg>
                )}
                <span>
                  {r.kind} <strong>{r.name}</strong>
                  {r.namespace ? ` in ${r.namespace}` : ""}: {r.action}
                </span>
                {r.error && (
                  <span className="text-xs text-danger ml-2">({r.error})</span>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    );
  }

  // The editor takes whatever height the step pane has left, so the whole
  // manifest is reachable without an Apply button scrolled out of view.
  const masked = maskValues !== undefined && !revealed;
  return (
    <div className="flex flex-col flex-1 min-h-0 gap-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-text-muted">
          {masked
            ? "Review the generated YAML below. Values are hidden; show them to check or edit them."
            : "Review the generated YAML below. You can edit it before applying."}
        </p>
        {maskValues && (
          <button
            type="button"
            className="shrink-0 text-sm text-brand hover:underline"
            aria-pressed={revealed}
            onClick={() => {
              setRevealed((prev) => !prev);
            }}
          >
            {revealed ? "Hide values" : "Show values"}
          </button>
        )}
      </div>

      <div className="flex-1 min-h-60">
        <MonacoEditor
          value={masked ? maskValues(yaml) : yaml}
          onChange={onYamlChange}
          readOnly={masked}
          height="100%"
        />
      </div>

      {applyError && (
        <div className="rounded-md bg-danger/10 border border-danger/30 p-3 text-danger text-sm">
          {applyError}
        </div>
      )}
    </div>
  );
}
