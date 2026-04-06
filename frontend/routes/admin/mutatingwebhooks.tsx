import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function MutatingWebhooksPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <ResourceTable
        kind="mutatingwebhookconfigurations"
        title="MutatingWebhookConfigurations"
        clusterScoped
      />
    </>
  );
});
