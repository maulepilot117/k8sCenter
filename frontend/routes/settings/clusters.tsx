import { define } from "@/utils.ts";
import ClusterManager from "@/islands/ClusterManager.tsx";

export default define.page(function ClustersPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Clusters
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Manage registered Kubernetes clusters.
        </p>
      </div>
      <ClusterManager />
    </div>
  );
});
