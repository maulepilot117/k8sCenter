import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { timeAgo } from "@/lib/timeAgo.ts";
import type { ExternalSecret } from "@/lib/eso-types.ts";

/** Debounce window (ms) for namespace input → re-fetch. Long enough to
 *  coalesce typing, short enough to feel responsive once the user stops. */
const NAMESPACE_DEBOUNCE_MS = 300;

export default function ESOExternalSecretsList() {
  const items = useSignal<ExternalSecret[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const namespace = useSignal("");
  const search = useSignal("");
  // null = unknown (still loading), true = ESO present, false = render install prompt.
  const detected = useSignal<boolean | null>(null);

  // Sequence counter: every fetch captures a token; a response is applied
  // only if its token is still the latest. Stops slow earlier responses
  // from overwriting state from later, faster ones (`apiGet` does not
  // expose an AbortSignal, so the sequence guard is the canonical guard).
  const fetchSeq = useRef(0);
  const debounceHandle = useRef<number | null>(null);

  async function fetchData() {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespace.value.trim() || undefined;
      const res = await esoApi.listExternalSecrets(ns);
      if (seq !== fetchSeq.current) return; // stale — drop
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      if (seq !== fetchSeq.current) return;
      error.value = "Failed to load ExternalSecrets";
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
        // Status probe failed — assume present and let fetchData surface the
        // real error rather than masking it as "ESO not installed".
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
        <h1 class="text-2xl font-bold text-text-primary mb-6">
          ExternalSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.value.filter((es) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      es.name.toLowerCase().includes(q) ||
      es.namespace.toLowerCase().includes(q) ||
      es.storeRef.name.toLowerCase().includes(q) ||
      (es.targetSecretName ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <div class="p-6">
      <div class="flex items-start justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">ExternalSecrets</h1>
      </div>
      <p class="text-sm text-text-muted mb-6">
        ExternalSecrets sync data from a SecretStore into a Kubernetes Secret.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-es-ns"
          >
            Namespace
          </label>
          <input
            id="eso-es-ns"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary max-w-xs"
            placeholder="All namespaces"
            value={namespace.value}
            aria-describedby="eso-es-ns-hint"
            onInput={(e) =>
              handleNamespaceChange((e.target as HTMLInputElement).value)}
          />
          <span id="eso-es-ns-hint" class="sr-only">
            Filter ExternalSecrets by namespace; updates after a brief pause.
          </span>
        </div>
        <div class="flex items-center gap-2">
          <label
            class="text-sm font-medium text-text-secondary"
            htmlFor="eso-es-search"
          >
            Search
          </label>
          <input
            id="eso-es-search"
            type="text"
            class="rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary max-w-xs"
            placeholder="name, store, target..."
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {items.value.length} ExternalSecrets
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
                  Store
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Target Secret
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                >
                  Last Sync
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {filtered.map((es) => (
                <tr key={es.uid} class="hover:bg-hover/30">
                  <td class="px-3 py-2">
                    <a
                      href={`/external-secrets/external-secrets/${
                        encodeURIComponent(es.namespace)
                      }/${encodeURIComponent(es.name)}`}
                      class="font-medium text-brand hover:underline"
                    >
                      {es.name}
                    </a>
                  </td>
                  <td class="px-3 py-2 text-text-secondary">{es.namespace}</td>
                  <td class="px-3 py-2">
                    <StatusBadge status={es.status} />
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {es.storeRef.name}
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {es.targetSecretName ?? "—"}
                  </td>
                  <td class="px-3 py-2 text-text-secondary text-xs">
                    {es.lastSyncTime ? timeAgo(es.lastSyncTime) : "—"}
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
          <p class="text-text-muted">
            No ExternalSecrets match your filters.
          </p>
        </div>
      )}

      {!loading.value && !error.value && items.value.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p class="text-text-muted">
            No ExternalSecrets in this namespace. Create one to start syncing
            secrets from a SecretStore.
          </p>
        </div>
      )}
    </div>
  );
}
