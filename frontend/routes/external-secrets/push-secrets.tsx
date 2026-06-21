import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ESOPushSecretsList from "@/islands/ESOPushSecretsList.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function PushSecretsPage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <ESOPushSecretsList />
    </>
  );
});
