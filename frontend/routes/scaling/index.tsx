import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ScalingPage() {
  return (
    <ResourceTable
      kind="horizontalpodautoscalers"
      title="HorizontalPodAutoscalers"
      createHref="/scaling/hpas/new"
    />
  );
});
