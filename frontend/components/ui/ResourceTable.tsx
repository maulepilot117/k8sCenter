import type { ComponentChildren } from "preact";

export interface Column {
  key: string;
  label: string;
  /** CSS track size, e.g. "1.6fr" | "120px". Default "1fr". */
  width?: string;
  align?: "left" | "right";
  /** when true (and onSort is provided) the header toggles sort on this key */
  sortable?: boolean;
}

export interface Row {
  id: string;
  cells: Record<string, ComponentChildren>;
  onClick?: () => void;
}

interface ResourceTableProps {
  columns: Column[];
  rows: Row[];
  /** show the trailing chevron affordance (default true when rows are clickable) */
  chevron?: boolean;
  /** active sort column key (enables header sort affordance with onSort) */
  sortKey?: string;
  sortDir?: "asc" | "desc";
  /** called with a column key when a sortable header is clicked */
  onSort?: (key: string) => void;
}

/**
 * Generic resource list table for all 37 resource kinds.
 *
 * Solid surface (var(--bg-surface)) — NOT glass — per the app's rule that
 * data-dense surfaces stay opaque for GPU cost + WCAG contrast. Sticky-ish
 * header row, hover highlight, row → detail navigation.
 *
 * Compose cells with status pills / dots from your existing components.
 */
export default function ResourceTable(
  { columns, rows, chevron = true, sortKey, sortDir, onSort }:
    ResourceTableProps,
) {
  const grid = columns.map((c) => c.width ?? "1fr").join(" ") +
    (chevron ? " 40px" : "");

  return (
    <div
      role="table"
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "16px",
        overflow: "hidden",
      }}
    >
      {/* header */}
      <div
        role="row"
        style={{
          display: "grid",
          gridTemplateColumns: grid,
          gap: "12px",
          padding: "11px 18px",
          borderBottom: "1px solid var(--border-subtle)",
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.05em",
          color: "var(--text-muted)",
        }}
      >
        {columns.map((c) => {
          const canSort = !!onSort && c.sortable;
          const active = canSort && sortKey === c.key;
          return (
            <span
              key={c.key}
              role="columnheader"
              aria-sort={active
                ? (sortDir === "asc" ? "ascending" : "descending")
                : undefined}
              onClick={canSort ? () => onSort(c.key) : undefined}
              style={{
                textAlign: c.align ?? "left",
                cursor: canSort ? "pointer" : "default",
                userSelect: "none",
                color: active ? "var(--text-secondary)" : undefined,
                justifyContent: c.align === "right" ? "flex-end" : "flex-start",
                display: "inline-flex",
                alignItems: "center",
                gap: "4px",
              }}
            >
              {c.label}
              {active && (
                <span aria-hidden="true">{sortDir === "asc" ? "▲" : "▼"}</span>
              )}
            </span>
          );
        })}
        {chevron && <span role="columnheader" aria-hidden="true" />}
      </div>

      {/* rows */}
      {rows.map((r) => (
        <div
          key={r.id}
          role="row"
          onClick={r.onClick}
          onMouseEnter={(
            e,
          ) => ((e.currentTarget as HTMLElement).style.background =
            "var(--bg-hover)")}
          onMouseLeave={(
            e,
          ) => ((e.currentTarget as HTMLElement).style.background =
            "transparent")}
          style={{
            display: "grid",
            gridTemplateColumns: grid,
            gap: "12px",
            padding: "13px 18px",
            borderBottom: "1px solid var(--border-subtle)",
            alignItems: "center",
            cursor: r.onClick ? "pointer" : "default",
            transition: "background 120ms ease",
          }}
        >
          {columns.map((c) => (
            <div
              key={c.key}
              role="cell"
              style={{ textAlign: c.align ?? "left", minWidth: 0 }}
            >
              {r.cells[c.key]}
            </div>
          ))}
          {chevron && (
            <svg
              role="cell"
              aria-hidden="true"
              width="16"
              height="16"
              viewBox="0 0 20 20"
              fill="none"
              stroke="var(--text-muted)"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M7 5l5 5-5 5" />
            </svg>
          )}
        </div>
      ))}
    </div>
  );
}
