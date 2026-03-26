import { define } from "@/utils.ts";
import PromQLQuery from "@/islands/PromQLQuery.tsx";

export default define.page(function PrometheusPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Prometheus Query
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          Run PromQL queries against the cluster's Prometheus instance
        </p>
      </div>
      <PromQLQuery />
    </div>
  );
});
