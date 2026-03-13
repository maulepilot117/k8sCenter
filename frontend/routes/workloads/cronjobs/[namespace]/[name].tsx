import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function CronJobDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="cronjobs"
      title="CronJob"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
