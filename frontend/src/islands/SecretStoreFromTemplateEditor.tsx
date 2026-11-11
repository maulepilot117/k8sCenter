import { useEffect } from "react";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { ValidationResults } from "@/components/ui/ValidationResults.tsx";
import type { TemplateOnlyProvider } from "@/lib/eso-types.ts";
import {
  ESO_YAML_TEMPLATES,
  type ESOTemplate,
} from "@/lib/eso-yaml-templates.ts";
import { singleSecretStoreHref } from "@/lib/secretstore-template-nav.ts";
import { type ApplyResponse, useYamlApply } from "@/lib/yaml-apply.ts";
import YamlEditor from "@/src/islands/YamlEditor.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

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
  const template: ESOTemplate | null =
    provider !== null ? ESO_YAML_TEMPLATES[provider] : null;

  const {
    yamlContent: yamlText,
    setYamlContent,
    yamlContentRef,
    applying: applyingNow,
    validating: validatingNow,
    error: errorText,
    result: resultNow,
    preview: previewNow,
    clearPin,
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
      <div className="rounded-md border border-border-primary bg-surface p-6">
        <p className="text-sm text-text-secondary">
          No template selected. Pick a provider from the{" "}
          <a
            href="/external-secrets/stores/new-from-template"
            className="text-accent hover:underline"
          >
            template gallery
          </a>{" "}
          or use the{" "}
          <a
            href="/external-secrets/stores/new"
            className="text-accent hover:underline"
          >
            guided wizard
          </a>{" "}
          for supported providers.
        </p>
      </div>
    );
  }

  const isWorking = applyingNow || validatingNow;

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold text-text-primary">
          Create {template.displayName} SecretStore
        </h1>
        <p className="mt-1 text-sm text-text-muted">{template.notes}</p>
        <p className="mt-2 text-sm text-text-muted">
          Schema reference:{" "}
          <a
            href={template.docsURL}
            target="_blank"
            rel="noopener noreferrer"
            className="text-accent hover:underline"
          >
            {template.docsURL}
          </a>
        </p>
      </header>

      <div className="rounded-md border border-warning/30 bg-warning/10 p-3">
        <p className="text-sm text-text-primary">
          Fill every <code className="font-mono text-xs"># REPLACE:</code>{" "}
          marker before applying. Templates are pre-filled with the field shape
          ESO expects, but the placeholder values will be rejected by the
          cluster API.
        </p>
      </div>

      {errorText && <ErrorBanner message={errorText} />}

      <div className="flex items-center justify-between">
        <a
          href="/external-secrets/stores"
          className="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface"
        >
          Cancel
        </a>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={handleValidate}
            disabled={isWorking}
            className="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
          >
            {validatingNow ? "Validating..." : "Validate"}
          </button>
          <button
            type="button"
            onClick={handleApply}
            disabled={isWorking}
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-base hover:bg-accent/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {applyingNow ? "Applying..." : "Apply"}
          </button>
        </div>
      </div>

      <div className="rounded-lg border border-border-primary bg-surface">
        <YamlEditor
          value={yamlText}
          onChange={(v) => {
            if (v === yamlContentRef.current) return;
            setYamlContent(v);
            // The last verdict described the text before this edit.
            clearPin();
          }}
          readOnly={isWorking}
          height="calc(100vh - 420px)"
        />
      </div>

      {isWorking && (
        <div className="flex justify-center py-4">
          <LoadingSpinner />
        </div>
      )}

      {previewNow && !resultNow && <ValidationResults response={previewNow} />}
      {resultNow && <ApplyResultPanel response={resultNow} />}
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
    <div className={`rounded-md border ${borderColor} ${bgColor} p-4`}>
      <p className={`text-sm font-medium ${textColor}`}>
        {summary.total} resource{summary.total !== 1 ? "s" : ""} processed:{" "}
        {summaryParts.join(", ")}
      </p>
      {results.length > 0 && (
        <ul className="mt-3 space-y-1 text-sm">
          {results.map((r) => (
            <li
              key={`${r.index}-${r.kind}-${r.name}`}
              className="flex items-baseline gap-2"
            >
              <span className="font-mono text-xs text-text-muted">
                {r.kind}
                {r.namespace ? `/${r.namespace}` : ""}/{r.name}
              </span>
              {r.action === "failed" ? (
                <span className="text-danger">failed: {r.error}</span>
              ) : (
                <span
                  className={
                    r.action === "created"
                      ? "text-success"
                      : r.action === "configured"
                        ? "text-accent"
                        : "text-text-muted"
                  }
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
