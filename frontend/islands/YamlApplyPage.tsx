import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPostRaw } from "@/lib/api.ts";
import YamlEditor from "@/islands/YamlEditor.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";

interface ApplyResult {
  index: number;
  kind: string;
  name: string;
  namespace?: string;
  action: string; //"created" |"configured" |"unchanged" |"failed"
  error?: string;
}

interface ApplyResponse {
  results: ApplyResult[];
  summary: {
    total: number;
    created: number;
    configured: number;
    unchanged: number;
    failed: number;
  };
}

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
  const yamlContent = useSignal(PLACEHOLDER_YAML);
  const applying = useSignal(false);
  const validating = useSignal(false);
  const error = useSignal<string | null>(null);
  const results = useSignal<ApplyResponse | null>(null);
  const forceConflicts = useSignal(false);

  // Set document title
  useEffect(() => {
    if (!IS_BROWSER) return;
    document.title = "YAML Apply - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  const handleValidate = useCallback(async () => {
    if (applying.value || validating.value) return;
    validating.value = true;
    error.value = null;
    results.value = null;
    try {
      const res = await apiPostRaw<ApplyResponse>(
        "/v1/yaml/validate",
        yamlContent.value,
      );
      results.value = res.data;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Validation failed";
    } finally {
      validating.value = false;
    }
  }, []);

  const handleApply = useCallback(async () => {
    if (applying.value || validating.value) return;
    applying.value = true;
    error.value = null;
    results.value = null;
    try {
      const queryStr = forceConflicts.value ? "?force=true" : "";
      const res = await apiPostRaw<ApplyResponse>(
        `/v1/yaml/apply${queryStr}`,
        yamlContent.value,
      );
      results.value = res.data;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Apply failed";
    } finally {
      applying.value = false;
    }
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
    <div class="space-y-4">
      <div>
        <h1 class="text-xl font-semibold text-text-primary">
          YAML Apply
        </h1>
        <p class="mt-1 text-sm text-text-muted">
          Apply Kubernetes resources from YAML. Supports multi-document YAML
          with server-side apply.
        </p>
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      {/* Toolbar */}
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <button
            type="button"
            onClick={handleFileUpload}
            disabled={isWorking}
            class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50 text-text-secondary"
          >
            Upload File
          </button>
          <label class="flex items-center gap-2 text-sm text-text-secondary">
            <input
              type="checkbox"
              checked={forceConflicts.value}
              onChange={(e) => {
                forceConflicts.value = (e.target as HTMLInputElement).checked;
              }}
              class="rounded border-border-primary"
            />
            Force conflicts
          </label>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={handleValidate}
            disabled={isWorking ||
              yamlContent.value === PLACEHOLDER_YAML}
            class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50 text-text-secondary"
          >
            {validating.value ? "Validating..." : "Validate"}
          </button>
          <button
            type="button"
            onClick={handleApply}
            disabled={isWorking ||
              yamlContent.value === PLACEHOLDER_YAML}
            class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {applying.value ? "Applying..." : "Apply"}
          </button>
        </div>
      </div>

      {/* Editor */}
      <div class="rounded-lg border border-border-primary bg-surface">
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
        <div class="flex justify-center py-4">
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
  const borderColor = hasFailed ? "border-warning/30" : "border-success/30";
  const bgColor = hasFailed ? "bg-warning/10" : "bg-success/10";
  const textColor = hasFailed ? "text-warning" : "text-success";

  return (
    <div class={`rounded-md border ${borderColor} ${bgColor} p-4`}>
      <p class={`text-sm font-medium ${textColor}`}>
        {summary.total} resource{summary.total !== 1 ? "s" : ""} processed:{""}
        {summaryParts.join(",")}
      </p>

      {results.length > 0 && (
        <table class="mt-3 w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-text-muted">
                Kind
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-text-muted">
                Name
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-text-muted">
                Namespace
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-text-muted">
                Result
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {results.map((r) => (
              <tr key={`${r.index}-${r.kind}-${r.name}`}>
                <td class="px-2 py-1 text-text-secondary">
                  {r.kind}
                </td>
                <td class="px-2 py-1 text-text-secondary">
                  {r.name}
                </td>
                <td class="px-2 py-1 text-text-muted">
                  {r.namespace || "-"}
                </td>
                <td class="px-2 py-1">
                  {r.action === "failed"
                    ? (
                      <span
                        class="text-danger"
                        title={r.error}
                      >
                        failed: {r.error}
                      </span>
                    )
                    : (
                      <span
                        class={r.action === "created"
                          ? "text-success"
                          : r.action === "configured"
                          ? "text-accent"
                          : "text-text-muted"}
                      >
                        {r.action}
                      </span>
                    )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
