import type { ReactNode } from "react";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import ResourceTable, {
  type Column,
  type Row,
} from "@/components/ui/ResourceTable.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { phaseTone } from "@/components/velero/velero-utils.ts";
import { age } from "@/lib/format.ts";
import type { RubrikInput } from "@/lib/protection-overview.ts";
import type { RubrikCompliance, RubrikObject } from "@/lib/rubrik-types.ts";
import type { Backup } from "@/lib/velero-types.ts";

/**
 * One Rubrik object on the Backups page: a namespace or a protection set,
 * with its SLA domain, compliance and newest snapshot. `panelNamespace` is
 * a namespace whose Rubrik detail lists the object, so its snapshots can be
 * opened; absent when no namespace row the user can see names it.
 */
export interface RubrikBackupRow {
  id: string;
  name: string;
  /** "Namespace", or "Protection set" with what it covers. */
  kind: string;
  sla: string;
  compliance: RubrikCompliance;
  newestSnapshot?: string;
  relic: boolean;
  panelNamespace?: string;
}

/** What a protection set covers, in a few words. */
function covers(o: RubrikObject): string {
  if (o.coverage.all) return "every namespace";
  if (o.coverage.unresolved) return "namespaces RSC does not resolve";
  const ns = o.coverage.namespaces ?? [];
  return ns.length > 0 ? ns.join(", ") : "no namespace";
}

/**
 * The Rubrik rows for the Backups page: every object the Rubrik answer
 * shows the user, in its order. Anything but a loaded answer has none; the
 * page explains those states on its own.
 */
export function rubrikBackupRows(input: RubrikInput): RubrikBackupRow[] {
  if (input.kind !== "loaded" || input.list.noAccess) return [];
  const { namespaces, objects } = input.list;
  return objects.map((o) => ({
    id: o.id,
    name: o.name,
    kind:
      o.family === "protectionSet"
        ? `Protection set · covers ${covers(o)}`
        : "Namespace",
    sla: o.effectiveSla?.name || "No SLA",
    compliance: o.compliance,
    newestSnapshot: o.newestSnapshot,
    relic: !!o.relic,
    panelNamespace: namespaces.find((r) => r.objectIds.includes(o.id))
      ?.namespace,
  }));
}

const COMPLIANCE: Record<
  RubrikCompliance,
  { label: string; tone: "ok" | "crit" | "neutral" }
> = {
  in_compliance: { label: "In compliance", tone: "ok" },
  out_of_compliance: { label: "Out of compliance", tone: "crit" },
  unknown: { label: "Unknown", tone: "neutral" },
};

const BACKUPS_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "provider", label: "Provider", width: "90px" },
  { key: "status", label: "Status", width: "120px" },
  { key: "schedule", label: "Schedule", width: "1fr" },
  { key: "started", label: "Started", width: "80px", align: "right" },
  { key: "items", label: "Items", width: "80px", align: "right" },
  { key: "issues", label: "Issues", width: "80px", align: "right" },
  { key: "actions", label: "", width: "120px" },
];

export function BackupsResourceTable({
  backups,
  deleting,
  onDelete,
  rubrik = [],
  onOpenRubrik,
}: {
  backups: Backup[];
  deleting: string | null;
  onDelete: (ns: string, name: string) => void;
  /** Rubrik objects shown with the Velero backups, a provider each. */
  rubrik?: RubrikBackupRow[];
  /** Opens the Rubrik detail of a namespace (its snapshots). */
  onOpenRubrik?: (namespace: string) => void;
}) {
  if (backups.length === 0 && rubrik.length === 0) {
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
      provider: <Muted>Velero</Muted>,
      status: <StatusBadge label={b.phase} tone={phaseTone(b.phase)} />,
      schedule: (
        <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
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
      issues:
        b.warnings > 0 || b.errors > 0 ? (
          <span
            style={{
              fontSize: "13px",
              color: "var(--warning)",
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {b.warnings}W/{b.errors}E
          </span>
        ) : (
          <span style={{ fontSize: "13px", color: "var(--success)" }}>0</span>
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

  for (const r of rubrik) {
    const badge = COMPLIANCE[r.compliance] ?? COMPLIANCE.unknown;
    const panel = r.panelNamespace;
    rows.push({
      id: `rubrik/${r.id}`,
      cells: {
        name: (
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
              {r.kind}
              {r.relic ? " · no longer seen by RSC" : ""}
            </div>
          </div>
        ),
        provider: <Muted>Rubrik</Muted>,
        status: <StatusBadge label={badge.label} tone={badge.tone} />,
        schedule: <Muted>SLA {r.sla}</Muted>,
        started: (
          <Muted>{r.newestSnapshot ? age(r.newestSnapshot) : "—"}</Muted>
        ),
        items: <Muted>—</Muted>,
        issues: <Muted>—</Muted>,
        actions:
          panel && onOpenRubrik ? (
            <div
              style={{ display: "flex", gap: "4px" }}
              onClick={(e) => e.stopPropagation()}
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={`Snapshots of ${r.name}`}
                onClick={() => onOpenRubrik(panel)}
              >
                Snapshots
              </Button>
            </div>
          ) : null,
      },
    });
  }

  return (
    <ResourceTable columns={BACKUPS_COLUMNS} rows={rows} chevron={false} />
  );
}

function Muted({ children }: { children: ReactNode }) {
  return (
    <span
      style={{
        fontSize: "13px",
        color: "var(--text-muted)",
        fontVariantNumeric: "tabular-nums",
      }}
    >
      {children}
    </span>
  );
}
