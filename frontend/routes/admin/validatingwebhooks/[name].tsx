import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ValidatingWebhookDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="validatingwebhookconfigurations"
      title="ValidatingWebhookConfiguration"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
