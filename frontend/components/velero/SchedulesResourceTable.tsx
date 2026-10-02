import { Button } from "@/components/ui/Button.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import ResourceTable, {
  type Column,
  type Row,
} from "@/components/ui/ResourceTable.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { outcomeTone, phaseTone } from "@/components/velero/velero-utils.ts";
import { age } from "@/lib/format.ts";
import type { Schedule } from "@/lib/velero-types.ts";

const SCHEDULES_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "schedule", label: "Cron", width: "120px" },
  { key: "newestRun", label: "Newest Run", width: "150px" },
  { key: "lastBackup", label: "Last Backup", width: "100px", align: "right" },
  { key: "nextRun", label: "Next Run", width: "100px", align: "right" },
  { key: "actions", label: "", width: "80px" },
];

/**
 * Names the phase of a schedule's newest backup, when it has one. That
 * backup may be a manual trigger, so it is shown apart from "Last Backup",
 * which is Velero's status.lastBackup and only moves on cron runs.
 */
function newestBackupLabel(s: Schedule): string | undefined {
  return s.lastBackupPhase ? `Newest backup: ${s.lastBackupPhase}` : undefined;
}

export function SchedulesResourceTable({
  schedules,
  deleting,
  onDelete,
}: {
  schedules: Schedule[];
  deleting: string | null;
  onDelete: (ns: string, name: string) => void;
}) {
  if (schedules.length === 0) {
    return (
      <div
        style={{
          textAlign: "center",
          padding: "48px 0",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        No schedules found.
      </div>
    );
  }

  const rows: Row[] = schedules.map((s) => ({
    id: `${s.namespace}/${s.name}`,
    onClick: () => {
      globalThis.location.href = `/backup/schedules/${s.namespace}/${s.name}`;
    },
    cells: {
      name: (
        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <StatusDot status={phaseTone(s.paused ? "Paused" : s.phase)} />
          <div>
            <div
              style={{
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
                fontFamily: "var(--font-mono, monospace)",
              }}
            >
              {s.name}
            </div>
            <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>
              {s.namespace}
            </div>
          </div>
        </div>
      ),
      status: (
        <StatusBadge
          label={s.paused ? "Paused" : s.phase}
          tone={phaseTone(s.paused ? "Paused" : s.phase)}
        />
      ),
      schedule: (
        <span
          style={{
            fontSize: "12px",
            fontFamily: "var(--font-mono, monospace)",
            color: "var(--text-muted)",
          }}
        >
          {s.schedule}
        </span>
      ),
      newestRun: (
        <span
          title={newestBackupLabel(s)}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "6px",
            fontSize: "13px",
            color: "var(--text-muted)",
          }}
        >
          {s.lastBackupPhase ? (
            <>
              <StatusDot status={outcomeTone(s.lastBackupOutcome)} size={6} />
              <span class="sr-only">Newest backup:</span>
              {s.lastBackupPhase}
            </>
          ) : (
            "—"
          )}
        </span>
      ),
      lastBackup: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {s.lastBackup ? age(s.lastBackup) : "Never"}
        </span>
      ),
      nextRun: (
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {s.nextRunTime ? age(s.nextRunTime) : "—"}
        </span>
      ),
      actions: (
        <div onClick={(e) => e.stopPropagation()}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onDelete(s.namespace, s.name)}
            disabled={deleting === `schedule-${s.namespace}-${s.name}`}
          >
            {deleting === `schedule-${s.namespace}-${s.name}` ? "…" : "Delete"}
          </Button>
        </div>
      ),
    },
  }));

  return (
    <ResourceTable columns={SCHEDULES_COLUMNS} rows={rows} chevron={false} />
  );
}
