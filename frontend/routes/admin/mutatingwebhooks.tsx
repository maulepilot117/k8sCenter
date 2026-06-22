import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function MutatingWebhooksPage() {
  return (
    <ResourceTable
      kind="mutatingwebhookconfigurations"
      title="MutatingWebhookConfigurations"
      clusterScoped
    />
  );
});
