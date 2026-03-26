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
          <div class="h-8 bg-elevated rounded w-48" />
          <div class="h-48 bg-elevated rounded" />
        </div>
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="p-6">
        <div class="bg-danger-dim border border-danger rounded-lg p-4 text-danger">
          {error.value}
        </div>
      </div>
    );
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
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
            <p class="text-text-muted text-sm py-4 text-center">
              No CSI drivers detected in this cluster.
            </p>
          )
          : (
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary">
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Driver
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Capabilities
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Lifecycle Modes
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      FS Group Policy
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {drivers.value.map((d) => (
                    <tr
                      key={d.name}
                      class="border-b border-border-subtle"
                    >
                      <td class="py-2 px-3 font-mono text-xs text-text-secondary">
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
                            <span class="text-xs text-text-muted">None</span>
                          )}
                        </div>
                      </td>
                      <td class="py-2 px-3 text-xs text-text-secondary">
                        {d.volumeLifecycleModes?.join(",") || "Persistent"}
                      </td>
                      <td class="py-2 px-3 text-xs text-text-secondary">
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
              <p class="text-text-muted text-sm py-4 text-center">
                No storage classes found.
              </p>
            )
            : (
              <div class="overflow-x-auto">
                <table class="w-full text-sm">
                  <thead>
                    <tr class="border-b border-border-primary">
                      <th class="text-left py-2 px-3 font-medium text-text-muted">
                        Name
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-text-muted">
                        Provisioner
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-text-muted">
                        Reclaim
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-text-muted">
                        Binding
                      </th>
                      <th class="text-left py-2 px-3 font-medium text-text-muted">
                        Expansion
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {classes.value.map((sc) => (
                      <tr
                        key={sc.name}
                        class="border-b border-border-subtle"
                      >
                        <td class="py-2 px-3">
                          <div class="flex items-center gap-2">
                            <span class="font-mono text-xs text-text-secondary">
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
                        <td class="py-2 px-3 font-mono text-xs text-text-secondary">
                          {sc.provisioner}
                        </td>
                        <td class="py-2 px-3 text-xs text-text-secondary">
                          {sc.reclaimPolicy || "Delete"}
                        </td>
                        <td class="py-2 px-3 text-xs text-text-secondary">
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
    <span class="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-accent-dim text-accent">
      {label}
    </span>
  );
}
