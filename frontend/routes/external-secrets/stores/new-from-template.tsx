import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import SecretStoreFromTemplateEditor from "@/islands/SecretStoreFromTemplateEditor.tsx";
import {
  ESO_YAML_TEMPLATES,
  type ESOTemplate,
} from "@/lib/eso-yaml-templates.ts";
import {
  type SecretStoreProvider,
  TEMPLATE_ONLY_PROVIDERS,
} from "@/lib/eso-types.ts";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function SecretStoreFromTemplatePage(ctx) {
  const url = new URL(ctx.req.url);
  const templateParam = url.searchParams.get("template");

  // ?template=<provider> — render the editor for that provider.
  if (templateParam !== null) {
    return (
      <>
        <SubNav
          tabs={section.tabs ?? []}
          currentPath="/external-secrets/stores"
        />
        <SecretStoreFromTemplateEditor provider={templateParam} />
      </>
    );
  }

  // No query param — render the gallery of all template-only providers.
  const galleryEntries: Array<{
    provider: SecretStoreProvider;
    template: ESOTemplate;
  }> = [];
  for (const p of TEMPLATE_ONLY_PROVIDERS) {
    const tpl = ESO_YAML_TEMPLATES[p];
    if (tpl) galleryEntries.push({ provider: p, template: tpl });
  }
  // Stable display order: alphabetical by display name so the operator's
  // muscle memory is preserved across releases as new templates land.
  galleryEntries.sort((a, b) =>
    a.template.displayName.localeCompare(b.template.displayName)
  );

  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/external-secrets/stores"
      />
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
    </>
  );
});
