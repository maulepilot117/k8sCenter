import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Card } from "@/components/ui/Card.tsx";

interface RuleSummary {
  name: string;
  namespace: string;
  rulesCount: number;
  createdAt: string;
  managedBy: string;
}

const DEFAULT_RULE_YAML = `apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kubecenter-example
spec:
  groups:
    - name: example.rules
      rules:
        - alert: HighPodCPU
          expr: |
            sum(rate(container_cpu_usage_seconds_total{container!=""}[5m])) by (pod, namespace) > 0.8
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "High CPU usage on {{ $labels.pod }}"
            description: "Pod {{ $labels.pod }} in {{ $labels.namespace }} is using > 80% CPU for 5 minutes."
`;

export default function AlertRulesPage() {
  const rules = useSignal<RuleSummary[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const editing = useSignal<string | null>(null); // "new" or "{namespace}/{name}"
  const editorContent = useSignal("");
  const editorNamespace = useSignal("default");
  const saving = useSignal(false);

  function fetchRules() {
    loading.value = true;
    apiGet<RuleSummary[]>("/v1/alerts/rules")
      .then((res) => {
        rules.value = res.data ?? [];
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch alert rules";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchRules();
  }, []);

  function handleNew() {
    editing.value = "new";
    editorContent.value = DEFAULT_RULE_YAML;
  }

  function handleEdit(ns: string, name: string) {
    loading.value = true;
    apiGet<Record<string, unknown>>(`/v1/alerts/rules/${ns}/${name}`)
      .then((res) => {
        editorContent.value = JSON.stringify(res.data, null, 2);
        editing.value = `${ns}/${name}`;
        editorNamespace.value = ns;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch rule";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  function handleSave() {
    saving.value = true;
    error.value = null;

    let content: Record<string, unknown>;
    try {
      content = JSON.parse(editorContent.value);
    } catch {
      error.value = "Invalid JSON content";
      saving.value = false;
      return;
    }

    const isNew = editing.value === "new";
    const promise = isNew
      ? apiPost("/v1/alerts/rules", {
        namespace: editorNamespace.value,
        content,
      })
      : apiPut(
        `/v1/alerts/rules/${editing.value}`,
        content,
      );

    promise
      .then(() => {
        editing.value = null;
        fetchRules();
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to save rule";
      })
      .finally(() => {
        saving.value = false;
      });
  }

  function handleDelete(ns: string, name: string) {
    if (!confirm(`Delete PrometheusRule ${ns}/${name}?`)) return;

    apiDelete(`/v1/alerts/rules/${ns}/${name}`)
      .then(() => fetchRules())
      .catch((err) => {
        error.value = err.message ?? "Failed to delete rule";
      });
  }

  if (editing.value) {
    return (
      <Card>
        <div class="space-y-4 p-4">
          <div class="flex items-center justify-between">
            <h2 class="text-lg font-semibold text-slate-900 dark:text-white">
              {editing.value === "new"
                ? "Create Alert Rule"
                : `Edit ${editing.value}`}
            </h2>
            <Button variant="ghost" onClick={() => editing.value = null}>
              Cancel
            </Button>
          </div>

          {editing.value === "new" && (
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-1">
                Namespace
              </label>
              <input
                type="text"
                value={editorNamespace.value}
                onInput={(e) =>
                  editorNamespace.value = (e.target as HTMLInputElement).value}
                class="w-64 px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-md text-sm bg-white dark:bg-slate-800 text-slate-900 dark:text-white"
              />
            </div>
          )}

          {error.value && (
            <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 text-red-700 dark:text-red-400 rounded p-3 text-sm">
              {error.value}
            </div>
          )}

          <textarea
            value={editorContent.value}
            onInput={(e) =>
              editorContent.value = (e.target as HTMLTextAreaElement).value}
            class="w-full h-96 font-mono text-sm px-4 py-3 border border-slate-300 dark:border-slate-600 rounded-md bg-white dark:bg-slate-900 text-slate-900 dark:text-white"
            spellcheck={false}
          />

          <div class="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => editing.value = null}>
              Cancel
            </Button>
            <Button onClick={handleSave} disabled={saving.value}>
              {saving.value ? "Saving..." : "Save"}
            </Button>
          </div>
        </div>
      </Card>
    );
  }

  return (
    <div class="space-y-4">
      <div class="flex justify-end">
        <Button onClick={handleNew}>Create Rule</Button>
      </div>

      {error.value && (
        <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 text-red-700 dark:text-red-400 rounded-lg p-4 text-sm">
          {error.value}
        </div>
      )}

      {loading.value
        ? (
          <div class="text-slate-500 dark:text-slate-400 text-sm py-8 text-center">
            Loading...
          </div>
        )
        : rules.value.length === 0
        ? (
          <div class="text-center py-12 text-slate-500 dark:text-slate-400">
            <p class="text-lg font-medium">No alert rules</p>
            <p class="text-sm mt-1">
              Create PrometheusRule resources to define alerting conditions.
              Requires Prometheus Operator.
            </p>
          </div>
        )
        : (
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-slate-200 dark:divide-slate-700">
              <thead class="bg-slate-50 dark:bg-slate-800">
                <tr>
                  <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                    Name
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                    Namespace
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                    Rules
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                    Created
                  </th>
                  <th class="px-4 py-3 text-right text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-200 dark:divide-slate-700">
                {rules.value.map((rule) => (
                  <tr
                    key={`${rule.namespace}/${rule.name}`}
                    class="hover:bg-slate-50 dark:hover:bg-slate-800/50"
                  >
                    <td class="px-4 py-3 text-sm font-medium text-slate-900 dark:text-white">
                      {rule.name}
                    </td>
                    <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
                      {rule.namespace}
                    </td>
                    <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
                      {rule.rulesCount}
                    </td>
                    <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
                      {new Date(rule.createdAt).toLocaleDateString()}
                    </td>
                    <td class="px-4 py-3 text-right space-x-2">
                      <button
                        type="button"
                        onClick={() => handleEdit(rule.namespace, rule.name)}
                        class="text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 text-sm"
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDelete(rule.namespace, rule.name)}
                        class="text-red-600 hover:text-red-800 dark:text-red-400 dark:hover:text-red-300 text-sm"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
    </div>
  );
}
