import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ResourceQuotasPage() {
  return <ResourceTable kind="resourcequotas" title="ResourceQuotas" />;
});
