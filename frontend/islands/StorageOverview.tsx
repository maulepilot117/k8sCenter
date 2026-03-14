import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";

interface DriverCapability {
  volumeExpansion: boolean;
  snapshot: boolean;
  clone: boolean;
}

interface DriverInfo {
  name: string;
  attachRequired: boolean;
  podInfoOnMount: boolean;
  volumeLifecycleModes: string[];
  storageCapacity: boolean;
  fsGroupPolicy: string;
  capabilities: DriverCapability;
}

interface ClassInfo {
  name: string;
  provisioner: string;
  reclaimPolicy: string;
  volumeBindingMode: string;
  allowVolumeExpansion: boolean;
  isDefault: boolean;
  parameters: Record<string, string>;
  createdAt: string;
}

export default function StorageOverview() {
  const drivers = useSignal<DriverInfo[]>([]);
  const classes = useSignal<ClassInfo[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    loading.value = true;

    Promise.all([
      apiGet<DriverInfo[]>("/v1/storage/drivers")
        .then((resp) => {
          if (Array.isArray(resp.data)) {
            drivers.value = resp.data;
          }
        }),
      apiGet<ClassInfo[]>("/v1/storage/classes")
        .then((resp) => {
          if (Array.isArray(resp.data)) {
            classes.value = resp.data;
          }
        }),
    ]).catch((err) => {
      error.value = err instanceof Error
        ? err.message
        : "Failed to load storage information";
    }).finally(() => {
      loading.value = false;
    });
  }, []);

  if (!IS_BROWSER) {
    return <div class="p-6">Loading storage overview...</div>;
  }

  if (loading.value) {
    return (
      <div class="p-6">
        <div class="animate-pulse space-y-4">
          <div class="h-8 bg-slate-200 dark:bg-slate-700 rounded w-48" />
          <div class="h-48 bg-slate-200 dark:bg-slate-700 rounded" />
        </div>
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="p-6">
        <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-red-700 dark:text-red-300">
          {error.value}
        </div>
      </div>
    );
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Storage
        </h1>
        <a
          href="/tools/storageclass-wizard"
          class="inline-flex items-center gap-2 px-4 py-2 bg-brand text-white rounded-lg hover:bg-brand/90 text-sm font-medium transition-colors"
        >
          Create StorageClass
        </a>
      </div>

      {/* CSI Drivers */}
      <Card title={`CSI Drivers (${drivers.value.length})`}>
        {drivers.value.length === 0
          ? (
            <p class="text-slate-500 dark:text-slate-400 text-sm py-4 text-center">
              No CSI drivers detected in this cluster.
            </p>
          )
          : (
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-slate-200 dark:border-slate-700">
                    <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                      Driver
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                      Capabilities
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                      Lifecycle Modes
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                      FS Group Policy
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {drivers.value.map((d) => (
                    <tr
                      key={d.name}
                      class="border-b border-slate-100 dark:border-slate-800"
                    >
                      <td class="py-2 px-3 font-mono text-xs text-slate-700 dark:text-slate-300">
                        {d.name}
                      </td>
                      <td class="py-2 px-3">
                        <div class="flex gap-1 flex-wrap">
                          {d.capabilities.volumeExpansion && (
                            <CapBadge label="Expand" />
                          )}
                          {d.capabilities.snapshot && (
                            <CapBadge label="Snapshot" />
                          )}
                          {d.capabilities.clone && <CapBadge label="Clone" />}
                          {d.storageCapacity && <CapBadge label="Capacity" />}
                          {!d.capabilities.volumeExpansion &&
                            !d.capabilities.snapshot &&
                            !d.capabilities.clone && (
                            <span class="text-xs text-slate-400">None</span>
                          )}
                        </div>
                      </td>
                      <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                        {d.volumeLifecycleModes?.join(", ") || "Persistent"}
                      </td>
                      <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                        {d.fsGroupPolicy || "N/A"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
      </Card>

      {/* StorageClasses */}
      <div class="mt-6">
        <Card title={`Storage Classes (${classes.value.length})`}>
          {classes.value.length === 0
            ? (
              <p class="text-slate-500 dark:text-slate-400 text-sm py-4 text-center">
                No storage classes found.
              </p>
            )
            : (
              <div class="overflow-x-auto">
                <table class="w-full text-sm">
                  <thead>
                    <tr class="border-b border-slate-200 dark:border-slate-700">
                      <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                        Name
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                        Provisioner
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                        Reclaim
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                        Binding
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                        Expansion
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {classes.value.map((sc) => (
                      <tr
                        key={sc.name}
                        class="border-b border-slate-100 dark:border-slate-800"
                      >
                        <td class="py-2 px-3">
                          <div class="flex items-center gap-2">
                            <span class="font-mono text-xs text-slate-700 dark:text-slate-300">
                              {sc.name}
                            </span>
                            {sc.isDefault && (
                              <StatusBadge
                                status="Default"
                                variant="info"
                              />
                            )}
                          </div>
                        </td>
                        <td class="py-2 px-3 font-mono text-xs text-slate-600 dark:text-slate-400">
                          {sc.provisioner}
                        </td>
                        <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                          {sc.reclaimPolicy || "Delete"}
                        </td>
                        <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                          {sc.volumeBindingMode || "Immediate"}
                        </td>
                        <td class="py-2 px-3">
                          <StatusBadge
                            status={sc.allowVolumeExpansion ? "Yes" : "No"}
                            variant={sc.allowVolumeExpansion
                              ? "success"
                              : "neutral"}
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
        </Card>
      </div>
    </div>
  );
}

function CapBadge({ label }: { label: string }) {
  return (
    <span class="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
      {label}
    </span>
  );
}
