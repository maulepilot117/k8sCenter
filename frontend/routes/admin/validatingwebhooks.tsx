import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ValidatingWebhooksPage() {
  return (
    <ResourceTable
      kind="validatingwebhookconfigurations"
      title="ValidatingWebhookConfigurations"
      clusterScoped
    />
  );
});
