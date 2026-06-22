import { age } from "@/lib/format.ts";
import type { Backup } from "@/lib/velero-types.ts";
import ResourceTable, {
  type Column,
  type Row,
} from "@/components/ui/ResourceTable.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { phaseTone } from "@/components/velero/velero-utils.ts";

const BACKUPS_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "schedule", label: "Schedule", width: "1fr" },
  { key: "started", label: "Started", width: "80px", align: "right" },
  { key: "items", label: "Items", width: "80px", align: "right" },
  { key: "issues", label: "Issues", width: "80px", align: "right" },
  { key: "actions", label: "", width: "120px" },
];

export function BackupsResourceTable(
  { backups, deleting, onDelete }: {
    backups: Backup[];
    deleting: string | null;
    onDelete: (ns: string, name: string) => void;
  },
) {
  if (backups.length === 0) {
    return (
      <div
        style={{
          textAlign: "center",
          padding: "48px 0",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        No backups found.
      </div>
    );
  }

  const rows: Row[] = backups.map((b) => ({
    id: `${b.namespace}/${b.name}`,
    onClick: () => {
      globalThis.location.href = `/backup/backups/${b.namespace}/${b.name}`;
    },
    cells: {
      name: (
        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <StatusDot status={phaseTone(b.phase)} />
          <div>
            <div
              style={{
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
                fontFamily: "var(--font-mono, monospace)",
              }}
            >
              {b.name}
            </div>
            <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>
              {b.namespace}
            </div>
          </div>
        </div>
      ),
      status: <StatusBadge label={b.phase} tone={phaseTone(b.phase)} />,
      schedule: (
        <span
          style={{ fontSize: "13px", color: "var(--text-muted)" }}
        >
          {b.scheduleName || "—"}
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
          {b.startTime ? age(b.startTime) : "—"}
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
          {b.itemsBackedUp}/{b.totalItems}
        </span>
      ),
      issues: (b.warnings > 0 || b.errors > 0)
        ? (
          <span
            style={{
              fontSize: "13px",
              color: "var(--warning)",
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {b.warnings}W/{b.errors}E
          </span>
        )
        : (
          <span
            style={{ fontSize: "13px", color: "var(--success)" }}
          >
            0
          </span>
        ),
      actions: (
        <div
          style={{ display: "flex", gap: "4px" }}
          onClick={(e) => e.stopPropagation()}
        >
          <a href={`/backup/restores/new?backup=${b.name}`}>
            <Button type="button" variant="ghost" size="sm">
              Restore
            </Button>
          </a>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onDelete(b.namespace, b.name)}
            disabled={deleting === `backup-${b.namespace}-${b.name}`}
          >
            {deleting === `backup-${b.namespace}-${b.name}` ? "…" : "Delete"}
          </Button>
        </div>
      ),
    },
  }));

  return (
    <ResourceTable
      columns={BACKUPS_COLUMNS}
      rows={rows}
      chevron={false}
    />
  );
}
