import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { SecretStore } from "@/lib/eso-types.ts";

export default function ESOClusterStoresList() {
  const items = useSignal<SecretStore[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");

  async function fetchData() {
    try {
      const res = await esoApi.listClusterStores();
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      error.value = "Failed to load ClusterSecretStores";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  if (!IS_BROWSER) return null;

  const filtered = items.value.filter((s) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
      (s.provider ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <div class="p-6">
      <div class="flex items-start justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">
          ClusterSecretStores
        </h1>
      </div>
      <p class="text-sm text-text-muted mb-6">
        Cluster-scoped SecretStores accessible to ExternalSecrets in any
        namespace.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-cstores-search"
          >
            Search
          </label>
          <input
            id="eso-cstores-search"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-bg-base text-text-primary max-w-xs"
            placeholder="name, provider..."
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {items.value.length} ClusterSecretStores
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {!loading.value && error.value && (
        <p class="text-sm text-danger py-4">{error.value}</p>
      )}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Name
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Status
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Provider
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Ready
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {filtered.map((s) => (
                <tr key={s.uid} class="hover:bg-hover/30">
                  <td class="px-3 py-2">
                    <a
                      href={`/external-secrets/cluster-stores/${s.name}`}
                      class="font-medium text-brand hover:underline"
                    >
                      {s.name}
                    </a>
                  </td>
                  <td class="px-3 py-2">
                    <StatusBadge status={s.status} />
                  </td>
                  <td class="px-3 py-2">
                    <ProviderBadge provider={s.provider} />
                  </td>
                  <td class="px-3 py-2">
                    {s.ready
                      ? (
                        <span class="text-success text-xs font-medium">
                          Ready
                        </span>
                      )
                      : (
                        <span class="text-danger text-xs font-medium">
                          Not Ready
                        </span>
                      )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!loading.value && !error.value && filtered.length === 0 &&
        items.value.length > 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            No ClusterSecretStores match your filters.
          </p>
        </div>
      )}

      {!loading.value && !error.value && items.value.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            No ClusterSecretStores visible. Your permissions may restrict
            visibility, or no ClusterSecretStores exist.
          </p>
        </div>
      )}
    </div>
  );
}
