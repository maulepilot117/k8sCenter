import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import type { CRDInfo, PrinterColumn } from "@/lib/crd-types.ts";
import { age } from "@/lib/format.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";

interface Props {
  group: string;
  resource: string;
}

interface CRDItem {
  metadata: {
    name: string;
    namespace?: string;
    creationTimestamp: string;
    uid: string;
  };
  spec?: Record<string, unknown>;
  status?: {
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
    }>;
    [key: string]: unknown;
  };
  [key: string]: unknown;
}

/** Extract a value from an object using a simple jsonPath-like string. */
function extractJsonPath(obj: Record<string, unknown>, path: string): unknown {
  const parts = path.replace(/^\.*/, "").split(/\.|\[(\d+)\]/).filter(Boolean);
  let current: unknown = obj;
  for (const part of parts) {
    if (current == null || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}

/** Derive Ready status from conditions array. */
function getReadyStatus(
  item: CRDItem,
): "Ready" | "NotReady" | "Unknown" {
  const conditions = item.status?.conditions;
  if (!conditions || conditions.length === 0) return "Unknown";
  const ready = conditions.find((c) => c.type === "Ready");
  if (!ready) return "Unknown";
  return ready.status === "True" ? "Ready" : "NotReady";
}

export default function CRDResourceList({ group, resource }: Props) {
  const loading = useSignal(true);
  const crdMeta = useSignal<CRDInfo | null>(null);
  const items = useSignal<CRDItem[]>([]);
  const continueToken = useSignal<string | undefined>(undefined);
  const loadingMore = useSignal(false);
  const search = useSignal("");
  const statusFilter = useSignal<"all" | "ready" | "notready">("all");
  const openMenu = useSignal<string | null>(null);
  const confirmDelete = useSignal<CRDItem | null>(null);
  const deleting = useSignal(false);

  // Fetch CRD metadata
  useEffect(() => {
    if (!IS_BROWSER) return;

    const fetchMeta = async () => {
      try {
        const res = await apiGet<{ info: CRDInfo; schema: unknown }>(
          `/v1/extensions/crds/${group}/${resource}`,
        );
        crdMeta.value = res.data?.info ?? null;
      } catch {
        crdMeta.value = null;
      }
    };
    fetchMeta();
  }, [group, resource]);

  // Fetch instances (re-fetch when namespace changes)
  useEffect(() => {
    if (!IS_BROWSER) return;

    const fetchInstances = async () => {
      loading.value = true;
      try {
        const ns = selectedNamespace.value;
        const nsPath = ns && ns !== "all" ? `/${ns}` : "";
        const res = await apiGet<CRDItem[]>(
          `/v1/extensions/resources/${group}/${resource}${nsPath}?limit=100`,
        );
        items.value = res.data ?? [];
        continueToken.value = (res as { metadata?: { continue?: string } })
          .metadata?.continue;
      } catch {
        items.value = [];
        continueToken.value = undefined;
      } finally {
        loading.value = false;
      }
    };
    fetchInstances();
  }, [group, resource, selectedNamespace.value]);

  // Load more handler
  const loadMore = async () => {
    if (!continueToken.value || loadingMore.value) return;
    loadingMore.value = true;
    try {
      const ns = selectedNamespace.value;
      const nsPath = ns && ns !== "all" ? `/${ns}` : "";
      const res = await apiGet<CRDItem[]>(
        `/v1/extensions/resources/${group}/${resource}${nsPath}?limit=100&continue=${
          encodeURIComponent(continueToken.value)
        }`,
      );
      items.value = [...items.value, ...(res.data ?? [])];
      continueToken.value = (res as { metadata?: { continue?: string } })
        .metadata?.continue;
    } catch {
      // keep existing items
    } finally {
      loadingMore.value = false;
    }
  };

  // Delete handler
  const handleDelete = async (item: CRDItem) => {
    deleting.value = true;
    try {
      const ns = item.metadata.namespace;
      const nsPath = ns ? `/${ns}` : "";
      await apiDelete(
        `/v1/extensions/resources/${group}/${resource}${nsPath}/${item.metadata.name}`,
      );
      items.value = items.value.filter(
        (i) => i.metadata.uid !== item.metadata.uid,
      );
      showToast(`Deleted ${item.metadata.name}`, "success");
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Delete failed";
      showToast(msg, "error");
    } finally {
      deleting.value = false;
      confirmDelete.value = null;
    }
  };

  // Close kebab menu on outside click
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = () => {
      openMenu.value = null;
    };
    globalThis.document.addEventListener("click", handler);
    return () => globalThis.document.removeEventListener("click", handler);
  }, []);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "400px" }} />;
  }

  if (loading.value) {
    return (
      <div>
        <Skeleton class="h-5 w-48 mb-2" />
        <Skeleton class="h-4 w-72 mb-6" />
        <Skeleton class="h-10 w-full mb-4" />
        <Skeleton class="h-64 w-full rounded-lg" />
      </div>
    );
  }

  const meta = crdMeta.value;
  const isNamespaced = meta?.scope === "Namespaced";
  const kind = meta?.kind ?? resource;
  const version = meta?.version ?? "v1";
  const printerColumns: PrinterColumn[] = meta?.additionalPrinterColumns ?? [];

  // Filter items
  const query = search.value.toLowerCase().trim();
  const filtered = items.value.filter((item) => {
    // Name search
    if (query && !item.metadata.name.toLowerCase().includes(query)) {
      return false;
    }
    // Status filter
    if (statusFilter.value !== "all") {
      const status = getReadyStatus(item);
      if (statusFilter.value === "ready" && status !== "Ready") return false;
      if (statusFilter.value === "notready" && status !== "NotReady") {
        return false;
      }
    }
    return true;
  });

  const instanceLabel = resource.toLowerCase();

  return (
    <div class="flex flex-col h-full">
      {/* Breadcrumbs */}
      <nav
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          fontSize: "12px",
          color: "var(--text-muted)",
          marginBottom: "12px",
        }}
      >
        <a
          href="/extensions"
          style={{ color: "var(--accent)", textDecoration: "none" }}
        >
          Extensions
        </a>
        <span>/</span>
        <a
          href="/extensions"
          style={{ color: "var(--accent)", textDecoration: "none" }}
        >
          {group}
        </a>
        <span>/</span>
        <span style={{ color: "var(--text-primary)" }}>{kind}s</span>
      </nav>

      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "16px",
        }}
      >
        <div>
          <h1
            style={{
              fontSize: "18px",
              fontWeight: 600,
              color: "var(--text-primary)",
              margin: 0,
              letterSpacing: "-0.02em",
            }}
          >
            {kind}s
          </h1>
          <p
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              margin: "2px 0 0",
            }}
          >
            {group}/{version} &middot; {meta?.scope ?? "Unknown"} &middot;{" "}
            {items.value.length} instance{items.value.length !== 1 ? "s" : ""}
          </p>
        </div>
        <a
          href={`/extensions/${group}/${resource}/new`}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "4px",
            padding: "6px 14px",
            fontSize: "12px",
            fontWeight: 500,
            borderRadius: "var(--radius)",
            background: "var(--accent)",
            color: "var(--bg-base)",
            textDecoration: "none",
            whiteSpace: "nowrap",
          }}
        >
          + New {kind}
        </a>
      </div>

      {/* CRD metadata bar */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "16px",
          padding: "8px 14px",
          marginBottom: "16px",
          background: "var(--bg-surface)",
          border: "1px solid var(--border-primary)",
          borderRadius: "var(--radius)",
          fontSize: "11px",
          color: "var(--text-muted)",
        }}
      >
        <span>
          Group:{" "}
          <span
            style={{
              fontFamily: "var(--font-mono)",
              color: "var(--text-secondary)",
            }}
          >
            {group}
          </span>
        </span>
        <span style={{ color: "var(--border-primary)" }}>|</span>
        <span>
          Version:{" "}
          <span
            style={{
              fontFamily: "var(--font-mono)",
              color: "var(--text-secondary)",
            }}
          >
            {version}
          </span>
        </span>
        <span style={{ color: "var(--border-primary)" }}>|</span>
        <span>
          Kind:{" "}
          <span
            style={{
              fontFamily: "var(--font-mono)",
              color: "var(--text-secondary)",
            }}
          >
            {kind}
          </span>
        </span>
        <span style={{ color: "var(--border-primary)" }}>|</span>
        <span>
          Scope:{" "}
          <span
            style={{
              fontFamily: "var(--font-mono)",
              color: "var(--text-secondary)",
            }}
          >
            {meta?.scope ?? "Unknown"}
          </span>
        </span>
        <span style={{ color: "var(--border-primary)" }}>|</span>
        <span>
          Served:{" "}
          <span
            style={{
              fontFamily: "var(--font-mono)",
              color: "var(--text-secondary)",
            }}
          >
            {meta?.served !== false ? "\u2713 Yes" : "\u2717 No"}
          </span>
        </span>
      </div>

      {/* Search + filter bar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "12px",
          marginBottom: "16px",
          flexWrap: "wrap",
        }}
      >
        <input
          type="text"
          placeholder={`Search ${instanceLabel}...`}
          value={search.value}
          onInput={(e) => search.value = (e.target as HTMLInputElement).value}
          style={{
            flex: "1 1 200px",
            maxWidth: "300px",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "6px",
            padding: "7px 12px",
            fontSize: "12px",
            color: "var(--text-primary)",
            outline: "none",
            fontFamily: "inherit",
          }}
        />

        {/* Status filter tabs */}
        <div
          style={{
            display: "flex",
            gap: "2px",
            background: "var(--bg-surface)",
            borderRadius: "6px",
            border: "1px solid var(--border-primary)",
            padding: "2px",
          }}
        >
          {(
            [
              ["all", "All"],
              ["ready", "Ready"],
              ["notready", "Not Ready"],
            ] as const
          ).map(([val, label]) => (
            <button
              type="button"
              key={val}
              onClick={() => statusFilter.value = val}
              style={{
                padding: "4px 10px",
                fontSize: "11px",
                fontWeight: 500,
                borderRadius: "4px",
                border: "none",
                cursor: "pointer",
                background: statusFilter.value === val
                  ? "var(--accent)"
                  : "transparent",
                color: statusFilter.value === val
                  ? "var(--bg-base)"
                  : "var(--text-muted)",
              }}
            >
              {label}
            </button>
          ))}
        </div>

        <span
          style={{
            marginLeft: "auto",
            fontSize: "11px",
            color: "var(--text-muted)",
            fontFamily: "var(--font-mono)",
          }}
        >
          {filtered.length} {instanceLabel}
        </span>
      </div>

      {/* Resource table */}
      <div
        style={{
          flex: 1,
          minHeight: 0,
          overflow: "auto",
          border: "1px solid var(--border-subtle)",
          borderRadius: "var(--radius)",
        }}
      >
        <table
          style={{
            width: "100%",
            borderCollapse: "collapse",
            fontSize: "12px",
          }}
        >
          <thead>
            <tr
              style={{
                background: "var(--bg-surface)",
                position: "sticky",
                top: 0,
                zIndex: 1,
              }}
            >
              <th style={thStyle}>Name</th>
              {isNamespaced && <th style={thStyle}>Namespace</th>}
              <th style={thStyle}>Status</th>
              {printerColumns.map((col) => (
                <th key={col.name} style={thStyle} title={col.description}>
                  {col.name}
                </th>
              ))}
              <th style={thStyle}>Age</th>
              <th style={{ ...thStyle, width: "40px" }} />
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0
              ? (
                <tr>
                  <td
                    colSpan={3 + (isNamespaced ? 1 : 0) +
                      printerColumns.length + 1}
                    style={{
                      padding: "32px 14px",
                      textAlign: "center",
                      color: "var(--text-muted)",
                      fontSize: "13px",
                    }}
                  >
                    {items.value.length === 0
                      ? `No ${instanceLabel} found`
                      : "No matching results"}
                  </td>
                </tr>
              )
              : filtered.map((item) => {
                const status = getReadyStatus(item);
                const ns = item.metadata.namespace;
                const detailNs = ns ?? "_";
                const detailHref =
                  `/extensions/${group}/${resource}/${detailNs}/${item.metadata.name}`;
                const menuId = item.metadata.uid;

                return (
                  <tr
                    key={item.metadata.uid}
                    style={{
                      borderBottom: "1px solid var(--border-subtle)",
                    }}
                    onMouseEnter={(e) => {
                      (e.currentTarget as HTMLElement).style.background =
                        "var(--bg-hover)";
                    }}
                    onMouseLeave={(e) => {
                      (e.currentTarget as HTMLElement).style.background = "";
                    }}
                  >
                    {/* Name */}
                    <td style={tdStyle}>
                      <a
                        href={detailHref}
                        style={{
                          color: "var(--accent)",
                          fontWeight: 500,
                          textDecoration: "none",
                        }}
                      >
                        {item.metadata.name}
                      </a>
                    </td>

                    {/* Namespace */}
                    {isNamespaced && (
                      <td
                        style={{
                          ...tdStyle,
                          color: "var(--text-secondary)",
                        }}
                      >
                        {ns ?? "-"}
                      </td>
                    )}

                    {/* Status */}
                    <td style={tdStyle}>
                      {status === "Unknown"
                        ? (
                          <span style={{ color: "var(--text-muted)" }}>
                            &mdash;
                          </span>
                        )
                        : (
                          <span
                            style={{
                              display: "inline-block",
                              padding: "2px 8px",
                              borderRadius: "var(--radius-sm)",
                              fontSize: "11px",
                              fontWeight: 500,
                              background: status === "Ready"
                                ? "var(--success-dim)"
                                : "var(--error-dim)",
                              color: status === "Ready"
                                ? "var(--success)"
                                : "var(--error)",
                            }}
                          >
                            {status === "Ready" ? "Ready" : "Not Ready"}
                          </span>
                        )}
                    </td>

                    {/* Additional printer columns */}
                    {printerColumns.map((col) => {
                      const val = extractJsonPath(
                        item as unknown as Record<string, unknown>,
                        col.jsonPath,
                      );
                      return (
                        <td
                          key={col.name}
                          style={{
                            ...tdStyle,
                            fontFamily: "var(--font-mono)",
                            color: "var(--text-secondary)",
                          }}
                        >
                          {val != null ? String(val) : "-"}
                        </td>
                      );
                    })}

                    {/* Age */}
                    <td
                      style={{
                        ...tdStyle,
                        color: "var(--text-muted)",
                        fontFamily: "var(--font-mono)",
                      }}
                    >
                      {age(item.metadata.creationTimestamp)}
                    </td>

                    {/* Kebab menu */}
                    <td style={{ ...tdStyle, position: "relative" }}>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          openMenu.value = openMenu.value === menuId
                            ? null
                            : menuId;
                        }}
                        style={{
                          background: "none",
                          border: "none",
                          cursor: "pointer",
                          padding: "4px 8px",
                          fontSize: "16px",
                          color: "var(--text-muted)",
                          lineHeight: 1,
                        }}
                      >
                        &#8942;
                      </button>
                      {openMenu.value === menuId && (
                        <div
                          style={{
                            position: "absolute",
                            right: "14px",
                            top: "100%",
                            background: "var(--bg-surface)",
                            border: "1px solid var(--border-primary)",
                            borderRadius: "var(--radius)",
                            boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
                            zIndex: 10,
                            minWidth: "100px",
                          }}
                        >
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              openMenu.value = null;
                              confirmDelete.value = item;
                            }}
                            style={{
                              display: "block",
                              width: "100%",
                              padding: "8px 14px",
                              fontSize: "12px",
                              color: "var(--error)",
                              background: "none",
                              border: "none",
                              cursor: "pointer",
                              textAlign: "left",
                            }}
                            onMouseEnter={(e) => {
                              (e.currentTarget as HTMLElement).style
                                .background = "var(--bg-hover)";
                            }}
                            onMouseLeave={(e) => {
                              (e.currentTarget as HTMLElement).style
                                .background = "";
                            }}
                          >
                            Delete
                          </button>
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
          </tbody>
        </table>
      </div>

      {/* Load more */}
      {continueToken.value && (
        <div style={{ textAlign: "center", padding: "16px 0" }}>
          <button
            type="button"
            onClick={loadMore}
            disabled={loadingMore.value}
            style={{
              padding: "6px 20px",
              fontSize: "12px",
              fontWeight: 500,
              borderRadius: "var(--radius)",
              border: "1px solid var(--border-primary)",
              background: "var(--bg-surface)",
              color: "var(--text-primary)",
              cursor: loadingMore.value ? "default" : "pointer",
              opacity: loadingMore.value ? 0.6 : 1,
            }}
          >
            {loadingMore.value ? "Loading..." : "Load more"}
          </button>
        </div>
      )}

      {/* Delete confirmation dialog */}
      {confirmDelete.value && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "rgba(0,0,0,0.5)",
            zIndex: 100,
          }}
          onClick={() => confirmDelete.value = null}
        >
          <div
            style={{
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              borderRadius: "var(--radius)",
              padding: "24px",
              maxWidth: "400px",
              width: "90%",
            }}
            onClick={(e) => e.stopPropagation()}
          >
            <p
              style={{
                margin: "0 0 16px",
                fontSize: "13px",
                color: "var(--text-primary)",
              }}
            >
              Are you sure you want to delete{" "}
              <strong>{confirmDelete.value.metadata.name}</strong>?
            </p>
            <div
              style={{
                display: "flex",
                justifyContent: "flex-end",
                gap: "8px",
              }}
            >
              <button
                type="button"
                onClick={() => confirmDelete.value = null}
                style={{
                  padding: "6px 14px",
                  fontSize: "12px",
                  borderRadius: "var(--radius)",
                  border: "1px solid var(--border-primary)",
                  background: "var(--bg-surface)",
                  color: "var(--text-primary)",
                  cursor: "pointer",
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => handleDelete(confirmDelete.value!)}
                disabled={deleting.value}
                style={{
                  padding: "6px 14px",
                  fontSize: "12px",
                  borderRadius: "var(--radius)",
                  border: "none",
                  background: "var(--error)",
                  color: "#fff",
                  cursor: deleting.value ? "default" : "pointer",
                  opacity: deleting.value ? 0.6 : 1,
                }}
              >
                {deleting.value ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/** Table header cell style */
const thStyle: Record<string, string> = {
  padding: "10px 14px",
  fontSize: "11px",
  fontWeight: "600",
  textTransform: "uppercase",
  letterSpacing: "0.06em",
  color: "var(--text-muted)",
  textAlign: "left",
  borderBottom: "1px solid var(--border-subtle)",
  whiteSpace: "nowrap",
};

/** Table data cell style */
const tdStyle: Record<string, string> = {
  padding: "10px 14px",
  fontSize: "12px",
  whiteSpace: "nowrap",
};
