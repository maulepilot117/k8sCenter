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
      const key = `${item.resource}.${item.group}`;
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
    <div class="flex flex-col h-full">
      {/* Page header */}
      <div style={{ marginBottom: "20px" }}>
        <h1
          style={{
            fontSize: "20px",
            fontWeight: 600,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          Extensions
        </h1>
        <p
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            marginTop: "2px",
          }}
        >
          {totalCRDs} CRDs across {groupCount} API group{groupCount !== 1
            ? "s"
            : ""}
        </p>
      </div>

      {/* Search bar */}
      <div style={{ marginBottom: "20px" }}>
        <input
          type="text"
          placeholder="Search CRDs by kind or resource name..."
          value={search.value}
          onInput={(e) =>
            search.value = (e.target as HTMLInputElement).value}
          style={{
            width: "100%",
            maxWidth: "400px",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "6px",
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
              fontSize: "14px",
            }}
          >
            No custom resource definitions found in this cluster.
          </div>
        )
        : (
          <div
            style={{
              flex: 1,
              minHeight: 0,
              overflow: "auto",
            }}
          >
            {sortedGroups.map(([group, items]) => {
              const instances = groupInstanceCount(items);
              return (
                <div key={group} style={{ marginBottom: "28px" }}>
                  {/* Group header */}
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
                        background: "var(--border-primary)",
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

                  {/* CRD card grid */}
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
                      const instanceCount = counts.value[fullName] ?? 0;

                      return (
                        <a
                          key={fullName}
                          href={`/extensions/${info.group}/${info.resource}`}
                          style={{
                            display: "block",
                            background: "var(--bg-surface)",
                            border: "1px solid var(--border-primary)",
                            borderRadius: "var(--radius)",
                            padding: "12px 14px",
                            cursor: "pointer",
                            textDecoration: "none",
                            transition:
                              "border-color 0.15s ease, background 0.15s ease",
                          }}
                          onMouseEnter={(e) => {
                            (e.currentTarget as HTMLElement).style
                              .borderColor = "var(--accent)";
                          }}
                          onMouseLeave={(e) => {
                            (e.currentTarget as HTMLElement).style
                              .borderColor = "var(--border-primary)";
                          }}
                        >
                          {/* Kind name */}
                          <div
                            style={{
                              display: "flex",
                              alignItems: "center",
                              justifyContent: "space-between",
                              marginBottom: "4px",
                            }}
                          >
                            <span
                              style={{
                                fontSize: "13px",
                                fontWeight: 500,
                                color: "var(--text-primary)",
                              }}
                            >
                              {info.kind}
                            </span>
                            <span
                              style={{
                                fontSize: "12px",
                                fontFamily: "var(--font-mono)",
                                color: "var(--text-secondary)",
                              }}
                            >
                              {instanceCount}
                            </span>
                          </div>

                          {/* Full resource name + scope */}
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
                                color: "var(--text-muted)",
                                whiteSpace: "nowrap",
                                padding: "1px 6px",
                                borderRadius: "var(--radius-sm)",
                                background: "var(--bg-base)",
                              }}
                            >
                              {info.scope}
                            </span>
                          </div>
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
