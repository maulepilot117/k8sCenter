/** @jsxImportSource react */
import { useEffect, useState } from "react";
import BarRow from "@/components/charts/BarRow.tsx";
import Donut from "@/components/charts/Donut.tsx";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { KpiTile } from "@/components/ui/KpiTile.tsx";
import { LegendRow } from "@/components/ui/LegendRow.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { BackupsResourceTable } from "@/components/velero/BackupsResourceTable.tsx";
import { RestoresResourceTable } from "@/components/velero/RestoresResourceTable.tsx";
import { SchedulesResourceTable } from "@/components/velero/SchedulesResourceTable.tsx";
import { phaseTone } from "@/components/velero/velero-utils.ts";
import { ApiError, apiDelete, apiGet } from "@/lib/api.ts";
import {
  type AssuranceStatus,
  countsAreCurrent,
  HONESTY_TEXT,
  surfaceStateCopy,
  surfaceStateFor,
} from "@/lib/backup-assurance-types.ts";
import { age } from "@/lib/format.ts";
import type {
  Backup,
  LocationsResponse,
  Restore,
  Schedule,
  VeleroStatus,
} from "@/lib/velero-types.ts";
import { getPhaseCategory } from "@/lib/velero-types.ts";
import VeleroBackupWizard from "@/src/islands/VeleroBackupWizard.tsx";
import VeleroRestoreWizard from "@/src/islands/VeleroRestoreWizard.tsx";
import VeleroScheduleWizard from "@/src/islands/VeleroScheduleWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

type Tab = "overview" | "backups" | "restores" | "schedules";

interface Props {
  initialTab?: Tab;
}

// ---------------------------------------------------------------------------
// Shared data-fetch hook
// ---------------------------------------------------------------------------

export default function VeleroDashboard({ initialTab = "backups" }: Props) {
  const isClient = useIsClient();
  const [status, setStatus] = useState<VeleroStatus | null>(null);
  const [backups, setBackups] = useState<Backup[]>([]);
  const [restores, setRestores] = useState<Restore[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [locations, setLocations] = useState<LocationsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  const [backupWizardOpen, setBackupWizardOpen] = useState(false);
  const [restoreWizardOpen, setRestoreWizardOpen] = useState(false);
  const [scheduleWizardOpen, setScheduleWizardOpen] = useState(false);

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
      setStatus(statusRes.data);
      setBackups(Array.isArray(backupsRes.data) ? backupsRes.data : []);
      setRestores(Array.isArray(restoresRes.data) ? restoresRes.data : []);
      setSchedules(Array.isArray(schedulesRes.data) ? schedulesRes.data : []);
      setLocations(locationsRes.data);
      setError(null);
    } catch {
      setError("Failed to load Velero data");
    }
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchData` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reads the URL once on mount; `initialTab` is fixed for the page
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") {
      if (initialTab === "backups") setBackupWizardOpen(true);
      else if (initialTab === "restores") setRestoreWizardOpen(true);
      else if (initialTab === "schedules") setScheduleWizardOpen(true);
    }
  }, []);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  async function handleDeleteBackup(ns: string, name: string) {
    if (
      !confirm(`Delete backup "${name}"? This will remove the backup data.`)
    ) {
      return;
    }
    setDeleting(`backup-${ns}-${name}`);
    try {
      await apiDelete(`/v1/velero/backups/${ns}/${name}`);
      await fetchData();
    } catch {
      alert("Failed to delete backup");
    }
    setDeleting(null);
  }

  async function handleDeleteSchedule(ns: string, name: string) {
    if (!confirm(`Delete schedule "${name}"?`)) return;
    setDeleting(`schedule-${ns}-${name}`);
    try {
      await apiDelete(`/v1/velero/schedules/${ns}/${name}`);
      await fetchData();
    } catch {
      alert("Failed to delete schedule");
    }
    setDeleting(null);
  }

  if (!isClient) return null;

  const notDetected = status && !status.detected;

  // Filter items by search
  const filteredBackups = backups.filter(
    (b) =>
      !search ||
      b.name.toLowerCase().includes(search.toLowerCase()) ||
      (b.scheduleName ?? "").toLowerCase().includes(search.toLowerCase()),
  );
  const filteredRestores = restores.filter(
    (r) =>
      !search ||
      r.name.toLowerCase().includes(search.toLowerCase()) ||
      (r.backupName ?? "").toLowerCase().includes(search.toLowerCase()),
  );
  const filteredSchedules = schedules.filter(
    (s) => !search || s.name.toLowerCase().includes(search.toLowerCase()),
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
    backups: loading
      ? "Loading…"
      : `${backups.length} backup${backups.length !== 1 ? "s" : ""}`,
    restores: loading
      ? "Loading…"
      : `${restores.length} restore${restores.length !== 1 ? "s" : ""}`,
    schedules: loading
      ? "Loading…"
      : `${schedules.length} schedule${schedules.length !== 1 ? "s" : ""}`,
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
          {!loading && createLabel && (
            <Button
              type="button"
              variant="primary"
              onClick={() => {
                if (initialTab === "backups") setBackupWizardOpen(true);
                else if (initialTab === "restores") {
                  setRestoreWizardOpen(true);
                } else if (initialTab === "schedules") {
                  setScheduleWizardOpen(true);
                }
              }}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                style={{ marginRight: "4px", verticalAlign: "middle" }}
              >
                <path d="M4 8h8M8 4v8" />
              </svg>
              {createLabel}
            </Button>
          )}
          {!loading && (
            <Button
              type="button"
              variant="ghost"
              onClick={handleRefresh}
              disabled={refreshing}
            >
              {refreshing ? "Refreshing…" : "Refresh"}
            </Button>
          )}
        </div>
      </div>

      {/* ── Loading ──────────────────────────────────────────────── */}
      {loading && (
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
      {error && (
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
          {error}
        </div>
      )}

      {/* ── Velero not detected ──────────────────────────────────── */}
      {notDetected && !loading && (
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
              className="hover:underline"
            >
              View Velero Installation Docs &rarr;
            </a>
          </div>
        </WidgetShell>
      )}

      {/* ── Main content (Velero detected, not loading) ──────────── */}
      {!loading && !error && status?.detected && (
        <>
          {initialTab === "overview" && <AssuranceStrip />}

          {initialTab === "overview" && (
            <VeleroOverview
              backups={backups}
              restores={restores}
              schedules={schedules}
              locations={locations}
              status={status}
            />
          )}

          {initialTab !== "overview" && (
            <>
              {/* Search bar */}
              <div style={{ maxWidth: "320px" }}>
                <SearchBar
                  value={search}
                  onInput={setSearch}
                  placeholder="Search…"
                />
              </div>

              {initialTab === "backups" && (
                <BackupsResourceTable
                  backups={filteredBackups}
                  deleting={deleting}
                  onDelete={handleDeleteBackup}
                />
              )}
              {initialTab === "restores" && (
                <RestoresResourceTable restores={filteredRestores} />
              )}
              {initialTab === "schedules" && (
                <SchedulesResourceTable
                  schedules={filteredSchedules}
                  deleting={deleting}
                  onDelete={handleDeleteSchedule}
                />
              )}
            </>
          )}
        </>
      )}

      {/* ── Wizard modals ─────────────────────────────────────────── */}
      {backupWizardOpen && (
        <VeleroBackupWizard onClose={() => setBackupWizardOpen(false)} />
      )}
      {restoreWizardOpen && (
        <VeleroRestoreWizard onClose={() => setRestoreWizardOpen(false)} />
      )}
      {scheduleWizardOpen && (
        <VeleroScheduleWizard onClose={() => setScheduleWizardOpen(false)} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Assurance strip — read-only cross-link to /backup/assurance
// ---------------------------------------------------------------------------

const ASSURANCE_STATUS_URL = "/v1/velero/assurance/status";

/**
 * One summary strip on the overview. Self-contained: it owns its own fetch so
 * the dashboard's state is untouched. Read-only by design — it links to the
 * assurance page and offers no restore action. A 501 (remote cluster) hides
 * the strip entirely; 403/503 and other failures show the six-state word
 * without counts, never a "no backups" claim.
 */
function AssuranceStrip() {
  const [assurance, setAssurance] = useState<AssuranceStatus | null>(null);
  const [httpStatus, setHttpStatus] = useState<number | undefined>(undefined);
  const [hidden, setHidden] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<AssuranceStatus>(ASSURANCE_STATUS_URL)
      .then((res) => {
        setAssurance(res.data);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 501) setHidden(true);
        setHttpStatus(err instanceof ApiError ? err.status : undefined);
      })
      .finally(() => {
        setReady(true);
      });
  }, []);

  if (hidden) return null;

  const state = surfaceStateFor(assurance, httpStatus);
  const copy = surfaceStateCopy(state);
  const current = ready && countsAreCurrent(state) && assurance;
  const lastRun = assurance?.runtime?.lastRunAt;

  return (
    <div
      data-testid="velero-assurance-strip"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-md border border-[var(--border-primary)] px-4 py-3 text-sm text-[var(--text-secondary)]"
    >
      <span className="font-medium text-[var(--text-primary)]">
        Backup assurance
      </span>
      {!ready && <span>Checking…</span>}
      {ready && current && (
        <span data-testid="velero-assurance-open-count">
          {current.open.total} open{" "}
          {current.open.total === 1 ? "exception" : "exceptions"}
        </span>
      )}
      {ready && !current && (
        <span data-testid="velero-assurance-state" data-state={state}>
          State: {copy.word}
        </span>
      )}
      {lastRun && <span>Last evaluated {age(lastRun)} ago</span>}
      <a
        href="/backup/assurance"
        className="text-[var(--accent)] hover:underline"
      >
        View recovery readiness &rarr;
      </a>
      <span
        data-testid="velero-assurance-honesty"
        className="basis-full text-xs text-[var(--text-muted)]"
      >
        Freshness is not demonstrated recoverability. {HONESTY_TEXT}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview — glass WidgetShell cards
// ---------------------------------------------------------------------------

function VeleroOverview({
  backups,
  restores,
  schedules,
  locations,
  status,
}: {
  backups: Backup[];
  restores: Restore[];
  schedules: Schedule[];
  locations: LocationsResponse | null;
  status: VeleroStatus;
}) {
  // Donut segments for backups by phase category
  const successBackups = backups.filter(
    (b) => getPhaseCategory(b.phase) === "success",
  ).length;
  const failedBackups = backups.filter(
    (b) => getPhaseCategory(b.phase) === "error",
  ).length;
  const warnBackups = backups.filter(
    (b) => getPhaseCategory(b.phase) === "warning",
  ).length;
  const inProgressBackups = backups.filter(
    (b) => getPhaseCategory(b.phase) === "progress",
  ).length;
  const otherBackups =
    backups.length -
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
      ? [
          {
            value: inProgressBackups,
            color: "var(--info)",
            label: "In Progress",
          },
        ]
      : []),
    ...(otherBackups > 0
      ? [{ value: otherBackups, color: "var(--bg-elevated)", label: "Other" }]
      : []),
    // Placeholder when no backups
    ...(backups.length === 0
      ? [
          {
            value: 1,
            color: "var(--bg-elevated)",
            label: "No backups",
            placeholder: true,
          },
        ]
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
          <div style={{ display: "flex", alignItems: "center", gap: "24px" }}>
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
              className="hover:underline"
            >
              View all &rarr;
            </a>
          }
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
            {/* Not links: there is no per-backup detail page. */}
            {backups.slice(0, 5).map((b) => (
              <div
                key={`${b.namespace}/${b.name}`}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "10px",
                  padding: "8px 0",
                  borderBottom:
                    "1px solid color-mix(in srgb, var(--border-primary) 50%, transparent)",
                }}
              >
                <StatusDot status={phaseTone(b.phase)} />
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
              </div>
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
                <StatusDot status={phaseTone(bsl.phase)} />
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
                <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
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
