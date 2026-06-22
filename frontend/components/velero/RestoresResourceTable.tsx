import { age } from "@/lib/format.ts";
import type { Restore } from "@/lib/velero-types.ts";
import ResourceTable, {
  type Column,
  type Row,
} from "@/components/ui/ResourceTable.tsx";
import StatusBadge, { StatusDot } from "@/components/ui/glass/StatusBadge.tsx";
import { phaseTone } from "@/components/velero/velero-utils.ts";

const RESTORES_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "backup", label: "Backup", width: "1fr" },
  { key: "started", label: "Started", width: "80px", align: "right" },
  { key: "items", label: "Items", width: "80px", align: "right" },
  { key: "issues", label: "Issues", width: "80px", align: "right" },
];

export function RestoresResourceTable({ restores }: { restores: Restore[] }) {
  if (restores.length === 0) {
    return (
      <div
        style={{
          textAlign: "center",
          padding: "48px 0",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        No restores found.
      </div>
    );
  }

  const rows: Row[] = restores.map((r) => ({
    id: `${r.namespace}/${r.name}`,
    onClick: () => {
      globalThis.location.href = `/backup/restores/${r.namespace}/${r.name}`;
    },
    cells: {
      name: (
        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <StatusDot tone={phaseTone(r.phase)} />
          <div>
            <div
              style={{
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
                fontFamily: "var(--font-mono, monospace)",
              }}
            >
              {r.name}
            </div>
            <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>
              {r.namespace}
            </div>
          </div>
        </div>
      ),
      status: <StatusBadge label={r.phase} tone={phaseTone(r.phase)} />,
      backup: (
        <span
          style={{ fontSize: "13px", color: "var(--text-muted)" }}
        >
          {r.backupName || r.scheduleName || "—"}
        </span>
      ),
      started: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {r.startTime ? age(r.startTime) : "—"}
        </span>
      ),
      items: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {r.itemsRestored}/{r.totalItems}
        </span>
      ),
      issues: (r.warnings > 0 || r.errors > 0)
        ? (
          <span
            style={{
              fontSize: "13px",
              color: "var(--warning)",
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {r.warnings}W/{r.errors}E
          </span>
        )
        : (
          <span
            style={{ fontSize: "13px", color: "var(--success)" }}
          >
            0
          </span>
        ),
    },
  }));

  return <ResourceTable columns={RESTORES_COLUMNS} rows={rows} />;
}
