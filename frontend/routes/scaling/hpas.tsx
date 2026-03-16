import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function HPAsPage() {
  return <ResourceTable kind="hpas" title="HorizontalPodAutoscalers" />;
});
