import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ReplicaSetsPage() {
  return <ResourceTable kind="replicasets" title="ReplicaSets" />;
});
