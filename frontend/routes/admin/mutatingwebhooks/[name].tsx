import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function MutatingWebhookDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="mutatingwebhookconfigurations"
      title="MutatingWebhookConfiguration"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
