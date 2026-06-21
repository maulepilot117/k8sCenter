import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import type { SecretStore } from "@/lib/eso-types.ts";

const NAMESPACE_DEBOUNCE_MS = 300;

/** Map SecretStore ready state → StatusDot tone. */
function storeToDot(
  status: string,
  ready: boolean,
): "success" | "error" | "warning" | "neutral" {
  if (ready) return "success";
  switch (status) {
    case "Ready":
      return "success";
    case "NotReady":
      return "error";
    default:
      return "neutral";
  }
}

const TH_STYLE = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase" as const,
  letterSpacing: "0.05em",
  color: "var(--text-muted)",
};

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
    if (debounceHandle.current !== null) clearTimeout(debounceHandle.current);
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;
      fetchData();
    }, NAMESPACE_DEBOUNCE_MS);
  }

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
          SecretStores
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const nsByGlobal = selectedNamespace.value;
  const byNamespace = filterByNamespace(items.value, nsByGlobal);
  const filtered = byNamespace.filter((s) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
      (s.namespace ?? "").toLowerCase().includes(q) ||
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
          SecretStores
        </h1>
        <a
          href="/external-secrets/stores/new"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
          style={{ background: "var(--accent)", color: "var(--bg-base)" }}
        >
          + New SecretStore
        </a>
      </div>
      <p
        class="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Namespaced SecretStores describe how ESO talks to a secret backend.
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
            htmlFor="eso-stores-ns"
          >
            Namespace
          </label>
          <input
            id="eso-stores-ns"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
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
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-stores-search"
          >
            Search
          </label>
          <input
            id="eso-stores-search"
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
          {filtered.length} of {byNamespace.length} SecretStores
          {byNamespace.length < items.value.length &&
            ` (${items.value.length} total)`}
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
                  Namespace
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
                const detailHref = s.namespace
                  ? `/external-secrets/stores/${
                    encodeURIComponent(s.namespace)
                  }/${encodeURIComponent(s.name)}`
                  : null;
                return (
                  <tr
                    key={s.uid}
                    class={`hover:bg-hover/30 ${
                      detailHref ? "cursor-pointer" : ""
                    }`}
                    style={{ borderTop: "1px solid var(--border-subtle)" }}
                    onClick={() => {
                      if (detailHref) globalThis.location.href = detailHref;
                    }}
                  >
                    <td class="px-3 py-2">
                      <span class="inline-flex items-center gap-2">
                        <StatusDot status={storeToDot(s.status, s.ready)} />
                        {detailHref
                          ? (
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
                          )
                          : (
                            <span
                              style={{
                                fontSize: "13px",
                                fontWeight: 500,
                                fontFamily: "var(--font-mono, monospace)",
                                color: "var(--text-primary)",
                              }}
                            >
                              {s.name}
                            </span>
                          )}
                      </span>
                    </td>
                    <td
                      class="px-3 py-2"
                      style={{ fontSize: "13px", color: "var(--text-muted)" }}
                    >
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
                      {detailHref ? "›" : ""}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {!loading.value && !error.value && filtered.length === 0 &&
        byNamespace.length > 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No SecretStores match your filters.
          </p>
        </div>
      )}

      {!loading.value && !error.value && byNamespace.length === 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No SecretStores in this namespace. ExternalSecrets require a
            SecretStore to function.
          </p>
        </div>
      )}
    </div>
  );
}
