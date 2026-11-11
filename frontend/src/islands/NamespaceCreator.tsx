import { type FormEvent, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { apiPost } from "@/lib/api.ts";
import { NS_NAME_REGEX } from "@/lib/wizard-constants.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const NAMESPACE_LIST = "/cluster/namespaces";

/** One root class for the SSR placeholder and the form: hydration keeps the
 * server-rendered root's props, so a root that differed would keep the
 * placeholder's classes. */
const ROOT_CLASS = "p-6 max-w-lg";

/** Where a successful create goes: the new namespace's detail page. */
export function createdNamespaceHref(name: string): string {
  return `${NAMESPACE_LIST}/${encodeURIComponent(name)}`;
}

export default function NamespaceCreator() {
  const isClient = useIsClient();
  const [name, setName] = useState("");
  const [labels, setLabels] = useState<Array<{ key: string; value: string }>>([
    { key: "", value: "" },
  ]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!isClient) {
    return (
      <div className={ROOT_CLASS}>
        <h1 className="text-2xl font-semibold text-text-primary">
          Create Namespace
        </h1>
      </div>
    );
  }

  const validate = (): string | null => {
    if (!name.trim()) return "Name is required";
    if (!NS_NAME_REGEX.test(name)) {
      return "Must be lowercase alphanumeric with dashes, max 63 chars";
    }
    if (name.startsWith("kube-")) {
      return 'Names starting with "kube-" are reserved for Kubernetes system namespaces';
    }
    return null;
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const err = validate();
    if (err) {
      setError(err);
      return;
    }

    setSubmitting(true);
    try {
      const nsLabels: Record<string, string> = {};
      for (const l of labels) {
        if (l.key.trim()) nsLabels[l.key.trim()] = l.value.trim();
      }

      await apiPost("/v1/resources/namespaces", {
        metadata: {
          name,
          labels: Object.keys(nsLabels).length > 0 ? nsLabels : undefined,
        },
      });
      globalThis.location.href = createdNamespaceHref(name);
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to create namespace";
      setError(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const updateLabel = (index: number, field: "key" | "value", val: string) => {
    setLabels((prev) =>
      prev.map((l, i) => (i === index ? { ...l, [field]: val } : l)),
    );
  };

  const addLabel = () => {
    setLabels((prev) => [...prev, { key: "", value: "" }]);
  };

  const removeLabel = (index: number) => {
    setLabels((prev) => prev.filter((_, i) => i !== index));
  };

  return (
    <div className={ROOT_CLASS}>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-text-primary">
          Create Namespace
        </h1>
        <a
          href={NAMESPACE_LIST}
          className="text-sm text-text-muted hover:text-text-secondary"
        >
          Back to list
        </a>
      </div>

      {error && (
        <div className="mb-4 rounded-md bg-danger-dim p-4 border border-danger">
          <p className="text-sm text-danger">{error}</p>
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-6">
        <Input
          label="Namespace Name"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          placeholder="my-namespace"
          required
        />

        <KeyValueListEditor
          label="Labels (optional)"
          entries={labels}
          onUpdate={updateLabel}
          onAdd={addLabel}
          onRemove={removeLabel}
          addLabel="+ Add Label"
        />

        <div className="flex items-center gap-4">
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? "Creating..." : "Create Namespace"}
          </Button>
          <button
            type="button"
            onClick={() => globalThis.history.back()}
            className="text-sm text-text-muted hover:text-text-secondary"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
