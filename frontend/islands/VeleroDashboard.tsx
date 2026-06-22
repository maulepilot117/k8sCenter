import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiDelete, apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { age } from "@/lib/format.ts";
import ResourceTable, {
  type Column,
  type Row,
} from "@/components/ui/ResourceTable.tsx";
import StatusBadge, {
  StatusDot,
  type Tone,
} from "@/components/ui/glass/StatusBadge.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import Donut from "@/components/charts/Donut.tsx";
import BarRow from "@/components/charts/BarRow.tsx";
import type {
  Backup,
  LocationsResponse,
  Restore,
  Schedule,
  VeleroStatus,
} from "@/lib/velero-types.ts";
import { getPhaseCategory } from "@/lib/velero-types.ts";
import VeleroBackupWizard from "@/islands/VeleroBackupWizard.tsx";
import VeleroRestoreWizard from "@/islands/VeleroRestoreWizard.tsx";
import VeleroScheduleWizard from "@/islands/VeleroScheduleWizard.tsx";

type Tab = "overview" | "backups" | "restores" | "schedules";

interface Props {
  initialTab?: Tab;
}

/** Map Velero phase category → StatusBadge tone. */
function phaseTone(phase: string): Tone {
  const cat = getPhaseCategory(phase);
  if (cat === "success") return "ok";
  if (cat === "error") return "crit";
  if (cat === "warning") return "warn";
  if (cat === "progress") return "info";
  return "neutral";
}

// ---------------------------------------------------------------------------
// Shared data-fetch hook
// ---------------------------------------------------------------------------

export default function VeleroDashboard({ initialTab = "backups" }: Props) {
  const status = useSignal<VeleroStatus | null>(null);
  const backups = useSignal<Backup[]>([]);
  const restores = useSignal<Restore[]>([]);
  const schedules = useSignal<Schedule[]>([]);
  const locations = useSignal<LocationsResponse | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const refreshing = useSignal(false);
  const deleting = useSignal<string | null>(null);

  const backupWizardOpen = useSignal(false);
  const restoreWizardOpen = useSignal(false);
  const scheduleWizardOpen = useSignal(false);

  async function fetchData() {
    try {
      const [statusRes, backupsRes, restoresRes, schedulesRes, locationsRes] =
        await Promise.all([
          apiGet<VeleroStatus>("/v1/velero/status"),
          apiGet<Backup[]>("/v1/velero/backups"),
          apiGet<Restore[]>("/v1/velero/restores"),
          apiGet<Schedule[]>("/v1/velero/schedules"),
          apiGet<LocationsResponse>("/v1/velero/locations"),
        ]);
      status.value = statusRes.data;
      backups.value = Array.isArray(backupsRes.data) ? backupsRes.data : [];
      restores.value = Array.isArray(restoresRes.data) ? restoresRes.data : [];
      schedules.value = Array.isArray(schedulesRes.data)
        ? schedulesRes.data
        : [];
      locations.value = locationsRes.data;
      error.value = null;
    } catch {
      error.value = "Failed to load Velero data";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") {
      if (initialTab === "backups") backupWizardOpen.value = true;
      else if (initialTab === "restores") restoreWizardOpen.value = true;
      else if (initialTab === "schedules") scheduleWizardOpen.value = true;
    }
  }, []);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  async function handleDeleteBackup(ns: string, name: string) {
    if (
      !confirm(`Delete backup "${name}"? This will remove the backup data.`)
    ) {
      return;
    }
    deleting.value = `backup-${ns}-${name}`;
    try {
      await apiDelete(`/v1/velero/backups/${ns}/${name}`);
      await fetchData();
    } catch {
      alert("Failed to delete backup");
    }
    deleting.value = null;
  }

  async function handleDeleteSchedule(ns: string, name: string) {
    if (!confirm(`Delete schedule "${name}"?`)) return;
    deleting.value = `schedule-${ns}-${name}`;
    try {
      await apiDelete(`/v1/velero/schedules/${ns}/${name}`);
      await fetchData();
    } catch {
      alert("Failed to delete schedule");
    }
    deleting.value = null;
  }

  if (!IS_BROWSER) return null;

  const notDetected = status.value && !status.value.detected;

  // Filter items by search
  const filteredBackups = backups.value.filter((b) =>
    !search.value ||
    b.name.toLowerCase().includes(search.value.toLowerCase()) ||
    (b.scheduleName ?? "").toLowerCase().includes(search.value.toLowerCase())
  );
  const filteredRestores = restores.value.filter((r) =>
    !search.value ||
    r.name.toLowerCase().includes(search.value.toLowerCase()) ||
    (r.backupName ?? "").toLowerCase().includes(search.value.toLowerCase())
  );
  const filteredSchedules = schedules.value.filter((s) =>
    !search.value || s.name.toLowerCase().includes(search.value.toLowerCase())
  );

  // ---------------------------------------------------------------------------
  // Page header labels
  // ---------------------------------------------------------------------------
  const titles: Record<Tab, string> = {
    overview: "Backup & Restore",
    backups: "Backups",
    restores: "Restores",
    schedules: "Schedules",
  };
  const subtitles: Record<Tab, string> = {
    overview: "Velero backup and restore management",
    backups: loading.value
      ? "Loading…"
      : `${backups.value.length} backup${
        backups.value.length !== 1 ? "s" : ""
      }`,
    restores: loading.value
      ? "Loading…"
      : `${restores.value.length} restore${
        restores.value.length !== 1 ? "s" : ""
      }`,
    schedules: loading.value
      ? "Loading…"
      : `${schedules.value.length} schedule${
        schedules.value.length !== 1 ? "s" : ""
      }`,
  };
  const createLabels: Partial<Record<Tab, string>> = {
    backups: "New Backup",
    restores: "New Restore",
    schedules: "New Schedule",
  };

  const pageTitle = titles[initialTab];
  const pageSubtitle = subtitles[initialTab];
  const createLabel = createLabels[initialTab];

  return (
    <div
      style={{
        padding: "24px",
        display: "flex",
        flexDirection: "column",
        gap: "20px",
      }}
    >
      {/* ── Page header ──────────────────────────────────────────── */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: "16px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            {pageTitle}
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            {pageSubtitle}
          </p>
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            flexShrink: 0,
          }}
        >
          {!loading.value && createLabel && (
            <Button
              type="button"
              variant="primary"
              onClick={() => {
                if (initialTab === "backups") backupWizardOpen.value = true;
                else if (initialTab === "restores") {
                  restoreWizardOpen.value = true;
                } else if (initialTab === "schedules") {
                  scheduleWizardOpen.value = true;
                }
              }}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                stroke-width="2.5"
                style={{ marginRight: "4px", verticalAlign: "middle" }}
              >
                <path d="M4 8h8M8 4v8" />
              </svg>
              {createLabel}
            </Button>
          )}
          {!loading.value && (
            <Button
              type="button"
              variant="ghost"
              onClick={handleRefresh}
              disabled={refreshing.value}
            >
              {refreshing.value ? "Refreshing…" : "Refresh"}
            </Button>
          )}
        </div>
      </div>

      {/* ── Loading ──────────────────────────────────────────────── */}
      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner />
        </div>
      )}

      {/* ── Error ────────────────────────────────────────────────── */}
      {error.value && (
        <div
          style={{
            borderRadius: "9px",
            border:
              "1px solid color-mix(in srgb, var(--error) 30%, transparent)",
            background: "color-mix(in srgb, var(--error) 10%, transparent)",
            padding: "12px 16px",
            fontSize: "13px",
            color: "var(--error)",
          }}
        >
          {error.value}
        </div>
      )}

      {/* ── Velero not detected ──────────────────────────────────── */}
      {notDetected && !loading.value && (
        <WidgetShell title="Velero Not Detected">
          <div style={{ textAlign: "center", padding: "16px 0" }}>
            <p
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                margin: "0 0 16px",
              }}
            >
              Velero CRDs were not found in this cluster. Install Velero to
              enable backup and restore functionality.
            </p>
            <a
              href="https://velero.io/docs/v1.12/basic-install/"
              target="_blank"
              rel="noopener noreferrer"
              style={{ fontSize: "13px", color: "var(--accent)" }}
              class="hover:underline"
            >
              View Velero Installation Docs &rarr;
            </a>
          </div>
        </WidgetShell>
      )}

      {/* ── Main content (Velero detected, not loading) ──────────── */}
      {!loading.value && !error.value && status.value?.detected && (
        <>
          {initialTab === "overview" && (
            <VeleroOverview
              backups={backups.value}
              restores={restores.value}
              schedules={schedules.value}
              locations={locations.value}
              status={status.value}
            />
          )}

          {initialTab !== "overview" && (
            <>
              {/* Search bar */}
              <div style={{ maxWidth: "320px" }}>
                <SearchBar
                  value={search.value}
                  onInput={(v) => (search.value = v)}
                  placeholder="Search…"
                />
              </div>

              {initialTab === "backups" && (
                <BackupsResourceTable
                  backups={filteredBackups}
                  deleting={deleting.value}
                  onDelete={handleDeleteBackup}
                />
              )}
              {initialTab === "restores" && (
                <RestoresResourceTable restores={filteredRestores} />
              )}
              {initialTab === "schedules" && (
                <SchedulesResourceTable
                  schedules={filteredSchedules}
                  deleting={deleting.value}
                  onDelete={handleDeleteSchedule}
                />
              )}
            </>
          )}
        </>
      )}

      {/* ── Wizard modals ─────────────────────────────────────────── */}
      {backupWizardOpen.value && (
        <VeleroBackupWizard onClose={() => (backupWizardOpen.value = false)} />
      )}
      {restoreWizardOpen.value && (
        <VeleroRestoreWizard
          onClose={() => (restoreWizardOpen.value = false)}
        />
      )}
      {scheduleWizardOpen.value && (
        <VeleroScheduleWizard
          onClose={() => (scheduleWizardOpen.value = false)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview — glass WidgetShell cards
// ---------------------------------------------------------------------------

function VeleroOverview(
  { backups, restores, schedules, locations, status }: {
    backups: Backup[];
    restores: Restore[];
    schedules: Schedule[];
    locations: LocationsResponse | null;
    status: VeleroStatus;
  },
) {
  // Donut segments for backups by phase category
  const successBackups =
    backups.filter((b) => getPhaseCategory(b.phase) === "success").length;
  const failedBackups =
    backups.filter((b) => getPhaseCategory(b.phase) === "error").length;
  const warnBackups =
    backups.filter((b) => getPhaseCategory(b.phase) === "warning").length;
  const inProgressBackups =
    backups.filter((b) => getPhaseCategory(b.phase) === "progress").length;
  const otherBackups = backups.length -
    successBackups -
    failedBackups -
    warnBackups -
    inProgressBackups;

  const backupDonutSegments = [
    ...(successBackups > 0
      ? [{ value: successBackups, color: "var(--success)", label: "Completed" }]
      : []),
    ...(failedBackups > 0
      ? [{ value: failedBackups, color: "var(--error)", label: "Failed" }]
      : []),
    ...(warnBackups > 0
      ? [{ value: warnBackups, color: "var(--warning)", label: "Partial" }]
      : []),
    ...(inProgressBackups > 0
      ? [{
        value: inProgressBackups,
        color: "var(--info)",
        label: "In Progress",
      }]
      : []),
    ...(otherBackups > 0
      ? [{ value: otherBackups, color: "var(--bg-elevated)", label: "Other" }]
      : []),
    // Placeholder when no backups
    ...(backups.length === 0
      ? [{ value: 1, color: "var(--bg-elevated)" }]
      : []),
  ];

  const bslCount = locations?.backupStorageLocations.length ?? status.bslCount;
  const vslCount = locations?.volumeSnapshotLocations.length ?? status.vslCount;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "20px",
      }}
    >
      {/* Row 1: 2×2 KPI tiles */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "12px",
        }}
      >
        <KpiTile
          label="Backups"
          value={backups.length}
          color="var(--accent)"
          href="/backup/backups"
        />
        <KpiTile
          label="Restores"
          value={restores.length}
          color="var(--success)"
          href="/backup/restores"
        />
        <KpiTile
          label="Schedules"
          value={schedules.length}
          color="var(--warning)"
          href="/backup/schedules"
        />
        <KpiTile
          label="Storage Locations"
          value={bslCount}
          color="var(--info)"
        />
      </div>

      {/* Row 2: Backup health donut + Storage location list */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "12px",
        }}
      >
        {/* Backup health donut */}
        <WidgetShell
          title="Backup Health"
          style={{ flex: "1 1 260px", minWidth: "220px" }}
        >
          <div
            style={{ display: "flex", alignItems: "center", gap: "24px" }}
          >
            <Donut
              segments={backupDonutSegments}
              size={96}
              thickness={14}
              center={
                <div style={{ textAlign: "center" }}>
                  <div
                    style={{
                      fontSize: "20px",
                      fontWeight: 700,
                      fontVariantNumeric: "tabular-nums",
                      color: "var(--text-primary)",
                    }}
                  >
                    {backups.length}
                  </div>
                  <div
                    style={{
                      fontSize: "10px",
                      fontWeight: 600,
                      letterSpacing: "0.05em",
                      textTransform: "uppercase",
                      color: "var(--text-muted)",
                    }}
                  >
                    total
                  </div>
                </div>
              }
            />
            <div
              style={{ display: "flex", flexDirection: "column", gap: "6px" }}
            >
              <LegendRow
                color="var(--success)"
                label="Completed"
                count={successBackups}
              />
              <LegendRow
                color="var(--error)"
                label="Failed"
                count={failedBackups}
              />
              <LegendRow
                color="var(--warning)"
                label="Partial"
                count={warnBackups}
              />
              <LegendRow
                color="var(--info)"
                label="In Progress"
                count={inProgressBackups}
              />
            </div>
          </div>
        </WidgetShell>

        {/* Resource counts bar chart */}
        <WidgetShell
          title="Velero Resources"
          style={{ flex: "2 1 300px", minWidth: "260px" }}
        >
          <div style={{ paddingTop: "4px" }}>
            <BarRow
              label="Backups"
              value={backups.length}
              max={Math.max(
                backups.length,
                restores.length,
                schedules.length,
                1,
              )}
              suffix={String(backups.length)}
              color="var(--accent)"
            />
            <BarRow
              label="Restores"
              value={restores.length}
              max={Math.max(
                backups.length,
                restores.length,
                schedules.length,
                1,
              )}
              suffix={String(restores.length)}
              color="var(--success)"
            />
            <BarRow
              label="Schedules"
              value={schedules.length}
              max={Math.max(
                backups.length,
                restores.length,
                schedules.length,
                1,
              )}
              suffix={String(schedules.length)}
              color="var(--warning)"
            />
            <BarRow
              label="BSLs"
              value={bslCount}
              max={Math.max(bslCount, vslCount, 1)}
              suffix={String(bslCount)}
              color="var(--info)"
            />
          </div>
        </WidgetShell>
      </div>

      {/* Row 3: Recent backups list */}
      {backups.length > 0 && (
        <WidgetShell
          title="Recent Backups"
          action={
            <a
              href="/backup/backups"
              style={{ fontSize: "12px", color: "var(--accent)" }}
              class="hover:underline"
            >
              View all &rarr;
            </a>
          }
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
            {backups.slice(0, 5).map((b) => (
              <a
                key={`${b.namespace}/${b.name}`}
                href={`/backup/backups/${b.namespace}/${b.name}`}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "10px",
                  padding: "8px 0",
                  borderBottom:
                    "1px solid color-mix(in srgb, var(--border-primary) 50%, transparent)",
                  textDecoration: "none",
                }}
                class="hover:opacity-80"
              >
                <StatusDot tone={phaseTone(b.phase)} />
                <span
                  style={{
                    flex: 1,
                    fontSize: "13px",
                    fontWeight: 500,
                    color: "var(--text-primary)",
                    fontFamily: "var(--font-mono, monospace)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {b.name}
                </span>
                <StatusBadge label={b.phase} tone={phaseTone(b.phase)} />
                <span
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    fontVariantNumeric: "tabular-nums",
                    flexShrink: 0,
                  }}
                >
                  {b.startTime ? age(b.startTime) : "—"}
                </span>
              </a>
            ))}
          </div>
        </WidgetShell>
      )}

      {/* Row 4: Storage locations */}
      {locations && locations.backupStorageLocations.length > 0 && (
        <WidgetShell title="Backup Storage Locations">
          <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
            {locations.backupStorageLocations.map((bsl) => (
              <div
                key={`${bsl.namespace}/${bsl.name}`}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "10px",
                  padding: "8px 0",
                  borderBottom:
                    "1px solid color-mix(in srgb, var(--border-primary) 50%, transparent)",
                }}
              >
                <StatusDot tone={phaseTone(bsl.phase)} />
                <span
                  style={{
                    flex: 1,
                    fontSize: "13px",
                    fontWeight: 500,
                    color: "var(--text-primary)",
                    fontFamily: "var(--font-mono, monospace)",
                  }}
                >
                  {bsl.name}
                </span>
                {bsl.default && (
                  <span
                    style={{
                      fontSize: "10px",
                      fontWeight: 600,
                      letterSpacing: "0.05em",
                      textTransform: "uppercase",
                      color: "var(--text-muted)",
                    }}
                  >
                    default
                  </span>
                )}
                <span
                  style={{ fontSize: "12px", color: "var(--text-muted)" }}
                >
                  {bsl.provider}
                </span>
                <StatusBadge label={bsl.phase} tone={phaseTone(bsl.phase)} />
              </div>
            ))}
          </div>
        </WidgetShell>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Helper sub-components
// ---------------------------------------------------------------------------

function KpiTile(
  { label, value, color, href }: {
    label: string;
    value: number;
    color: string;
    href?: string;
  },
) {
  const inner = (
    <WidgetShell style={{ flex: "1 1 140px", minWidth: "120px" }}>
      <div
        style={{
          fontSize: "24px",
          fontWeight: 700,
          fontVariantNumeric: "tabular-nums",
          color,
          marginBottom: "4px",
        }}
      >
        {value}
      </div>
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          letterSpacing: "0.05em",
          textTransform: "uppercase",
          color: "var(--text-muted)",
        }}
      >
        {label}
      </div>
    </WidgetShell>
  );

  return href
    ? (
      <a href={href} style={{ display: "contents", textDecoration: "none" }}>
        {inner}
      </a>
    )
    : inner;
}

function LegendRow(
  { color, label, count }: {
    color: string;
    label: string;
    count: number;
  },
) {
  return (
    <div
      style={{ display: "flex", alignItems: "center", gap: "7px" }}
    >
      <span
        style={{
          width: "8px",
          height: "8px",
          borderRadius: "50%",
          background: color,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          fontSize: "12px",
          color: "var(--text-muted)",
          minWidth: "72px",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: "12px",
          fontWeight: 600,
          fontVariantNumeric: "tabular-nums",
          color: "var(--text-primary)",
        }}
      >
        {count}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// List tables — ResourceTable component
// ---------------------------------------------------------------------------

const BACKUPS_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "schedule", label: "Schedule", width: "1fr" },
  { key: "started", label: "Started", width: "80px", align: "right" },
  { key: "items", label: "Items", width: "80px", align: "right" },
  { key: "issues", label: "Issues", width: "80px", align: "right" },
  { key: "actions", label: "", width: "120px" },
];

function BackupsResourceTable(
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
          <StatusDot tone={phaseTone(b.phase)} />
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

const RESTORES_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "backup", label: "Backup", width: "1fr" },
  { key: "started", label: "Started", width: "80px", align: "right" },
  { key: "items", label: "Items", width: "80px", align: "right" },
  { key: "issues", label: "Issues", width: "80px", align: "right" },
];

function RestoresResourceTable({ restores }: { restores: Restore[] }) {
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

const SCHEDULES_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "status", label: "Status", width: "120px" },
  { key: "schedule", label: "Cron", width: "120px" },
  { key: "lastBackup", label: "Last Backup", width: "100px", align: "right" },
  { key: "nextRun", label: "Next Run", width: "100px", align: "right" },
  { key: "actions", label: "", width: "80px" },
];

function SchedulesResourceTable(
  { schedules, deleting, onDelete }: {
    schedules: Schedule[];
    deleting: string | null;
    onDelete: (ns: string, name: string) => void;
  },
) {
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
          <StatusDot
            tone={phaseTone(s.paused ? "Paused" : s.phase)}
          />
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
        <div
          onClick={(e) => e.stopPropagation()}
        >
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
    <ResourceTable
      columns={SCHEDULES_COLUMNS}
      rows={rows}
      chevron={false}
    />
  );
}
