import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { SecretStore } from "@/lib/eso-types.ts";

const NAMESPACE_DEBOUNCE_MS = 300;

export default function ESOStoresList() {
  const items = useSignal<SecretStore[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const namespace = useSignal("");
  const search = useSignal("");
  const detected = useSignal<boolean | null>(null);

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<number | null>(null);

  async function fetchData() {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespace.value.trim() || undefined;
      const res = await esoApi.listStores(ns);
      if (seq !== fetchSeq.current) return;
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      if (seq !== fetchSeq.current) return;
      error.value = "Failed to load SecretStores";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    (async () => {
      try {
        const statusRes = await esoApi.status();
        const present = statusRes.data?.detected !== false;
        detected.value = present;
        if (present) await fetchData();
      } catch {
        detected.value = true;
        await fetchData();
      } finally {
        loading.value = false;
      }
    })();
    return () => {
      if (debounceHandle.current !== null) {
        clearTimeout(debounceHandle.current);
        debounceHandle.current = null;
      }
    };
  }, []);

  function handleNamespaceChange(value: string) {
    namespace.value = value;
    if (debounceHandle.current !== null) {
      clearTimeout(debounceHandle.current);
    }
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;
      fetchData();
    }, NAMESPACE_DEBOUNCE_MS);
  }

  if (!IS_BROWSER) return null;

  if (!loading.value && detected.value === false) {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-bold text-text-primary mb-6">SecretStores</h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.value.filter((s) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
      (s.namespace ?? "").toLowerCase().includes(q) ||
      (s.provider ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <div class="p-6">
      <div class="flex items-start justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">SecretStores</h1>
      </div>
      <p class="text-sm text-text-muted mb-6">
        Namespaced SecretStores describe how ESO talks to a secret backend.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-stores-ns"
          >
            Namespace
          </label>
          <input
            id="eso-stores-ns"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary max-w-xs"
            placeholder="All namespaces"
            value={namespace.value}
            aria-describedby="eso-stores-ns-hint"
            onInput={(e) =>
              handleNamespaceChange((e.target as HTMLInputElement).value)}
          />
          <span id="eso-stores-ns-hint" class="sr-only">
            Filter SecretStores by namespace; updates after a brief pause.
          </span>
        </div>
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-stores-search"
          >
            Search
          </label>
          <input
            id="eso-stores-search"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary max-w-xs"
            placeholder="name, provider..."
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {items.value.length} SecretStores
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
                  Namespace
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
                    {s.namespace
                      ? (
                        <a
                          href={`/external-secrets/stores/${
                            encodeURIComponent(s.namespace)
                          }/${encodeURIComponent(s.name)}`}
                          class="font-medium text-brand hover:underline"
                        >
                          {s.name}
                        </a>
                      )
                      : (
                        // Namespaced list shouldn't return scope=Cluster, but
                        // type allows it. Render plain text rather than a link
                        // to a route that requires a namespace segment.
                        <span class="font-medium text-text-primary">
                          {s.name}
                        </span>
                      )}
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {s.namespace ?? "—"}
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
        <div class="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p class="text-text-muted">No SecretStores match your filters.</p>
        </div>
      )}

      {!loading.value && !error.value && items.value.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p class="text-text-muted">
            No SecretStores in this namespace. ExternalSecrets require a
            SecretStore to function.
          </p>
        </div>
      )}
    </div>
  );
}
