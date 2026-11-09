/** @jsxImportSource react */
import { useEffect, useRef, useState } from "react";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { apiDelete, apiGet } from "@/lib/api.ts";
import type { CRDInfo, PrinterColumn } from "@/lib/crd-types.ts";
import { age } from "@/lib/format.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface Props {
  group: string;
  resource: string;
}

interface CRDItem extends Record<string, unknown> {
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
}

/**
 * Root attributes shared by the server placeholder, the loading skeleton and
 * the hydrated list, so the markup swaps in place without a layout jump.
 */
const ROOT_PROPS = {
  className: "flex flex-col h-full",
  style: { minHeight: "400px" },
} as const;

/** Extract a value from an object using a simple jsonPath-like string. */
function extractJsonPath(obj: Record<string, unknown>, path: string): unknown {
  const parts = path
    .replace(/^\.*/, "")
    .split(/\.|\[(\d+)\]/)
    .filter(Boolean);
  let current: unknown = obj;
  for (const part of parts) {
    if (current == null || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}

/** Derive Ready status from conditions array. */
function getReadyStatus(item: CRDItem): "Ready" | "NotReady" | "Unknown" {
  const conditions = item.status?.conditions;
  if (!conditions || conditions.length === 0) return "Unknown";
  const ready = conditions.find((c) => c.type === "Ready");
  if (!ready) return "Unknown";
  return ready.status === "True" ? "Ready" : "NotReady";
}

export default function CRDResourceList({ group, resource }: Props) {
  const isClient = useIsClient();
  const namespace = useSignalValue(selectedNamespace);
  const [loading, setLoading] = useState(true);
  const [meta, setMeta] = useState<CRDInfo | null>(null);
  const [items, setItems] = useState<CRDItem[]>([]);
  const [continueToken, setContinueToken] = useState<string | undefined>(
    undefined,
  );
  const [loadingMore, setLoadingMore] = useState(false);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<
    "all" | "ready" | "notready"
  >("all");
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<CRDItem | null>(null);
  const [deleting, setDeleting] = useState(false);
  // Guards that must stop a second click before the next render.
  const loadingMoreRef = useRef(false);
  const deletingRef = useRef(false);

  const listNsPath = namespace && namespace !== "all" ? `/${namespace}` : "";
  const listUrl = `/v1/extensions/resources/${group}/${resource}${listNsPath}?limit=100`;

  // Fetch CRD metadata
  useEffect(() => {
    if (!isClient) return;

    const fetchMeta = async () => {
      try {
        const res = await apiGet<{ info: CRDInfo; schema: unknown }>(
          `/v1/extensions/crds/${group}/${resource}`,
        );
        setMeta(res.data?.info ?? null);
      } catch {
        setMeta(null);
      }
    };
    fetchMeta();
  }, [isClient, group, resource]);

  // Fetch instances (re-fetch when namespace changes)
  useEffect(() => {
    if (!isClient) return;

    const fetchInstances = async () => {
      setLoading(true);
      try {
        const res = await apiGet<CRDItem[]>(listUrl);
        setItems(res.data ?? []);
        setContinueToken(
          (res as { metadata?: { continue?: string } }).metadata?.continue,
        );
      } catch {
        setItems([]);
        setContinueToken(undefined);
      } finally {
        setLoading(false);
      }
    };
    fetchInstances();
  }, [isClient, listUrl]);

  // Load more handler
  const loadMore = async () => {
    if (!continueToken || loadingMoreRef.current) return;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    try {
      const res = await apiGet<CRDItem[]>(
        `${listUrl}&continue=${encodeURIComponent(continueToken)}`,
      );
      setItems((prev) => [...prev, ...(res.data ?? [])]);
      setContinueToken(
        (res as { metadata?: { continue?: string } }).metadata?.continue,
      );
    } catch {
      // keep existing items
    } finally {
      loadingMoreRef.current = false;
      setLoadingMore(false);
    }
  };

  // Delete handler
  const handleDelete = async (item: CRDItem) => {
    if (deletingRef.current) return;
    deletingRef.current = true;
    setDeleting(true);
    try {
      const ns = item.metadata.namespace;
      const nsPath = ns ? `/${ns}` : "";
      await apiDelete(
        `/v1/extensions/resources/${group}/${resource}${nsPath}/${item.metadata.name}`,
      );
      setItems((prev) =>
        prev.filter((i) => i.metadata.uid !== item.metadata.uid),
      );
      showToast(`Deleted ${item.metadata.name}`, "success");
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Delete failed";
      showToast(msg, "error");
    } finally {
      deletingRef.current = false;
      setDeleting(false);
      setConfirmDelete(null);
    }
  };

  // Close kebab menu on outside click
  useEffect(() => {
    if (!isClient) return;
    const handler = () => {
      setOpenMenu(null);
    };
    globalThis.document.addEventListener("click", handler);
    return () => globalThis.document.removeEventListener("click", handler);
  }, [isClient]);

  if (!isClient) {
    return <div {...ROOT_PROPS} />;
  }

  if (loading) {
    return (
      <div {...ROOT_PROPS}>
        <Skeleton className="h-5 w-48 mb-2" />
        <Skeleton className="h-4 w-72 mb-6" />
        <Skeleton className="h-10 w-full mb-4" />
        <Skeleton className="h-64 w-full rounded-lg" />
      </div>
    );
  }

  const isNamespaced = meta?.scope === "Namespaced";
  const kind = meta?.kind ?? resource;
  const version = meta?.version ?? "v1";
  const printerColumns: PrinterColumn[] = meta?.additionalPrinterColumns ?? [];

  // Filter items
  const query = search.toLowerCase().trim();
  const filtered = items.filter((item) => {
    // Name search
    if (query && !item.metadata.name.toLowerCase().includes(query)) {
      return false;
    }
    // Status filter
    if (statusFilter !== "all") {
      const status = getReadyStatus(item);
      if (statusFilter === "ready" && status !== "Ready") return false;
      if (statusFilter === "notready" && status !== "NotReady") {
        return false;
      }
    }
    return true;
  });

  const instanceLabel = resource.toLowerCase();

  return (
    <div {...ROOT_PROPS}>
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
            {items.length} instance{items.length !== 1 ? "s" : ""}
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
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
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
              onClick={() => setStatusFilter(val)}
              style={{
                padding: "4px 10px",
                fontSize: "11px",
                fontWeight: 500,
                borderRadius: "4px",
                border: "none",
                cursor: "pointer",
                background:
                  statusFilter === val ? "var(--accent)" : "transparent",
                color:
                  statusFilter === val ? "var(--bg-base)" : "var(--text-muted)",
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

      {/* Resource table — solid surface per archetype */}
      <div
        style={{
          flex: 1,
          minHeight: 0,
          overflow: "auto",
          background: "var(--bg-surface)",
          border: "1px solid var(--border-primary)",
          borderRadius: "16px",
        }}
      >
        {/* Header row */}
        <div
          style={{
            display: "grid",
            gridTemplateColumns: crdGridTemplate(isNamespaced, printerColumns),
            gap: "12px",
            padding: "11px 18px",
            borderBottom: "1px solid var(--border-subtle)",
            position: "sticky",
            top: 0,
            background: "var(--bg-surface)",
            zIndex: 1,
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.05em",
            color: "var(--text-muted)",
          }}
        >
          <span>Name</span>
          {isNamespaced && <span>Namespace</span>}
          <span>Status</span>
          {printerColumns.map((col) => (
            <span key={col.name} title={col.description}>
              {col.name}
            </span>
          ))}
          <span>Age</span>
          <span />
        </div>

        {/* Empty state */}
        {filtered.length === 0 && (
          <div
            style={{
              padding: "48px",
              textAlign: "center",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            {items.length === 0
              ? `No ${instanceLabel} found`
              : "No matching results"}
          </div>
        )}

        {/* Data rows */}
        {filtered.map((item) => {
          const status = getReadyStatus(item);
          // tone for the glass StatusBadge pill; dotStatus for the old StatusDot
          const statusTone =
            status === "Ready"
              ? "ok"
              : status === "NotReady"
                ? "crit"
                : ("neutral" as const);
          const dotStatus =
            status === "Ready"
              ? "success"
              : status === "NotReady"
                ? "error"
                : ("neutral" as const);
          const ns = item.metadata.namespace;
          const detailNs = ns ?? "_";
          const detailHref = `/extensions/${group}/${resource}/${detailNs}/${item.metadata.name}`;
          const menuId = item.metadata.uid;

          return (
            <div
              key={item.metadata.uid}
              style={{
                display: "grid",
                gridTemplateColumns: crdGridTemplate(
                  isNamespaced,
                  printerColumns,
                ),
                gap: "12px",
                padding: "12px 18px",
                borderBottom: "1px solid var(--border-subtle)",
                alignItems: "center",
                cursor: "pointer",
                transition: "background 120ms ease",
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLElement).style.background =
                  "var(--bg-hover)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLElement).style.background = "";
              }}
              onClick={() => {
                globalThis.location.href = detailHref;
              }}
            >
              {/* Name — StatusDot + monospace name */}
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  minWidth: 0,
                }}
              >
                <StatusDot status={dotStatus} size={7} />
                <a
                  href={detailHref}
                  onClick={(e) => e.stopPropagation()}
                  style={{
                    color: "var(--text-primary)",
                    fontFamily: "var(--font-mono)",
                    fontSize: "13px",
                    fontWeight: 500,
                    textDecoration: "none",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {item.metadata.name}
                </a>
              </div>

              {/* Namespace */}
              {isNamespaced && (
                <span
                  style={{
                    fontSize: "13px",
                    color: "var(--text-primary)",
                  }}
                >
                  {ns ?? "—"}
                </span>
              )}

              {/* Status badge */}
              <div>
                {status === "Unknown" ? (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    —
                  </span>
                ) : (
                  <StatusBadge
                    label={status === "Ready" ? "Ready" : "Not Ready"}
                    tone={statusTone}
                  />
                )}
              </div>

              {/* Printer columns */}
              {printerColumns.map((col) => {
                const val = extractJsonPath(item, col.jsonPath);
                return (
                  <span
                    key={col.name}
                    style={{
                      fontSize: "13px",
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-muted)",
                    }}
                  >
                    {val != null ? String(val) : "—"}
                  </span>
                );
              })}

              {/* Age */}
              <span
                style={{
                  fontSize: "13px",
                  color: "var(--text-muted)",
                  fontFamily: "var(--font-mono)",
                  fontVariantNumeric: "tabular-nums",
                }}
              >
                {age(item.metadata.creationTimestamp)}
              </span>

              {/* Kebab menu */}
              <div style={{ position: "relative", textAlign: "right" }}>
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    setOpenMenu((prev) => (prev === menuId ? null : menuId));
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
                {openMenu === menuId && (
                  <div
                    className="glass-elevated"
                    style={{
                      position: "absolute",
                      right: 0,
                      top: "100%",
                      borderRadius: "12px",
                      zIndex: 10,
                      minWidth: "110px",
                    }}
                  >
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        setOpenMenu(null);
                        setConfirmDelete(item);
                      }}
                      style={{
                        display: "block",
                        width: "100%",
                        padding: "9px 14px",
                        fontSize: "13px",
                        color: "var(--error)",
                        background: "none",
                        border: "none",
                        cursor: "pointer",
                        textAlign: "left",
                        borderRadius: "12px",
                      }}
                      onMouseEnter={(e) => {
                        (e.currentTarget as HTMLElement).style.background =
                          "var(--bg-hover)";
                      }}
                      onMouseLeave={(e) => {
                        (e.currentTarget as HTMLElement).style.background = "";
                      }}
                    >
                      Delete
                    </button>
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Load more */}
      {continueToken && (
        <div style={{ textAlign: "center", padding: "16px 0" }}>
          <button
            type="button"
            onClick={loadMore}
            disabled={loadingMore}
            style={{
              padding: "6px 20px",
              fontSize: "12px",
              fontWeight: 500,
              borderRadius: "var(--radius)",
              border: "1px solid var(--border-primary)",
              background: "var(--bg-surface)",
              color: "var(--text-primary)",
              cursor: loadingMore ? "default" : "pointer",
              opacity: loadingMore ? 0.6 : 1,
            }}
          >
            {loadingMore ? "Loading..." : "Load more"}
          </button>
        </div>
      )}

      {/* Delete confirmation dialog */}
      {confirmDelete && (
        <div
          className="glass-scrim"
          style={{
            position: "fixed",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 100,
          }}
          onClick={() => setConfirmDelete(null)}
        >
          <div
            className="glass-elevated"
            style={{
              borderRadius: "16px",
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
              <strong>{confirmDelete.metadata.name}</strong>?
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
                onClick={() => setConfirmDelete(null)}
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
                onClick={() => handleDelete(confirmDelete)}
                disabled={deleting}
                style={{
                  padding: "6px 14px",
                  fontSize: "12px",
                  borderRadius: "var(--radius)",
                  border: "none",
                  background: "var(--error)",
                  color: "var(--bg-base)",
                  cursor: deleting ? "default" : "pointer",
                  opacity: deleting ? 0.6 : 1,
                }}
              >
                {deleting ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * Build a CSS grid-template-columns string for the CRD resource table.
 * Name is 1.8fr, namespace (if present) 120px, status 100px,
 * each printer column 1fr, age 80px, kebab 40px.
 */
function crdGridTemplate(
  isNamespaced: boolean,
  printerColumns: PrinterColumn[],
): string {
  const cols: string[] = ["1.8fr"];
  if (isNamespaced) cols.push("120px");
  cols.push("100px");
  for (let i = 0; i < printerColumns.length; i++) cols.push("1fr");
  cols.push("80px", "40px");
  return cols.join(" ");
}
