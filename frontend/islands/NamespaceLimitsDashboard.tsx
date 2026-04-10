import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { limitsApi } from "@/lib/api.ts";
import type {
  LimitsStatus,
  NamespaceLimits,
  NamespaceSummary,
  ThresholdStatus,
} from "@/lib/limits-types.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Button } from "@/components/ui/Button.tsx";

const PAGE_SIZE = 50;
const PANEL_WIDTH = 400;

// Status badge colors
const STATUS_COLORS: Record<ThresholdStatus, { bg: string; text: string }> = {
  ok: { bg: "bg-success/10", text: "text-success" },
  warning: { bg: "bg-warning/10", text: "text-warning" },
  critical: { bg: "bg-error/10", text: "text-error" },
};

function StatusBadge({ status }: { status: ThresholdStatus }) {
  const colors = STATUS_COLORS[status];
  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colors.bg} ${colors.text} capitalize`}
    >
      {status}
    </span>
  );
}

function UtilizationBar({
  percentage,
  status,
  label,
}: {
  percentage: number;
  status: ThresholdStatus;
  label?: string;
}) {
  const barColor = status === "critical"
    ? "bg-error"
    : status === "warning"
    ? "bg-warning"
    : "bg-success";

  return (
    <div class="flex flex-col gap-1">
      {label && (
        <div class="flex justify-between text-xs text-text-muted">
          <span>{label}</span>
          <span>{percentage.toFixed(1)}%</span>
        </div>
      )}
      <div class="h-2 w-full rounded-full bg-bg-muted overflow-hidden">
        <div
          class={`h-full rounded-full ${barColor} transition-all`}
          style={{ width: `${Math.min(percentage, 100)}%` }}
        />
      </div>
    </div>
  );
}

function SummaryCard({
  label,
  value,
  variant = "default",
}: {
  label: string;
  value: number;
  variant?: "default" | "warning" | "critical" | "muted";
}) {
  const colors = {
    default: "text-text-primary",
    warning: "text-warning",
    critical: "text-error",
    muted: "text-text-muted",
  };
  return (
    <div class="rounded-lg border border-border-primary bg-bg-surface p-4">
      <div class={`text-2xl font-bold ${colors[variant]}`}>{value}</div>
      <div class="text-sm text-text-muted">{label}</div>
    </div>
  );
}

export default function NamespaceLimitsDashboard() {
  const status = useSignal<LimitsStatus | null>(null);
  const summaries = useSignal<NamespaceSummary[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const filterStatus = useSignal<string>("all");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  // Slide-out panel state
  const selectedNamespace = useSignal<string | null>(null);
  const detailData = useSignal<NamespaceLimits | null>(null);
  const detailLoading = useSignal(false);
  const detailError = useSignal<string | null>(null);

  async function fetchData() {
    try {
      const [statusRes, summariesRes] = await Promise.all([
        limitsApi.status(),
        limitsApi.list(),
      ]);
      status.value = statusRes.data;
      summaries.value = Array.isArray(summariesRes.data)
        ? summariesRes.data
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load namespace limits data";
    }
  }

  async function fetchDetail(namespace: string) {
    detailLoading.value = true;
    detailError.value = null;
    try {
      const res = await limitsApi.get(namespace);
      detailData.value = res.data;
    } catch {
      detailError.value = "Failed to load namespace details";
    } finally {
      detailLoading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  // Fetch detail when namespace is selected
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (selectedNamespace.value) {
      fetchDetail(selectedNamespace.value);
    } else {
      detailData.value = null;
    }
  }, [selectedNamespace.value]);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  // Compute counts
  const withQuota = summaries.value.filter((s) => s.hasQuota).length;
  const warningCount = summaries.value.filter(
    (s) => s.status === "warning",
  ).length;
  const criticalCount = summaries.value.filter(
    (s) => s.status === "critical",
  ).length;
  const noQuotaCount = summaries.value.filter((s) => !s.hasQuota).length;

  // Filter and search
  const filtered = summaries.value.filter((s) => {
    if (filterStatus.value === "warning" && s.status !== "warning") {
      return false;
    }
    if (filterStatus.value === "critical" && s.status !== "critical") {
      return false;
    }
    if (filterStatus.value === "no-quota" && s.hasQuota) return false;
    if (filterStatus.value === "ok" && s.status !== "ok") return false;
    if (search.value) {
      return s.namespace.toLowerCase().includes(search.value.toLowerCase());
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
  );

  return (
    <div class="flex h-full">
      {/* Main content */}
      <div
        class="flex-1 overflow-y-auto p-6"
        style={{
          marginRight: selectedNamespace.value ? `${PANEL_WIDTH}px` : 0,
        }}
      >
        <div class="flex items-center justify-between mb-1">
          <h1 class="text-2xl font-bold text-text-primary">Namespace Limits</h1>
          {!loading.value && (
            <div class="flex items-center gap-2">
              <a href="/config/namespace-limits/new">
                <Button type="button" variant="primary">
                  Create Limits
                </Button>
              </a>
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing.value}
              >
                {refreshing.value ? "Refreshing..." : "Refresh"}
              </Button>
            </div>
          )}
        </div>
        <p class="text-sm text-text-muted mb-6">
          ResourceQuota and LimitRange management for namespaces.
        </p>

        {loading.value && (
          <div class="flex items-center justify-center py-12">
            <Spinner />
          </div>
        )}

        {error.value && <ErrorBanner message={error.value} />}

        {!loading.value && !error.value && (
          <>
            {/* Summary cards */}
            <div class="grid grid-cols-4 gap-4 mb-6">
              <SummaryCard label="With Quota" value={withQuota} />
              <SummaryCard
                label="Warning"
                value={warningCount}
                variant="warning"
              />
              <SummaryCard
                label="Critical"
                value={criticalCount}
                variant="critical"
              />
              <SummaryCard
                label="No Quota"
                value={noQuotaCount}
                variant="muted"
              />
            </div>

            {/* Filters */}
            <div class="flex items-center gap-4 mb-4">
              <div class="flex-1 max-w-xs">
                <SearchBar
                  value={search.value}
                  onInput={(v) => {
                    search.value = v;
                    page.value = 1;
                  }}
                  placeholder="Search namespaces..."
                />
              </div>
              <select
                class="rounded-lg border border-border-primary bg-bg-surface px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                value={filterStatus.value}
                onChange={(e) => {
                  filterStatus.value = (e.target as HTMLSelectElement).value;
                  page.value = 1;
                }}
              >
                <option value="all">All Status</option>
                <option value="ok">OK</option>
                <option value="warning">Warning</option>
                <option value="critical">Critical</option>
                <option value="no-quota">No Quota</option>
              </select>
            </div>

            {/* Table */}
            <div class="rounded-lg border border-border-primary overflow-hidden">
              <table class="w-full">
                <thead class="bg-bg-elevated">
                  <tr>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      Namespace
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      CPU
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      Memory
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      Highest %
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      Status
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      Quotas
                    </th>
                    <th class="px-4 py-3 text-left text-sm font-medium text-text-muted">
                      LimitRanges
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-primary">
                  {displayed.length === 0 && (
                    <tr>
                      <td
                        colSpan={7}
                        class="px-4 py-8 text-center text-text-muted"
                      >
                        No namespaces found
                      </td>
                    </tr>
                  )}
                  {displayed.map((s) => (
                    <tr
                      key={s.namespace}
                      class={`hover:bg-bg-elevated cursor-pointer ${
                        selectedNamespace.value === s.namespace
                          ? "bg-accent/5"
                          : ""
                      }`}
                      onClick={() => {
                        selectedNamespace.value =
                          selectedNamespace.value === s.namespace
                            ? null
                            : s.namespace;
                      }}
                    >
                      <td class="px-4 py-3 font-medium text-text-primary">
                        {s.namespace}
                      </td>
                      <td class="px-4 py-3">
                        {s.cpuUsedPercent !== undefined
                          ? (
                            <div class="w-24">
                              <UtilizationBar
                                percentage={s.cpuUsedPercent}
                                status={s.status}
                              />
                            </div>
                          )
                          : <span class="text-text-muted">-</span>}
                      </td>
                      <td class="px-4 py-3">
                        {s.memoryUsedPercent !== undefined
                          ? (
                            <div class="w-24">
                              <UtilizationBar
                                percentage={s.memoryUsedPercent}
                                status={s.status}
                              />
                            </div>
                          )
                          : <span class="text-text-muted">-</span>}
                      </td>
                      <td class="px-4 py-3 text-sm">
                        {s.hasQuota
                          ? `${s.highestUtilization.toFixed(1)}%`
                          : "-"}
                      </td>
                      <td class="px-4 py-3">
                        <StatusBadge status={s.status} />
                      </td>
                      <td class="px-4 py-3 text-sm text-text-muted">
                        {s.quotaCount}
                      </td>
                      <td class="px-4 py-3 text-sm text-text-muted">
                        {s.limitRangeCount}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Pagination */}
            {totalPages > 1 && (
              <div class="flex items-center justify-between mt-4">
                <span class="text-sm text-text-muted">
                  Page {page.value} of {totalPages} ({filtered.length} total)
                </span>
                <div class="flex gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={page.value === 1}
                    onClick={() => page.value--}
                  >
                    Previous
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={page.value === totalPages}
                    onClick={() => page.value++}
                  >
                    Next
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {/* Slide-out detail panel */}
      {selectedNamespace.value && (
        <div
          class="fixed right-0 top-0 h-full overflow-y-auto border-l border-border-primary bg-bg-surface shadow-xl"
          style={{ width: `${PANEL_WIDTH}px` }}
        >
          <div class="sticky top-0 flex items-center justify-between border-b border-border-primary bg-bg-surface px-4 py-3">
            <div>
              <h2 class="text-lg font-semibold text-text-primary">
                {selectedNamespace.value}
              </h2>
              <p class="text-sm text-text-muted">Namespace Details</p>
            </div>
            <button
              type="button"
              class="rounded p-1 text-text-muted hover:text-text-primary hover:bg-bg-elevated"
              onClick={() => {
                selectedNamespace.value = null;
              }}
              aria-label="Close panel"
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 20 20"
                fill="currentColor"
              >
                <path d="M6.28 5.22a.75.75 0 00-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 101.06 1.06L10 11.06l3.72 3.72a.75.75 0 101.06-1.06L11.06 10l3.72-3.72a.75.75 0 00-1.06-1.06L10 8.94 6.28 5.22z" />
              </svg>
            </button>
          </div>

          <div class="p-4">
            {detailLoading.value && (
              <div class="flex items-center justify-center py-8">
                <Spinner />
              </div>
            )}

            {detailError.value && <ErrorBanner message={detailError.value} />}

            {!detailLoading.value && detailData.value && (
              <>
                {/* Quotas section */}
                <div class="mb-6">
                  <h3 class="text-sm font-semibold text-text-primary mb-3">
                    ResourceQuotas ({detailData.value.quotas.length})
                  </h3>
                  {detailData.value.quotas.length === 0
                    ? <p class="text-sm text-text-muted">No quotas defined</p>
                    : (
                      <div class="space-y-4">
                        {detailData.value.quotas.map((quota) => (
                          <div
                            key={quota.name}
                            class="rounded-lg border border-border-primary p-3"
                          >
                            <div class="flex items-center justify-between mb-2">
                              <span class="font-medium text-text-primary">
                                {quota.name}
                              </span>
                              <span class="text-xs text-text-muted">
                                Thresholds: {quota.warnThreshold}% /{" "}
                                {quota.criticalThreshold}%
                              </span>
                            </div>
                            <div class="space-y-2">
                              {Object.entries(quota.utilization).map(
                                ([resource, util]) => (
                                  <UtilizationBar
                                    key={resource}
                                    percentage={util.percentage}
                                    status={util.status}
                                    label={`${resource}: ${util.used} / ${util.hard}`}
                                  />
                                ),
                              )}
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                </div>

                {/* LimitRanges section */}
                <div>
                  <h3 class="text-sm font-semibold text-text-primary mb-3">
                    LimitRanges ({detailData.value.limitRanges.length})
                  </h3>
                  {detailData.value.limitRanges.length === 0
                    ? (
                      <p class="text-sm text-text-muted">
                        No limit ranges defined
                      </p>
                    )
                    : (
                      <div class="space-y-4">
                        {detailData.value.limitRanges.map((lr) => (
                          <div
                            key={lr.name}
                            class="rounded-lg border border-border-primary p-3"
                          >
                            <div class="font-medium text-text-primary mb-2">
                              {lr.name}
                            </div>
                            {lr.limits.map((limit, i) => (
                              <div key={i} class="mb-2">
                                <div class="text-xs font-medium text-text-muted mb-1">
                                  {limit.type}
                                </div>
                                <div class="grid grid-cols-2 gap-2 text-xs">
                                  {limit.default && (
                                    <div>
                                      <span class="text-text-muted">
                                        Default:
                                      </span>
                                      <div class="text-text-primary">
                                        {Object.entries(limit.default)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.defaultRequest && (
                                    <div>
                                      <span class="text-text-muted">
                                        Request:
                                      </span>
                                      <div class="text-text-primary">
                                        {Object.entries(limit.defaultRequest)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.max && (
                                    <div>
                                      <span class="text-text-muted">Max:</span>
                                      <div class="text-text-primary">
                                        {Object.entries(limit.max)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.min && (
                                    <div>
                                      <span class="text-text-muted">Min:</span>
                                      <div class="text-text-primary">
                                        {Object.entries(limit.min)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                </div>
                              </div>
                            ))}
                          </div>
                        ))}
                      </div>
                    )}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
