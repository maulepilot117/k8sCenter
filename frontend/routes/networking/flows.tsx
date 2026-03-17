import { define } from "@/utils.ts";
import FlowViewer from "@/islands/FlowViewer.tsx";

export default define.page(function NetworkFlowsPage() {
  return <FlowViewer />;
});
