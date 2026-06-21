import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { ClusterExternalSecret } from "@/lib/eso-types.ts";

/** Map ESO status → StatusDot tone. */
function esoToDot(
  status: string,
): "success" | "error" | "warning" | "info" | "neutral" {
  switch (status) {
    case "Synced":
      return "success";
    case "SyncFailed":
      return "error";
    case "Stale":
      return "warning";
    case "Drifted":
      return "info";
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

export default function ESOClusterExternalSecretsList() {
  const items = useSignal<ClusterExternalSecret[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const detected = useSignal<boolean | null>(null);

  async function fetchData() {
    try {
      const res = await esoApi.listClusterExternalSecrets();
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      error.value = "Failed to load ClusterExternalSecrets";
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
          ClusterExternalSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.value.filter((ces) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      ces.name.toLowerCase().includes(q) ||
      ces.storeRef.name.toLowerCase().includes(q) ||
      (ces.targetSecretName ?? "").toLowerCase().includes(q)
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
          ClusterExternalSecrets
        </h1>
      </div>
      <p
        class="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Cluster-scoped resources that fan out the same Secret to multiple
        namespaces.
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
            htmlFor="eso-ces-search"
          >
            Search
          </label>
          <input
            id="eso-ces-search"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, store, target…"
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {items.value.length} ClusterExternalSecrets
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
                  Store
                </th>
                <th scope="col" class="px-3 py-2 text-left" style={TH_STYLE}>
                  Target
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-right"
                  style={TH_STYLE}
                >
                  Provisioned
                </th>
                <th
                  scope="col"
                  class="px-3 py-2 text-right"
                  style={TH_STYLE}
                >
                  Failed
                </th>
                <th scope="col" class="px-3 py-2" style={TH_STYLE} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((ces) => (
                <tr
                  key={ces.uid}
                  class="hover:bg-hover/30 cursor-pointer"
                  style={{ borderTop: "1px solid var(--border-subtle)" }}
                  onClick={() => {
                    globalThis.location.href =
                      `/external-secrets/cluster-external-secrets/${
                        encodeURIComponent(ces.name)
                      }`;
                  }}
                >
                  <td class="px-3 py-2">
                    <span class="inline-flex items-center gap-2">
                      <StatusDot status={esoToDot(ces.status)} />
                      <a
                        href={`/external-secrets/cluster-external-secrets/${
                          encodeURIComponent(ces.name)
                        }`}
                        class="hover:underline"
                        style={{
                          fontSize: "13px",
                          fontWeight: 500,
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-primary)",
                        }}
                        onClick={(e) => e.stopPropagation()}
                      >
                        {ces.name}
                      </a>
                    </span>
                  </td>
                  <td class="px-3 py-2">
                    <StatusBadge status={ces.status} />
                  </td>
                  <td
                    class="px-3 py-2"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {ces.storeRef.name}
                  </td>
                  <td
                    class="px-3 py-2"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {ces.targetSecretName ?? "—"}
                  </td>
                  <td
                    class="px-3 py-2 text-right tabular-nums"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {(ces.provisionedNamespaces ?? []).length}
                  </td>
                  <td
                    class="px-3 py-2 text-right tabular-nums"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {(ces.failedNamespaces ?? []).length}
                  </td>
                  <td
                    class="px-3 py-2 text-right"
                    style={{ color: "var(--text-muted)" }}
                  >
                    ›
                  </td>
                </tr>
              ))}
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
            No ClusterExternalSecrets match your filters.
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
            No ClusterExternalSecrets visible. These sync the same Secret to
            multiple namespaces.
          </p>
        </div>
      )}
    </div>
  );
}
