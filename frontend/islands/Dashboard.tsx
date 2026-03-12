import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { Card } from "@/components/ui/Card.tsx";

interface ClusterInfoData {
  clusterID: string;
  kubernetesVersion: string;
  platform: string;
  nodeCount: number;
  kubecenter: {
    version: string;
    commit: string;
    buildDate: string;
  };
}

interface ResourceCounts {
  deployments: number;
  pods: number;
  services: number;
  namespaces: number;
}

export default function Dashboard() {
  const { fetchCurrentUser } = useAuth();
  const clusterInfo = useSignal<ClusterInfoData | null>(null);
  const counts = useSignal<ResourceCounts>({
    deployments: 0,
    pods: 0,
    services: 0,
    namespaces: 0,
  });
  const loading = useSignal(true);
  const error = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function load() {
      loading.value = true;
      error.value = "";

      try {
        // Fetch auth and cluster data in parallel (no waterfall)
        const [, infoRes, deplRes, podRes, svcRes, nsRes] = await Promise
          .allSettled([
            fetchCurrentUser(),
            apiGet<ClusterInfoData>("/v1/cluster/info"),
            apiGet<unknown>("/v1/resources/deployments?limit=1"),
            apiGet<unknown>("/v1/resources/pods?limit=1"),
            apiGet<unknown>("/v1/resources/services?limit=1"),
            apiGet<unknown>("/v1/resources/namespaces?limit=1"),
          ]);

        if (infoRes.status === "fulfilled") {
          clusterInfo.value = infoRes.value.data;
        }

        counts.value = {
          deployments: deplRes.status === "fulfilled"
            ? deplRes.value.metadata?.total ?? 0
            : 0,
          pods: podRes.status === "fulfilled"
            ? podRes.value.metadata?.total ?? 0
            : 0,
          services: svcRes.status === "fulfilled"
            ? svcRes.value.metadata?.total ?? 0
            : 0,
          namespaces: nsRes.status === "fulfilled"
            ? nsRes.value.metadata?.total ?? 0
            : 0,
        };
      } catch {
        error.value = "Failed to load cluster information";
      } finally {
        loading.value = false;
      }
    }

    load();
  }, []);

  if (loading.value) {
    return (
      <div class="animate-pulse space-y-6">
        <div class="h-8 w-48 rounded bg-slate-200 dark:bg-slate-700" />
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <div
              key={i}
              class="h-28 rounded-lg bg-slate-200 dark:bg-slate-700"
            />
          ))}
        </div>
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/30 dark:text-red-400">
        {error.value}
      </div>
    );
  }

  const statCards = [
    {
      label: "Nodes",
      value: clusterInfo.value?.nodeCount ?? 0,
      icon: "\u26C1",
      href: "/cluster/nodes",
    },
    {
      label: "Namespaces",
      value: counts.value.namespaces,
      icon: "\u25A3",
      href: "/cluster/namespaces",
    },
    {
      label: "Deployments",
      value: counts.value.deployments,
      icon: "\u25CE",
      href: "/workloads/deployments",
    },
    {
      label: "Pods",
      value: counts.value.pods,
      icon: "\u2B22",
      href: "/workloads/pods",
    },
    {
      label: "Services",
      value: counts.value.services,
      icon: "\u29BF",
      href: "/networking/services",
    },
  ];

  return (
    <div>
      <div class="mb-6">
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Cluster Overview
        </h1>
        {clusterInfo.value && (
          <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Kubernetes {clusterInfo.value.kubernetesVersion} on{" "}
            {clusterInfo.value.platform}
          </p>
        )}
      </div>

      {/* Stat cards */}
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
        {statCards.map((stat) => (
          <a key={stat.label} href={stat.href} class="group">
            <Card class="transition-shadow group-hover:shadow-md">
              <div class="flex items-center justify-between">
                <div>
                  <p class="text-sm font-medium text-slate-500 dark:text-slate-400">
                    {stat.label}
                  </p>
                  <p class="mt-1 text-3xl font-bold text-slate-900 dark:text-white">
                    {stat.value}
                  </p>
                </div>
                <span class="text-3xl text-slate-300 dark:text-slate-600">
                  {stat.icon}
                </span>
              </div>
            </Card>
          </a>
        ))}
      </div>

      {/* Cluster details */}
      {clusterInfo.value && (
        <div class="mt-8">
          <Card title="Cluster Details">
            <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <div>
                <dt class="text-sm text-slate-500 dark:text-slate-400">
                  Cluster ID
                </dt>
                <dd class="mt-1 text-sm font-medium text-slate-900 dark:text-white">
                  {clusterInfo.value.clusterID}
                </dd>
              </div>
              <div>
                <dt class="text-sm text-slate-500 dark:text-slate-400">
                  Kubernetes Version
                </dt>
                <dd class="mt-1 text-sm font-medium text-slate-900 dark:text-white">
                  {clusterInfo.value.kubernetesVersion}
                </dd>
              </div>
              <div>
                <dt class="text-sm text-slate-500 dark:text-slate-400">
                  KubeCenter Version
                </dt>
                <dd class="mt-1 text-sm font-medium text-slate-900 dark:text-white">
                  {clusterInfo.value.kubecenter?.version ?? "dev"}
                </dd>
              </div>
            </dl>
          </Card>
        </div>
      )}
    </div>
  );
}
