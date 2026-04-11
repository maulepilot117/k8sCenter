import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiDelete, apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { age } from "@/lib/format.ts";
import type {
  Backup,
  LocationsResponse,
  Restore,
  Schedule,
  VeleroStatus,
} from "@/lib/velero-types.ts";
import { getPhaseCategory as getPhaseCat } from "@/lib/velero-types.ts";

type Tab = "backups" | "restores" | "schedules";

interface Props {
  initialTab?: Tab;
}

export default function VeleroDashboard({ initialTab = "backups" }: Props) {
  const status = useSignal<VeleroStatus | null>(null);
  const backups = useSignal<Backup[]>([]);
  const restores = useSignal<Restore[]>([]);
  const schedules = useSignal<Schedule[]>([]);
  const locations = useSignal<LocationsResponse | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const tab = useSignal<Tab>(initialTab);
  const refreshing = useSignal(false);
  const deleting = useSignal<string | null>(null);

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

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">Backup & Restore</h1>
        {!loading.value && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>
      <p class="text-sm text-text-muted mb-6">
        Velero backup and restore management.
      </p>

      {loading.value && (
        <div class="flex items-center justify-center py-12">
          <Spinner />
        </div>
      )}

      {error.value && (
        <div class="rounded-lg border border-error/30 bg-error/10 p-4 text-error">
          {error.value}
        </div>
      )}

      {notDetected && !loading.value && (
        <div class="rounded-lg border border-warning/30 bg-warning/10 p-6 text-center">
          <h3 class="font-semibold text-lg text-text-primary mb-2">
            Velero Not Detected
          </h3>
          <p class="text-text-muted mb-4">
            Velero CRDs were not found in this cluster. Install Velero to enable
            backup and restore functionality.
          </p>
          <a
            href="https://velero.io/docs/v1.12/basic-install/"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-accent hover:underline"
          >
            View Velero Installation Docs
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-4 w-4"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                fill-rule="evenodd"
                d="M10.293 3.293a1 1 0 011.414 0l6 6a1 1 0 010 1.414l-6 6a1 1 0 01-1.414-1.414L14.586 11H3a1 1 0 110-2h11.586l-4.293-4.293a1 1 0 010-1.414z"
                clip-rule="evenodd"
              />
            </svg>
          </a>
        </div>
      )}

      {!loading.value && !error.value && status.value?.detected && (
        <>
          {/* Status summary */}
          <div class="mb-6 flex flex-wrap gap-3">
            <SummaryCard
              label="Backups"
              value={backups.value.length}
              color="text-accent"
            />
            <SummaryCard
              label="Restores"
              value={restores.value.length}
              color="text-success"
            />
            <SummaryCard
              label="Schedules"
              value={schedules.value.length}
              color="text-warning"
            />
            <SummaryCard
              label="Storage Locations"
              value={locations.value?.backupStorageLocations.length ?? 0}
              color="text-text-muted"
            />
          </div>

          {/* Tabs */}
          <div class="flex items-center gap-2 mb-4 border-b border-border">
            <TabButton
              active={tab.value === "backups"}
              onClick={() => (tab.value = "backups")}
              count={backups.value.length}
            >
              Backups
            </TabButton>
            <TabButton
              active={tab.value === "restores"}
              onClick={() => (tab.value = "restores")}
              count={restores.value.length}
            >
              Restores
            </TabButton>
            <TabButton
              active={tab.value === "schedules"}
              onClick={() => (tab.value = "schedules")}
              count={schedules.value.length}
            >
              Schedules
            </TabButton>
          </div>

          {/* Search and actions */}
          <div class="flex items-center justify-between gap-4 mb-4">
            <div class="w-64">
              <SearchBar
                value={search.value}
                onInput={(v) => (search.value = v)}
                placeholder="Search..."
              />
            </div>
            <div class="flex gap-2">
              {tab.value === "backups" && (
                <a href="/backup/backups/new">
                  <Button type="button" variant="primary">
                    + New Backup
                  </Button>
                </a>
              )}
              {tab.value === "restores" && (
                <a href="/backup/restores/new">
                  <Button type="button" variant="primary">
                    + New Restore
                  </Button>
                </a>
              )}
              {tab.value === "schedules" && (
                <a href="/backup/schedules/new">
                  <Button type="button" variant="primary">
                    + New Schedule
                  </Button>
                </a>
              )}
            </div>
          </div>

          {/* Tables */}
          {tab.value === "backups" && (
            <BackupsTable
              backups={filteredBackups}
              deleting={deleting.value}
              onDelete={handleDeleteBackup}
            />
          )}
          {tab.value === "restores" && (
            <RestoresTable restores={filteredRestores} />
          )}
          {tab.value === "schedules" && (
            <SchedulesTable
              schedules={filteredSchedules}
              deleting={deleting.value}
              onDelete={handleDeleteSchedule}
            />
          )}
        </>
      )}
    </div>
  );
}

function SummaryCard(
  { label, value, color }: { label: string; value: number; color: string },
) {
  return (
    <div class="rounded-lg border border-border bg-bg-secondary px-4 py-2">
      <div class={`text-2xl font-bold ${color}`}>{value}</div>
      <div class="text-xs text-text-muted">{label}</div>
    </div>
  );
}

function TabButton(
  { active, onClick, count, children }: {
    active: boolean;
    onClick: () => void;
    count: number;
    children: preact.ComponentChildren;
  },
) {
  return (
    <button
      type="button"
      onClick={onClick}
      class={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
        active
          ? "border-accent text-accent"
          : "border-transparent text-text-muted hover:text-text-primary"
      }`}
    >
      {children}
      <span
        class={`ml-1 px-1.5 py-0.5 text-xs rounded-full ${
          active ? "bg-accent/20" : "bg-bg-tertiary"
        }`}
      >
        {count}
      </span>
    </button>
  );
}

function PhaseBadge({ phase }: { phase: string }) {
  const cat = getPhaseCat(phase);
  const colors: Record<string, string> = {
    success: "bg-success/20 text-success",
    warning: "bg-warning/20 text-warning",
    error: "bg-error/20 text-error",
    progress: "bg-accent/20 text-accent",
    unknown: "bg-text-muted/20 text-text-muted",
  };
  return (
    <span
      class={`inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full ${
        colors[cat]
      }`}
    >
      {phase}
    </span>
  );
}

function BackupsTable(
  { backups, deleting, onDelete }: {
    backups: Backup[];
    deleting: string | null;
    onDelete: (ns: string, name: string) => void;
  },
) {
  if (backups.length === 0) {
    return (
      <div class="text-center py-8 text-text-muted">No backups found.</div>
    );
  }

  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border text-left text-text-muted">
            <th class="px-3 py-2 font-medium">Name</th>
            <th class="px-3 py-2 font-medium">Status</th>
            <th class="px-3 py-2 font-medium">Schedule</th>
            <th class="px-3 py-2 font-medium">Started</th>
            <th class="px-3 py-2 font-medium">Items</th>
            <th class="px-3 py-2 font-medium">Issues</th>
            <th class="px-3 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          {backups.map((b) => (
            <tr
              key={`${b.namespace}/${b.name}`}
              class="border-b border-border hover:bg-bg-secondary"
            >
              <td class="px-3 py-2">
                <a
                  href={`/backup/backups/${b.namespace}/${b.name}`}
                  class="text-accent hover:underline font-medium"
                >
                  {b.name}
                </a>
                <div class="text-xs text-text-muted">{b.namespace}</div>
              </td>
              <td class="px-3 py-2">
                <PhaseBadge phase={b.phase} />
              </td>
              <td class="px-3 py-2 text-text-muted">
                {b.scheduleName || "-"}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {b.startTime ? age(b.startTime) : "-"}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {b.itemsBackedUp} / {b.totalItems}
              </td>
              <td class="px-3 py-2">
                {(b.warnings > 0 || b.errors > 0)
                  ? (
                    <span class="text-warning">
                      {b.warnings}W / {b.errors}E
                    </span>
                  )
                  : <span class="text-success">0</span>}
              </td>
              <td class="px-3 py-2">
                <div class="flex gap-1">
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
                    {deleting === `backup-${b.namespace}-${b.name}`
                      ? "..."
                      : "Delete"}
                  </Button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RestoresTable({ restores }: { restores: Restore[] }) {
  if (restores.length === 0) {
    return (
      <div class="text-center py-8 text-text-muted">No restores found.</div>
    );
  }

  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border text-left text-text-muted">
            <th class="px-3 py-2 font-medium">Name</th>
            <th class="px-3 py-2 font-medium">Status</th>
            <th class="px-3 py-2 font-medium">Backup</th>
            <th class="px-3 py-2 font-medium">Started</th>
            <th class="px-3 py-2 font-medium">Items</th>
            <th class="px-3 py-2 font-medium">Issues</th>
          </tr>
        </thead>
        <tbody>
          {restores.map((r) => (
            <tr
              key={`${r.namespace}/${r.name}`}
              class="border-b border-border hover:bg-bg-secondary"
            >
              <td class="px-3 py-2">
                <a
                  href={`/backup/restores/${r.namespace}/${r.name}`}
                  class="text-accent hover:underline font-medium"
                >
                  {r.name}
                </a>
                <div class="text-xs text-text-muted">{r.namespace}</div>
              </td>
              <td class="px-3 py-2">
                <PhaseBadge phase={r.phase} />
              </td>
              <td class="px-3 py-2 text-text-muted">
                {r.backupName || r.scheduleName || "-"}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {r.startTime ? age(r.startTime) : "-"}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {r.itemsRestored} / {r.totalItems}
              </td>
              <td class="px-3 py-2">
                {(r.warnings > 0 || r.errors > 0)
                  ? (
                    <span class="text-warning">
                      {r.warnings}W / {r.errors}E
                    </span>
                  )
                  : <span class="text-success">0</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SchedulesTable(
  { schedules, deleting, onDelete }: {
    schedules: Schedule[];
    deleting: string | null;
    onDelete: (ns: string, name: string) => void;
  },
) {
  if (schedules.length === 0) {
    return (
      <div class="text-center py-8 text-text-muted">No schedules found.</div>
    );
  }

  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border text-left text-text-muted">
            <th class="px-3 py-2 font-medium">Name</th>
            <th class="px-3 py-2 font-medium">Status</th>
            <th class="px-3 py-2 font-medium">Schedule</th>
            <th class="px-3 py-2 font-medium">Last Backup</th>
            <th class="px-3 py-2 font-medium">Next Run</th>
            <th class="px-3 py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          {schedules.map((s) => (
            <tr
              key={`${s.namespace}/${s.name}`}
              class="border-b border-border hover:bg-bg-secondary"
            >
              <td class="px-3 py-2">
                <a
                  href={`/backup/schedules/${s.namespace}/${s.name}`}
                  class="text-accent hover:underline font-medium"
                >
                  {s.name}
                </a>
                <div class="text-xs text-text-muted">{s.namespace}</div>
              </td>
              <td class="px-3 py-2">
                <PhaseBadge phase={s.paused ? "Paused" : s.phase} />
              </td>
              <td class="px-3 py-2 font-mono text-xs text-text-muted">
                {s.schedule}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {s.lastBackup ? age(s.lastBackup) : "Never"}
              </td>
              <td class="px-3 py-2 text-text-muted">
                {s.nextRunTime ? age(s.nextRunTime) : "-"}
              </td>
              <td class="px-3 py-2">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => onDelete(s.namespace, s.name)}
                  disabled={deleting === `schedule-${s.namespace}-${s.name}`}
                >
                  {deleting === `schedule-${s.namespace}-${s.name}`
                    ? "..."
                    : "Delete"}
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
