import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ESOPushSecretDetail from "@/islands/ESOPushSecretDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ESPushSecretDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/external-secrets/push-secrets"
      />
      <ESOPushSecretDetail namespace={namespace} name={name} />
    </>
  );
});
