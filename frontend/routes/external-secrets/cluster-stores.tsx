import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import ESOClusterStoresList from "@/islands/ESOClusterStoresList.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ClusterStoresPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <ESOClusterStoresList />
    </>
  );
});
