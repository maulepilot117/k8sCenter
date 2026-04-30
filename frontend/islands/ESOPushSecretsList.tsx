import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { PushSecret } from "@/lib/eso-types.ts";

const NAMESPACE_DEBOUNCE_MS = 300;

export default function ESOPushSecretsList() {
  const items = useSignal<PushSecret[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const namespace = useSignal("");
  const search = useSignal("");

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<number | null>(null);

  async function fetchData() {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespace.value.trim() || undefined;
      const res = await esoApi.listPushSecrets(ns);
      if (seq !== fetchSeq.current) return;
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      if (seq !== fetchSeq.current) return;
      error.value = "Failed to load PushSecrets";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
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

  const filtered = items.value.filter((p) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      p.namespace.toLowerCase().includes(q) ||
      (p.sourceSecretName ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <div class="p-6">
      <div class="flex items-start justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">PushSecrets</h1>
      </div>
      <p class="text-sm text-text-muted mb-6">
        PushSecrets push Kubernetes Secrets out to a remote backend (the reverse
        direction of ExternalSecrets).
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-ps-ns"
          >
            Namespace
          </label>
          <input
            id="eso-ps-ns"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-bg-base text-text-primary max-w-xs"
            placeholder="All namespaces"
            value={namespace.value}
            aria-describedby="eso-ps-ns-hint"
            onInput={(e) =>
              handleNamespaceChange((e.target as HTMLInputElement).value)}
          />
          <span id="eso-ps-ns-hint" class="sr-only">
            Filter PushSecrets by namespace; updates after a brief pause.
          </span>
        </div>
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-ps-search"
          >
            Search
          </label>
          <input
            id="eso-ps-search"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-bg-base text-text-primary max-w-xs"
            placeholder="name, source..."
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {items.value.length} PushSecrets
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
                  Source Secret
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-right text-xs font-medium text-text-muted"
                >
                  Stores
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {filtered.map((p) => (
                <tr key={p.uid} class="hover:bg-hover/30">
                  <td class="px-3 py-2">
                    <a
                      href={`/external-secrets/push-secrets/${p.namespace}/${p.name}`}
                      class="font-medium text-brand hover:underline"
                    >
                      {p.name}
                    </a>
                  </td>
                  <td class="px-3 py-2 text-text-secondary">{p.namespace}</td>
                  <td class="px-3 py-2">
                    <StatusBadge status={p.status} />
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {p.sourceSecretName ?? "—"}
                  </td>
                  <td class="px-3 py-2 text-right text-text-secondary tabular-nums">
                    {(p.storeRefs ?? []).length}
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
          <p class="text-text-muted">No PushSecrets match your filters.</p>
        </div>
      )}

      {!loading.value && !error.value && items.value.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            No PushSecrets in this namespace. PushSecrets push Kubernetes
            Secrets back out to a source store (uncommon).
          </p>
        </div>
      )}
    </div>
  );
}
