import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import ESOExternalSecretDetail from "@/islands/ESOExternalSecretDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ESExternalSecretDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/external-secrets/external-secrets"
      />
      <ESOExternalSecretDetail namespace={namespace} name={name} />
    </>
  );
});
