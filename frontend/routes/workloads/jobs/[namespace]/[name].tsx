import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function JobDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="jobs"
      title="Job"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
