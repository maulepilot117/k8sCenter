import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";

export default define.page(function ToolsPage(ctx) {
  const section = DOMAIN_SECTIONS.find((s) => s.id === "tools")!;
  return (
    <div class="flex flex-col h-full">
      <div class="px-5 pt-4 pb-3">
        <h1 class="text-xl font-semibold tracking-tight text-text-primary">
          Tools
        </h1>
        <p class="text-xs text-text-muted mt-0.5">
          YAML tools, wizards, and cluster utilities
        </p>
      </div>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div class="flex-1 flex items-center justify-center px-5 py-10">
        <div class="text-center">
          <p class="text-sm text-text-muted mb-4">
            Select a tool from the tabs above to get started.
          </p>
          <div class="flex gap-3 justify-center">
            <a
              href="/tools/yaml-apply"
              class="px-4 py-2 text-xs font-medium text-white bg-accent rounded-md no-underline"
            >
              YAML Apply
            </a>
            <a
              href="/tools/storageclass-wizard"
              class="px-4 py-2 text-xs font-medium text-text-primary bg-bg-elevated rounded-md no-underline border border-border-subtle"
            >
              StorageClass Wizard
            </a>
          </div>
        </div>
      </div>
    </div>
  );
});
