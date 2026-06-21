import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import type {
  CRDCountsResponse,
  CRDGroupedResponse,
  CRDInfo,
} from "@/lib/crd-types.ts";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";

export default function ExtensionsHub() {
  const crds = useSignal<CRDGroupedResponse>({});
  const counts = useSignal<CRDCountsResponse>({});
  const loading = useSignal(true);
  const search = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;

    const fetchData = async () => {
      try {
        const [crdsRes, countsRes] = await Promise.all([
          apiGet<CRDGroupedResponse>("/v1/extensions/crds"),
          apiGet<CRDCountsResponse>("/v1/extensions/crds/counts"),
        ]);
        crds.value = crdsRes.data ?? {};
        counts.value = countsRes.data ?? {};
      } catch {
        crds.value = {};
        counts.value = {};
      } finally {
        loading.value = false;
      }
    };

    fetchData();
  }, []);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "400px" }} />;
  }

  const allGroups = crds.value;
  const query = search.value.toLowerCase().trim();

  // Filter CRDs by search query
  const filteredGroups: Record<string, CRDInfo[]> = {};
  let totalCRDs = 0;

  for (const [group, items] of Object.entries(allGroups)) {
    const filtered = query
      ? items.filter(
        (c) =>
          c.kind.toLowerCase().includes(query) ||
          `${c.resource}.${c.group}`.toLowerCase().includes(query),
      )
      : items;
    if (filtered.length > 0) {
      filteredGroups[group] = filtered;
      totalCRDs += filtered.length;
    }
  }

  const groupCount = Object.keys(filteredGroups).length;
  const sortedGroups = Object.entries(filteredGroups).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  // Count total instances for a group
  const groupInstanceCount = (items: CRDInfo[]): number => {
    let total = 0;
    for (const item of items) {
      const key = `${item.group}/${item.resource}`;
      total += counts.value[key] ?? 0;
    }
    return total;
  };

  if (loading.value) {
    return (
      <div style={{ padding: "0" }}>
        {/* Header skeleton */}
        <div style={{ marginBottom: "24px" }}>
          <Skeleton class="h-7 w-40 mb-2" />
          <Skeleton class="h-4 w-64" />
        </div>
        {/* Search skeleton */}
        <Skeleton class="h-10 w-full mb-6" style={{ maxWidth: "400px" }} />
        {/* Card grid skeleton */}
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))",
            gap: "12px",
          }}
        >
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} class="h-20 w-full rounded-lg" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header */}
      <div style={{ marginBottom: "20px" }}>
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          Extensions
        </h1>
        <p
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            marginTop: "4px",
          }}
        >
          {totalCRDs} CRDs across {groupCount}{" "}
          API group{groupCount !== 1 ? "s" : ""}
        </p>
      </div>

      {/* Search bar — glass-elevated input */}
      <div style={{ marginBottom: "20px" }}>
        <input
          type="text"
          placeholder="Search CRDs by kind or resource name…"
          value={search.value}
          onInput={(e) =>
            search.value = (e.target as HTMLInputElement).value}
          style={{
            width: "100%",
            maxWidth: "400px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-primary)",
            borderRadius: "9px",
            padding: "8px 12px",
            fontSize: "13px",
            color: "var(--text-primary)",
            outline: "none",
            fontFamily: "inherit",
          }}
        />
      </div>

      {/* Content */}
      {sortedGroups.length === 0
        ? (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              flex: 1,
              color: "var(--text-muted)",
              fontSize: "13px",
            }}
          >
            No custom resource definitions found in this cluster.
          </div>
        )
        : (
          <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
            {sortedGroups.map(([group, items]) => {
              const instances = groupInstanceCount(items);
              return (
                <div key={group} style={{ marginBottom: "28px" }}>
                  {/* Group section label */}
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "12px",
                      marginBottom: "12px",
                    }}
                  >
                    <span
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.08em",
                        color: "var(--text-muted)",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {group}
                    </span>
                    <div
                      style={{
                        flex: 1,
                        height: "1px",
                        background: "var(--border-subtle)",
                      }}
                    />
                    <span
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        whiteSpace: "nowrap",
                        fontFamily: "var(--font-mono)",
                      }}
                    >
                      {items.length} type{items.length !== 1 ? "s" : ""}{" "}
                      &middot; {instances} instance{instances !== 1 ? "s" : ""}
                    </span>
                  </div>

                  {/* CRD card grid — GlassCard per card */}
                  <div
                    class="stagger-in"
                    style={{
                      display: "grid",
                      gridTemplateColumns:
                        "repeat(auto-fill, minmax(280px, 1fr))",
                      gap: "10px",
                    }}
                  >
                    {items.map((info) => {
                      const fullName = `${info.resource}.${info.group}`;
                      const countKey = `${info.group}/${info.resource}`;
                      const instanceCount = counts.value[countKey] ?? 0;

                      return (
                        <a
                          key={fullName}
                          href={`/extensions/${info.group}/${info.resource}`}
                          style={{ textDecoration: "none", display: "block" }}
                        >
                          <GlassCard
                            padding="12px 14px"
                            radius={16}
                            style={{ cursor: "pointer" }}
                          >
                            {/* Kind name + instance count */}
                            <div
                              style={{
                                display: "flex",
                                alignItems: "center",
                                justifyContent: "space-between",
                                marginBottom: "6px",
                              }}
                            >
                              <span
                                style={{
                                  fontSize: "14px",
                                  fontWeight: 650,
                                  color: "var(--text-primary)",
                                }}
                              >
                                {info.kind}
                              </span>
                              <span
                                style={{
                                  fontSize: "13px",
                                  fontFamily: "var(--font-mono)",
                                  fontWeight: 600,
                                  color: "var(--accent)",
                                  fontVariantNumeric: "tabular-nums",
                                }}
                              >
                                {instanceCount}
                              </span>
                            </div>

                            {/* Full resource name + scope pill */}
                            <div
                              style={{
                                display: "flex",
                                alignItems: "center",
                                justifyContent: "space-between",
                                gap: "8px",
                              }}
                            >
                              <span
                                style={{
                                  fontSize: "11px",
                                  fontFamily: "var(--font-mono)",
                                  color: "var(--text-muted)",
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                  whiteSpace: "nowrap",
                                }}
                              >
                                {fullName}
                              </span>
                              <span
                                style={{
                                  fontSize: "10px",
                                  fontWeight: 600,
                                  color: "var(--text-muted)",
                                  whiteSpace: "nowrap",
                                  padding: "2px 7px",
                                  borderRadius: "6px",
                                  background:
                                    "color-mix(in srgb, var(--text-muted) 12%, transparent)",
                                }}
                              >
                                {info.scope}
                              </span>
                            </div>
                          </GlassCard>
                        </a>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        )}
    </div>
  );
}
