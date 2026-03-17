import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { NS_NAME_REGEX } from "@/lib/wizard-constants.ts";

export default function NamespaceCreator() {
  const name = useSignal("");
  const labels = useSignal<Array<{ key: string; value: string }>>([
    { key: "", value: "" },
  ]);
  const submitting = useSignal(false);
  const error = useSignal<string | null>(null);

  if (!IS_BROWSER) {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-white">
          Create Namespace
        </h1>
      </div>
    );
  }

  const validate = (): string | null => {
    if (!name.value.trim()) return "Name is required";
    if (!NS_NAME_REGEX.test(name.value)) {
      return "Must be lowercase alphanumeric with dashes, max 63 chars";
    }
    if (name.value.startsWith("kube-")) {
      return 'Names starting with "kube-" are reserved for Kubernetes system namespaces';
    }
    return null;
  };

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    error.value = null;
    const err = validate();
    if (err) {
      error.value = err;
      return;
    }

    submitting.value = true;
    try {
      const nsLabels: Record<string, string> = {};
      for (const l of labels.value) {
        if (l.key.trim()) nsLabels[l.key.trim()] = l.value.trim();
      }

      await apiPost("/v1/resources/namespaces", {
        metadata: {
          name: name.value,
          labels: Object.keys(nsLabels).length > 0 ? nsLabels : undefined,
        },
      });
      globalThis.location.href = "/cluster/namespaces";
    } catch (err: unknown) {
      const msg = err instanceof Error
        ? err.message
        : "Failed to create namespace";
      error.value = msg;
    } finally {
      submitting.value = false;
    }
  };

  const updateLabel = (index: number, field: "key" | "value", val: string) => {
    const updated = [...labels.value];
    updated[index] = { ...updated[index], [field]: val };
    labels.value = updated;
  };

  const addLabel = () => {
    labels.value = [...labels.value, { key: "", value: "" }];
  };

  const removeLabel = (index: number) => {
    labels.value = labels.value.filter((_, i) => i !== index);
  };

  return (
    <div class="p-6 max-w-lg">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-white">
          Create Namespace
        </h1>
        <a
          href="/cluster/namespaces"
          class="text-sm text-slate-500 hover:text-slate-700 dark:text-slate-400"
        >
          Back to list
        </a>
      </div>

      {error.value && (
        <div class="mb-4 rounded-md bg-red-50 dark:bg-red-900/20 p-4 border border-red-200 dark:border-red-800">
          <p class="text-sm text-red-800 dark:text-red-200">{error.value}</p>
        </div>
      )}

      <form onSubmit={handleSubmit} class="space-y-6">
        <Input
          label="Namespace Name"
          value={name.value}
          onInput={(e) => name.value = (e.target as HTMLInputElement).value}
          placeholder="my-namespace"
          required
        />

        <KeyValueListEditor
          label="Labels (optional)"
          entries={labels.value}
          onUpdate={updateLabel}
          onAdd={addLabel}
          onRemove={removeLabel}
          addLabel="+ Add Label"
        />

        <div class="flex items-center gap-4">
          <Button
            type="submit"
            variant="primary"
            disabled={submitting.value}
          >
            {submitting.value ? "Creating..." : "Create Namespace"}
          </Button>
          <a
            href="/cluster/namespaces"
            class="text-sm text-slate-500 hover:text-slate-700 dark:text-slate-400"
          >
            Cancel
          </a>
        </div>
      </form>
    </div>
  );
}
