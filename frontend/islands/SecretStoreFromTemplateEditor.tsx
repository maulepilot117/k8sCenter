import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import YamlEditor from "@/islands/YamlEditor.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import {
  ESO_YAML_TEMPLATES,
  type ESOTemplate,
} from "@/lib/eso-yaml-templates.ts";
import type { TemplateOnlyProvider } from "@/lib/eso-types.ts";
import { type ApplyResponse, useYamlApply } from "@/lib/yaml-apply.ts";
import { singleSecretStoreHref } from "@/lib/secretstore-template-nav.ts";

interface Props {
  /**
   * Validated provider key, or null when the URL `?template=` query param was
   * absent or did not match a known template. The route narrows the boundary
   * string via `isTemplateOnlyProvider` before passing it down, so the island
   * receives a precisely-typed value.
   */
  provider: TemplateOnlyProvider | null;
}

export default function SecretStoreFromTemplateEditor({ provider }: Props) {
  // Registry is total over TemplateOnlyProvider, so the lookup is null only
  // when the route already classified the URL as unknown.
  const template: ESOTemplate | null = provider !== null
    ? ESO_YAML_TEMPLATES[provider]
    : null;

  const {
    yamlContent,
    applying,
    validating,
    error,
    result,
    handleValidate,
    handleApply,
  } = useYamlApply(template?.yaml ?? "", {
    onApplySuccess: (res) => navigateOnSingleSecretStore(res),
  });

  useEffect(() => {
    if (!IS_BROWSER) return;
    document.title = template
      ? `Create ${template.displayName} SecretStore - k8sCenter`
      : "Create SecretStore from template - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, [template]);

  if (!template) {
    return (
      <div class="rounded-md border border-border-primary bg-surface p-6">
        <p class="text-sm text-text-secondary">
          No template selected. Pick a provider from the{" "}
          <a
            href="/external-secrets/stores/new-from-template"
            class="text-accent hover:underline"
          >
            template gallery
          </a>{" "}
          or use the{" "}
          <a
            href="/external-secrets/stores/new"
            class="text-accent hover:underline"
          >
            guided wizard
          </a>{" "}
          for supported providers.
        </p>
      </div>
    );
  }

  const isWorking = applying.value || validating.value;

  return (
    <div class="space-y-4">
      <header>
        <h1 class="text-xl font-semibold text-text-primary">
          Create {template.displayName} SecretStore
        </h1>
        <p class="mt-1 text-sm text-text-muted">{template.notes}</p>
        <p class="mt-2 text-sm text-text-muted">
          Schema reference:{" "}
          <a
            href={template.docsURL}
            target="_blank"
            rel="noopener noreferrer"
            class="text-accent hover:underline"
          >
            {template.docsURL}
          </a>
        </p>
      </header>

      <div class="rounded-md border border-warning/30 bg-warning/10 p-3">
        <p class="text-sm text-text-primary">
          Fill every <code class="font-mono text-xs"># REPLACE:</code>{" "}
          marker before applying. Templates are pre-filled with the field shape
          ESO expects, but the placeholder values will be rejected by the
          cluster API.
        </p>
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      <div class="flex items-center justify-between">
        <a
          href="/external-secrets/stores"
          class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface"
        >
          Cancel
        </a>
        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={handleValidate}
            disabled={isWorking}
            class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
          >
            {validating.value ? "Validating..." : "Validate"}
          </button>
          <button
            type="button"
            onClick={handleApply}
            disabled={isWorking}
            class="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-base hover:bg-accent/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {applying.value ? "Applying..." : "Apply"}
          </button>
        </div>
      </div>

      <div class="rounded-lg border border-border-primary bg-surface">
        <YamlEditor
          value={yamlContent.value}
          onChange={(v) => {
            yamlContent.value = v;
          }}
          readOnly={isWorking}
          height="calc(100vh - 420px)"
        />
      </div>

      {(applying.value || validating.value) && (
        <div class="flex justify-center py-4">
          <LoadingSpinner />
        </div>
      )}

      {result.value && <ApplyResultPanel response={result.value} />}
    </div>
  );
}

/**
 * Side-effecting wrapper around `singleSecretStoreHref`. Bound to the
 * `useYamlApply` hook's `onApplySuccess` callback in the editor render.
 * The pure decision lives in `lib/secretstore-template-nav.ts` so the unit
 * tests can exercise every branch without React-DOM or Fresh-runtime types.
 */
function navigateOnSingleSecretStore(res: ApplyResponse): void {
  if (!IS_BROWSER) return;
  const href = singleSecretStoreHref(res);
  if (href !== null) {
    globalThis.location.href = href;
  }
}

function ApplyResultPanel({ response }: { response: ApplyResponse }) {
  const { summary, results } = response;
  const hasFailed = summary.failed > 0;
  const borderColor = hasFailed ? "border-warning/30" : "border-success/30";
  const bgColor = hasFailed ? "bg-warning/10" : "bg-success/10";
  const textColor = hasFailed ? "text-warning" : "text-success";

  const summaryParts: string[] = [];
  if (summary.created > 0) summaryParts.push(`${summary.created} created`);
  if (summary.configured > 0) {
    summaryParts.push(`${summary.configured} configured`);
  }
  if (summary.unchanged > 0) {
    summaryParts.push(`${summary.unchanged} unchanged`);
  }
  if (summary.failed > 0) summaryParts.push(`${summary.failed} failed`);

  return (
    <div class={`rounded-md border ${borderColor} ${bgColor} p-4`}>
      <p class={`text-sm font-medium ${textColor}`}>
        {summary.total} resource{summary.total !== 1 ? "s" : ""} processed:{" "}
        {summaryParts.join(", ")}
      </p>
      {results.length > 0 && (
        <ul class="mt-3 space-y-1 text-sm">
          {results.map((r) => (
            <li
              key={`${r.index}-${r.kind}-${r.name}`}
              class="flex items-baseline gap-2"
            >
              <span class="font-mono text-xs text-text-muted">
                {r.kind}
                {r.namespace ? `/${r.namespace}` : ""}/{r.name}
              </span>
              {r.action === "failed"
                ? <span class="text-danger">failed: {r.error}</span>
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
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
