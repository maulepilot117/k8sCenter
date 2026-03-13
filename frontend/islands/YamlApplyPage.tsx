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
  action: string; // "created" | "configured" | "unchanged" | "failed"
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
#   name: my-config
#   namespace: default
# data:
#   key: value
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
    document.title = "YAML Apply - KubeCenter";
    return () => {
      document.title = "KubeCenter";
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
        <h1 class="text-xl font-semibold text-slate-900 dark:text-white">
          YAML Apply
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
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
            class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
          >
            Upload File
          </button>
          <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-400">
            <input
              type="checkbox"
              checked={forceConflicts.value}
              onChange={(e) => {
                forceConflicts.value = (e.target as HTMLInputElement).checked;
              }}
              class="rounded border-slate-300"
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
            class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
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
      <div class="rounded-lg border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
        <YamlEditor
          value={yamlContent.value}
          onChange={(v) => {
            yamlContent.value = v;
          }}
          readOnly={isWorking}
          height="400px"
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
  const borderColor = hasFailed
    ? "border-amber-200 dark:border-amber-800"
    : "border-green-200 dark:border-green-800";
  const bgColor = hasFailed
    ? "bg-amber-50 dark:bg-amber-900/20"
    : "bg-green-50 dark:bg-green-900/20";
  const textColor = hasFailed
    ? "text-amber-700 dark:text-amber-400"
    : "text-green-700 dark:text-green-400";

  return (
    <div class={`rounded-md border ${borderColor} ${bgColor} p-4`}>
      <p class={`text-sm font-medium ${textColor}`}>
        {summary.total} resource{summary.total !== 1 ? "s" : ""} processed:{" "}
        {summaryParts.join(", ")}
      </p>

      {results.length > 0 && (
        <table class="mt-3 w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-slate-500">
                Kind
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-slate-500">
                Name
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-slate-500">
                Namespace
              </th>
              <th class="px-2 py-1 text-left text-xs font-medium uppercase text-slate-500">
                Result
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
            {results.map((r) => (
              <tr key={`${r.index}-${r.kind}-${r.name}`}>
                <td class="px-2 py-1 text-slate-700 dark:text-slate-300">
                  {r.kind}
                </td>
                <td class="px-2 py-1 text-slate-700 dark:text-slate-300">
                  {r.name}
                </td>
                <td class="px-2 py-1 text-slate-500 dark:text-slate-400">
                  {r.namespace || "-"}
                </td>
                <td class="px-2 py-1">
                  {r.action === "failed"
                    ? (
                      <span
                        class="text-red-600 dark:text-red-400"
                        title={r.error}
                      >
                        failed: {r.error}
                      </span>
                    )
                    : (
                      <span
                        class={r.action === "created"
                          ? "text-green-600 dark:text-green-400"
                          : r.action === "configured"
                          ? "text-blue-600 dark:text-blue-400"
                          : "text-slate-500 dark:text-slate-400"}
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
