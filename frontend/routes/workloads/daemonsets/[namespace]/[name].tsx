import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function DaemonSetDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="daemonsets"
      title="DaemonSet"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
