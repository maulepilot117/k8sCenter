import { define } from "@/utils.ts";
import NamespaceTopology from "@/islands/NamespaceTopology.tsx";

export default define.page(function TopologyPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Resource Topology
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          Dependency graph showing resource relationships and health
        </p>
      </div>
      <NamespaceTopology />
    </div>
  );
});
