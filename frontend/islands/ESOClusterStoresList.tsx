import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import SecretStoreWizard from "@/islands/SecretStoreWizard.tsx";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { SecretStore } from "@/lib/eso-types.ts";

const TH_STYLE = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase" as const,
  letterSpacing: "0.05em",
  color: "var(--text-muted)",
};

export default function ESOClusterStoresList() {
  const items = useSignal<SecretStore[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const detected = useSignal<boolean | null>(null);
  const wizardOpen = useSignal(false);

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
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") wizardOpen.value = true;
  }, []);

  if (!IS_BROWSER) return null;

  if (!loading.value && detected.value === false) {
    return (
      <div class="p-6">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
          class="mb-6"
        >
          ClusterSecretStores
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.value.filter((s) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
      (s.provider ?? "").toLowerCase().includes(q)
    );
  });

  const inputStyle =
    "rounded-lg px-3 py-1.5 text-sm max-w-xs focus:outline-none focus:ring-1";

  return (
    <div class="p-6">
      {/* Page header */}
      <div class="flex items-start justify-between mb-1">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
        >
          ClusterSecretStores
        </h1>
        <button
          type="button"
          onClick={() => (wizardOpen.value = true)}
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
          style={{ background: "var(--accent)", color: "var(--bg-base)" }}
        >
          + New ClusterSecretStore
        </button>
      </div>
      <p
        class="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Cluster-scoped SecretStores accessible to ExternalSecrets in any
        namespace.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-cstores-search"
          >
            Search
          </label>
          <input
            id="eso-cstores-search"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, provider…"
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {items.value.length} ClusterSecretStores
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {!loading.value && error.value && (
        <p style={{ fontSize: "13px", color: "var(--error)" }} class="py-4">
          {error.value}
        </p>
      )}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div
          class="overflow-x-auto rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <table class="w-full">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border-subtle)" }}>
                <th scope="col" class="px-3 py-2 text-left" style={TH_STYLE}>
                  Name
                </th>
                <th scope="col" class="px-3 py-2 text-left" style={TH_STYLE}>
                  Status
                </th>
                <th scope="col" class="px-3 py-2 text-left" style={TH_STYLE}>
                  Provider
                </th>
                <th scope="col" class="px-3 py-2 text-left" style={TH_STYLE}>
                  Ready
                </th>
                <th scope="col" class="px-3 py-2" style={TH_STYLE} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((s) => {
                const detailHref = `/external-secrets/cluster-stores/${
                  encodeURIComponent(s.name)
                }`;
                return (
                  <tr
                    key={s.uid}
                    class="hover:bg-hover/30 cursor-pointer"
                    style={{ borderTop: "1px solid var(--border-subtle)" }}
                    onClick={() => {
                      globalThis.location.href = detailHref;
                    }}
                  >
                    <td class="px-3 py-2">
                      <span class="inline-flex items-center gap-2">
                        <StatusDot
                          status={s.ready ? "success" : "error"}
                        />
                        <a
                          href={detailHref}
                          class="hover:underline"
                          style={{
                            fontSize: "13px",
                            fontWeight: 500,
                            fontFamily: "var(--font-mono, monospace)",
                            color: "var(--text-primary)",
                          }}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {s.name}
                        </a>
                      </span>
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
                          <span
                            style={{
                              fontSize: "12px",
                              fontWeight: 500,
                              color: "var(--success)",
                            }}
                          >
                            Ready
                          </span>
                        )
                        : (
                          <span
                            style={{
                              fontSize: "12px",
                              fontWeight: 500,
                              color: "var(--error)",
                            }}
                          >
                            Not Ready
                          </span>
                        )}
                    </td>
                    <td
                      class="px-3 py-2 text-right"
                      style={{ color: "var(--text-muted)" }}
                    >
                      ›
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {!loading.value && !error.value && filtered.length === 0 &&
        items.value.length > 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No ClusterSecretStores match your filters.
          </p>
        </div>
      )}

      {!loading.value && !error.value && items.value.length === 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No ClusterSecretStores visible. Your permissions may restrict
            visibility, or no ClusterSecretStores exist.
          </p>
        </div>
      )}

      {wizardOpen.value && (
        <SecretStoreWizard
          scope="cluster"
          onClose={() => (wizardOpen.value = false)}
        />
      )}
    </div>
  );
}
