import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";

interface AuditEntry {
  timestamp: string;
  clusterID: string;
  user: string;
  sourceIP: string;
  action: string;
  resourceKind: string;
  resourceNamespace: string;
  resourceName: string;
  result: string;
  detail: string;
}

interface AuditResponse {
  data: AuditEntry[];
  metadata: { total: number; page: number; pageSize: number };
}

export default function AuditLogViewer() {
  const entries = useSignal<AuditEntry[]>([]);
  const total = useSignal(0);
  const page = useSignal(1);
  const loading = useSignal(true);

  // Filters
  const filterUser = useSignal("");
  const filterAction = useSignal("");
  const filterKind = useSignal("");

  const pageSize = 50;

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchLogs();
  }, []);

  async function fetchLogs() {
    loading.value = true;
    try {
      const params = new URLSearchParams();
      params.set("page", String(page.value));
      params.set("pageSize", String(pageSize));
      if (filterUser.value) params.set("user", filterUser.value);
      if (filterAction.value) params.set("action", filterAction.value);
      if (filterKind.value) params.set("kind", filterKind.value);

      const res = await api<AuditEntry[]>(
        `/v1/audit/logs?${params.toString()}`,
        { method: "GET" },
      );
      entries.value = res.data ?? [];
      total.value = res.metadata?.total ?? 0;
    } catch {
      entries.value = [];
    } finally {
      loading.value = false;
    }
  }

  function applyFilters() {
    page.value = 1;
    fetchLogs();
  }

  function nextPage() {
    if (page.value * pageSize < total.value) {
      page.value++;
      fetchLogs();
    }
  }

  function prevPage() {
    if (page.value > 1) {
      page.value--;
      fetchLogs();
    }
  }

  const totalPages = Math.ceil(total.value / pageSize) || 1;

  return (
    <div class="space-y-4">
      {/* Filters */}
      <div class="flex flex-wrap gap-3">
        <Input
          label="User"
          type="text"
          placeholder="admin"
          value={filterUser.value}
          onInput={(e) => {
            filterUser.value = (e.target as HTMLInputElement).value;
          }}
        />
        <Input
          label="Action"
          type="text"
          placeholder="create, login, reveal..."
          value={filterAction.value}
          onInput={(e) => {
            filterAction.value = (e.target as HTMLInputElement).value;
          }}
        />
        <Input
          label="Resource Kind"
          type="text"
          placeholder="deployment, secret..."
          value={filterKind.value}
          onInput={(e) => {
            filterKind.value = (e.target as HTMLInputElement).value;
          }}
        />
        <div class="flex items-end">
          <Button
            type="button"
            variant="secondary"
            onClick={applyFilters}
            loading={loading.value}
          >
            Filter
          </Button>
        </div>
      </div>

      {/* Results */}
      {loading.value
        ? (
          <div class="flex justify-center py-8">
            <div class="h-6 w-6 animate-spin rounded-full border-2 border-border-primary border-t-accent" />
          </div>
        )
        : (
          <div class="overflow-x-auto rounded-lg border border-border-primary">
            <table class="w-full text-sm">
              <thead class="bg-surface">
                <tr>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    Time
                  </th>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    User
                  </th>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    Action
                  </th>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    Resource
                  </th>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    Result
                  </th>
                  <th class="px-3 py-2 text-left font-medium text-text-secondary">
                    Detail
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-primary">
                {entries.value.map((e, i) => (
                  <tr
                    key={i}
                    class="hover:bg-hover/50"
                  >
                    <td class="whitespace-nowrap px-3 py-2 text-xs text-text-muted">
                      {new Date(e.timestamp).toLocaleString()}
                    </td>
                    <td class="px-3 py-2 font-medium text-text-primary">
                      {e.user}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
                      {e.action}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
                      {e.resourceKind
                        ? [
                          e.resourceKind,
                          e.resourceNamespace,
                          e.resourceName,
                        ].filter(Boolean).join("/")
                        : "-"}
                    </td>
                    <td class="px-3 py-2">
                      <StatusBadge
                        status={e.result === "success"
                          ? "running"
                          : e.result === "denied"
                          ? "warning"
                          : "failed"}
                        label={e.result}
                      />
                    </td>
                    <td class="max-w-xs truncate px-3 py-2 text-xs text-text-muted">
                      {e.detail || "-"}
                    </td>
                  </tr>
                ))}
                {entries.value.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      class="px-3 py-8 text-center text-text-muted"
                    >
                      No audit entries found.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}

      {/* Pagination */}
      <div class="flex items-center justify-between">
        <p class="text-sm text-text-muted">
          {total.value} entries &middot; Page {page.value} of {totalPages}
        </p>
        <div class="flex gap-2">
          <Button
            type="button"
            variant="ghost"
            onClick={prevPage}
            disabled={page.value <= 1}
          >
            Previous
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={nextPage}
            disabled={page.value >= totalPages}
          >
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
