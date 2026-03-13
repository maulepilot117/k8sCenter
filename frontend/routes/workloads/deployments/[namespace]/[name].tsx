import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function DeploymentDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="deployments"
      title="Deployment"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
