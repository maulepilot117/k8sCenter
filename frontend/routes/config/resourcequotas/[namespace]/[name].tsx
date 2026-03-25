import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ResourceQuotaDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="resourcequotas"
      title="ResourceQuota"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
