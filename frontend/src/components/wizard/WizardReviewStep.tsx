import { useSignal } from "@preact/signals";
import { useContext, useEffect, useLayoutEffect, useRef } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiPostRaw } from "@/lib/api.ts";
import { wizardBusy } from "@/lib/wizard-busy.ts";
import { WizardPrimaryActionContext } from "@/lib/wizard-primary-action.ts";
import { MonacoEditor } from "@/src/components/ui/MonacoEditor.tsx";

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
   * Called once an apply finishes with nothing failed, so the wizard can drop
   * its unsaved-changes guard before the user navigates to the new resource.
   */
  onApplied?: () => void;
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
  onApplied,
}: WizardReviewStepProps) {
  const applying = useSignal(false);
  const applyError = useSignal<string | null>(null);
  const applyResult = useSignal<ApplyResponse | null>(null);
  const applyGuard = useRef(false);
  const primary = useContext(WizardPrimaryActionContext);

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
    applying.value = true;
    wizardBusy.value = true;
    applyError.value = null;
    applyResult.value = null;

    try {
      const resp = await apiPostRaw<ApplyResponse>(
        "/v1/yaml/apply",
        yaml,
        "text/yaml",
      );
      applyResult.value = resp.data;
      if (resp.data.summary.failed === 0) onApplied?.();
    } catch (err) {
      applyError.value =
        err instanceof Error ? err.message : "Failed to apply resource";
    } finally {
      applying.value = false;
      wizardBusy.value = false;
      applyGuard.current = false;
    }
  };

  // The footer calls the latest handleApply (it closes over the current yaml)
  // through this ref, so the registered action only changes with its label
  // and state, not on every render.
  const applyRef = useRef(handleApply);
  applyRef.current = handleApply;

  const result = applyResult.value;
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
  const disabled =
    !result && (loading || !!error || !yaml.trim() || applying.value);
  const busy = applying.value;
  const hasResult = result !== null;

  useLayoutEffect(() => {
    if (!primary) return;
    primary.value = {
      label,
      href,
      disabled,
      loading: busy,
      // After a result, a click with no href falls through to the wizard's
      // own last-step action, which closes it.
      onClick: hasResult ? undefined : () => applyRef.current(),
    };
  }, [primary, label, href, disabled, busy, hasResult]);

  useEffect(
    () => () => {
      if (primary) primary.value = null;
    },
    [primary],
  );

  if (loading) {
    return (
      <div class="flex items-center justify-center py-16">
        <div class="flex items-center gap-3 text-text-muted">
          <Spinner size="md" />
          Generating YAML preview...
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div class="rounded-md bg-danger/10 border border-danger/30 p-4 text-danger text-sm">
        <p class="font-medium">Failed to generate preview</p>
        <p class="mt-1">{error}</p>
      </div>
    );
  }

  // Show apply results
  if (result) {
    const hasFailures = result.summary.failed > 0;

    return (
      <div class="space-y-4">
        <div
          class={`rounded-md p-4 text-sm ${
            hasFailures
              ? "bg-danger/10 border border-danger/30"
              : "bg-success/10 border border-success/30"
          }`}
        >
          <p
            class={`font-medium ${
              hasFailures ? "text-danger" : "text-success"
            }`}
          >
            {hasFailures
              ? "Apply completed with errors"
              : "Applied successfully"}
          </p>
          <div class="mt-2 space-y-1">
            {result.results.map((r, i) => (
              <div
                key={i}
                class={`flex items-center gap-2 ${
                  r.error ? "text-danger" : "text-text-secondary"
                }`}
              >
                {r.error ? (
                  <svg
                    class="w-4 h-4 text-danger"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M6 18L18 6M6 6l12 12"
                    />
                  </svg>
                ) : (
                  <svg
                    class="w-4 h-4 text-success"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M5 13l4 4L19 7"
                    />
                  </svg>
                )}
                <span>
                  {r.kind} <strong>{r.name}</strong>
                  {r.namespace ? ` in ${r.namespace}` : ""}: {r.action}
                </span>
                {r.error && (
                  <span class="text-xs text-danger ml-2">({r.error})</span>
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
  return (
    <div class="flex flex-col flex-1 min-h-0 gap-4">
      <p class="text-sm text-text-muted">
        Review the generated YAML below. You can edit it before applying.
      </p>

      <div class="flex-1 min-h-60">
        <MonacoEditor
          value={yaml}
          onChange={onYamlChange}
          readOnly={false}
          height="100%"
        />
      </div>

      {applyError.value && (
        <div class="rounded-md bg-danger/10 border border-danger/30 p-3 text-danger text-sm">
          {applyError.value}
        </div>
      )}
    </div>
  );
}
