import { define } from "@/utils.ts";
import VulnerabilityDetail from "@/islands/VulnerabilityDetail.tsx";

export default define.page(function VulnerabilityDetailPage(ctx) {
  const { namespace, kind, name } = ctx.params;
  return <VulnerabilityDetail namespace={namespace} kind={kind} name={name} />;
});
