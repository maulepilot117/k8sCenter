import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import ESOPushSecretsList from "@/islands/ESOPushSecretsList.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function PushSecretsPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <ESOPushSecretsList />
    </>
  );
});
