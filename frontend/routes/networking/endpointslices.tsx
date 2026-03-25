import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function EndpointSlicesPage() {
  return <ResourceTable kind="endpointslices" title="EndpointSlices" />;
});
