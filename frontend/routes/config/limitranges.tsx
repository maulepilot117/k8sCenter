import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function LimitRangesPage() {
  return <ResourceTable kind="limitranges" title="LimitRanges" />;
});
