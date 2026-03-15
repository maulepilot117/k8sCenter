import { useSignal } from "@preact/signals";
import { useRef } from "preact/hooks";
import { MonacoEditor } from "@/components/ui/MonacoEditor.tsx";
import { apiPostRaw } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";

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
  /** Base path for building the detail link (e.g. "/workloads/deployments") */
  detailBasePath: string;
}

export function WizardReviewStep({
  yaml,
  onYamlChange,
  loading,
  error,
  detailBasePath,
}: WizardReviewStepProps) {
  const applying = useSignal(false);
  const applyError = useSignal<string | null>(null);
  const applyResult = useSignal<ApplyResponse | null>(null);
  const applyGuard = useRef(false);

  const handleApply = async () => {
    if (!yaml.trim() || applyGuard.current) return;
    applyGuard.current = true;
    applying.value = true;
    applyError.value = null;
    applyResult.value = null;

    try {
      const resp = await apiPostRaw<ApplyResponse>(
        "/v1/yaml/apply",
        yaml,
        "text/yaml",
      );
      applyResult.value = resp.data;
    } catch (err) {
      applyError.value = err instanceof Error
        ? err.message
        : "Failed to apply resource";
    } finally {
      applying.value = false;
      applyGuard.current = false;
    }
  };

  if (loading) {
    return (
      <div class="flex items-center justify-center py-16">
        <div class="flex items-center gap-3 text-slate-500">
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
  if (applyResult.value) {
    const result = applyResult.value;
    const hasFailures = result.summary.failed > 0;
    const firstResult = result.results[0];
    const detailPath = firstResult && !firstResult.error
      ? `${detailBasePath}/${encodeURIComponent(firstResult.namespace ?? "")}/${
        encodeURIComponent(firstResult.name)
      }`
      : null;

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
                  r.error ? "text-danger" : "text-slate-700 dark:text-slate-300"
                }`}
              >
                {r.error
                  ? (
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
                  )
                  : (
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

        {detailPath && (
          <a
            href={detailPath}
            class="inline-flex items-center gap-2 text-brand hover:text-brand/80 text-sm font-medium"
          >
            View Resource
            <svg
              class="w-4 h-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M9 5l7 7-7 7"
              />
            </svg>
          </a>
        )}
      </div>
    );
  }

  return (
    <div class="space-y-4">
      <p class="text-sm text-slate-500">
        Review the generated YAML below. You can edit it before applying.
      </p>

      <MonacoEditor
        value={yaml}
        onChange={onYamlChange}
        readOnly={false}
        height="400px"
      />

      {applyError.value && (
        <div class="rounded-md bg-danger/10 border border-danger/30 p-3 text-danger text-sm">
          {applyError.value}
        </div>
      )}

      <div class="flex justify-end">
        <Button
          variant="primary"
          onClick={handleApply}
          loading={applying.value}
          disabled={!yaml.trim() || applying.value}
        >
          Apply
        </Button>
      </div>
    </div>
  );
}
