import { define } from "@/utils.ts";
import SecretStoreFromTemplateEditor from "@/islands/SecretStoreFromTemplateEditor.tsx";
import {
  ESO_YAML_TEMPLATES,
  type ESOTemplate,
} from "@/lib/eso-yaml-templates.ts";
import {
  isTemplateOnlyProvider,
  type TemplateOnlyProvider,
} from "@/lib/eso-types.ts";

export default define.page(function SecretStoreFromTemplatePage(ctx) {
  const url = new URL(ctx.req.url);
  const templateParam = url.searchParams.get("template");

  // ?template=<provider> — narrow the URL string to TemplateOnlyProvider at
  // the boundary so the island sees a precise type. The island still handles
  // an unknown key gracefully (empty state with a link to the gallery).
  if (templateParam !== null) {
    const provider = isTemplateOnlyProvider(templateParam)
      ? templateParam
      : null;
    return <SecretStoreFromTemplateEditor provider={provider} />;
  }

  // No query param — render the gallery of all template-only providers.
  // The registry is a total Record over TemplateOnlyProvider, so iteration
  // produces every key without runtime null checks.
  const galleryEntries: Array<{
    provider: TemplateOnlyProvider;
    template: ESOTemplate;
  }> = (Object.keys(ESO_YAML_TEMPLATES) as TemplateOnlyProvider[]).map((p) => ({
    provider: p,
    template: ESO_YAML_TEMPLATES[p],
  }));
  // Stable display order: alphabetical by display name so the operator's
  // muscle memory is preserved across releases as new templates land.
  galleryEntries.sort((a, b) =>
    a.template.displayName.localeCompare(b.template.displayName)
  );

  return (
    <div class="space-y-4">
      <header>
        <h1 class="text-xl font-semibold text-text-primary">
          Create SecretStore from template
        </h1>
        <p class="mt-1 text-sm text-text-muted">
          Pick a provider to open a pre-filled YAML template you can edit and
          apply. For providers with a guided wizard, use the{" "}
          <a
            href="/external-secrets/stores/new"
            class="text-accent hover:underline"
          >
            Create wizard
          </a>{" "}
          instead.
        </p>
      </header>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {galleryEntries.map(({ provider, template }) => (
          <a
            key={provider}
            href={`/external-secrets/stores/new-from-template?template=${provider}`}
            class="block rounded-lg border border-border-primary bg-surface p-4 transition-colors hover:border-border-emphasis"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium text-text-primary">
                {template.displayName}
              </span>
              <span class="text-xs font-medium text-text-muted whitespace-nowrap">
                template
              </span>
            </div>
            <p class="mt-2 text-sm text-text-muted">{template.notes}</p>
          </a>
        ))}
      </div>
    </div>
  );
});
