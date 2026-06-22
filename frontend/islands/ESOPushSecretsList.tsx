import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import type { PushSecret } from "@/lib/eso-types.ts";
import ResourceTable from "@/components/ui/ResourceTable.tsx";

const NAMESPACE_DEBOUNCE_MS = 300;

/** Map PushSecret status → StatusDot tone. */
function pushToDot(
  status: string,
): "success" | "error" | "warning" | "info" | "neutral" {
  switch (status) {
    case "Synced":
      return "success";
    case "SyncFailed":
      return "error";
    case "Stale":
      return "warning";
    default:
      return "neutral";
  }
}

export default function ESOPushSecretsList() {
  const items = useSignal<PushSecret[]>([]);
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
          PushSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const nsByGlobal = selectedNamespace.value;
  const byNamespace = filterByNamespace(items.value, nsByGlobal);
  const filtered = byNamespace.filter((p) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      p.namespace.toLowerCase().includes(q) ||
      (p.sourceSecretName ?? "").toLowerCase().includes(q)
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
          PushSecrets
        </h1>
      </div>
      <p
        class="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        PushSecrets push Kubernetes Secrets out to a remote backend (the reverse
        direction of ExternalSecrets).
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
            htmlFor="eso-ps-ns"
          >
            Namespace
          </label>
          <input
            id="eso-ps-ns"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
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
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-ps-search"
          >
            Search
          </label>
          <input
            id="eso-ps-search"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, source…"
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {byNamespace.length} PushSecrets
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
        <ResourceTable
          columns={[
            { key: "name", label: "Name", width: "1.6fr" },
            { key: "namespace", label: "Namespace", width: "120px" },
            { key: "status", label: "Status", width: "110px" },
            { key: "sourceSecret", label: "Source Secret", width: "1fr" },
            { key: "stores", label: "Stores", width: "70px", align: "right" },
          ]}
          rows={filtered.map((p) => {
            const detailHref = `/external-secrets/push-secrets/${
              encodeURIComponent(p.namespace)
            }/${encodeURIComponent(p.name)}`;
            return {
              id: p.uid,
              cells: {
                name: (
                  <span class="inline-flex items-center gap-2">
                    <StatusDot status={pushToDot(p.status)} />
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
                      {p.name}
                    </a>
                  </span>
                ),
                namespace: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {p.namespace}
                  </span>
                ),
                status: <StatusBadge status={p.status} />,
                sourceSecret: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {p.sourceSecretName ?? "—"}
                  </span>
                ),
                stores: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                    class="tabular-nums"
                  >
                    {(p.storeRefs ?? []).length}
                  </span>
                ),
              },
              onClick: () => {
                globalThis.location.href = detailHref;
              },
            };
          })}
        />
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
            No PushSecrets match your filters.
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
            No PushSecrets in this namespace. PushSecrets push Kubernetes
            Secrets back out to a source store (uncommon).
          </p>
        </div>
      )}
    </div>
  );
}
