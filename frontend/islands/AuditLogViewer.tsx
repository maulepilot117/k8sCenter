import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import type { Column, Row } from "@/components/ui/ResourceTable.tsx";

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

  const columns: Column[] = [
    { key: "time", label: "Time", width: "160px" },
    { key: "user", label: "User", width: "140px" },
    { key: "action", label: "Action", width: "140px" },
    { key: "resource", label: "Resource", width: "2fr" },
    { key: "result", label: "Result", width: "110px" },
    { key: "detail", label: "Detail", width: "2fr" },
  ];

  const buildRows = (): Row[] => {
    if (loading.value && entries.value.length === 0) {
      return [{
        id: "__loading__",
        cells: {
          time: (
            <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
              Loading…
            </span>
          ),
          user: null,
          action: null,
          resource: null,
          result: null,
          detail: null,
        },
      }];
    }
    if (entries.value.length === 0) {
      return [{
        id: "__empty__",
        cells: {
          time: (
            <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
              No audit entries found.
            </span>
          ),
          user: null,
          action: null,
          resource: null,
          result: null,
          detail: null,
        },
      }];
    }
    return entries.value.map((e, i) => ({
      id: `${e.timestamp}-${i}`,
      cells: {
        time: (
          <span
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              fontVariantNumeric: "tabular-nums",
              whiteSpace: "nowrap",
            }}
          >
            {new Date(e.timestamp).toLocaleString()}
          </span>
        ),
        user: (
          <span
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-primary)",
            }}
          >
            {e.user}
          </span>
        ),
        action: (
          <span style={{ fontSize: "13px", color: "var(--text-secondary)" }}>
            {e.action}
          </span>
        ),
        resource: (
          <span
            style={{
              fontSize: "13px",
              color: "var(--text-secondary)",
              fontFamily: "var(--font-mono)",
            }}
          >
            {e.resourceKind
              ? [e.resourceKind, e.resourceNamespace, e.resourceName]
                .filter(Boolean)
                .join("/")
              : "—"}
          </span>
        ),
        result: (
          <StatusBadge
            label={e.result}
            tone={e.result === "success"
              ? "ok"
              : e.result === "denied"
              ? "warn"
              : "crit"}
          />
        ),
        detail: (
          <span
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              display: "block",
              maxWidth: "100%",
            }}
            title={e.detail || undefined}
          >
            {e.detail || "—"}
          </span>
        ),
      },
    }));
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Filters */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "12px",
          alignItems: "flex-end",
        }}
      >
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
          placeholder="create, login, reveal…"
          value={filterAction.value}
          onInput={(e) => {
            filterAction.value = (e.target as HTMLInputElement).value;
          }}
        />
        <Input
          label="Resource Kind"
          type="text"
          placeholder="deployment, secret…"
          value={filterKind.value}
          onInput={(e) => {
            filterKind.value = (e.target as HTMLInputElement).value;
          }}
        />
        <Button
          type="button"
          variant="secondary"
          onClick={applyFilters}
          loading={loading.value}
        >
          Filter
        </Button>
      </div>

      {/* Results */}
      {loading.value && entries.value.length === 0
        ? (
          <div
            style={{
              display: "flex",
              justifyContent: "center",
              padding: "32px 0",
            }}
          >
            <Spinner class="text-accent" />
          </div>
        )
        : (
          <ResourceTable
            columns={columns}
            rows={buildRows()}
            chevron={false}
          />
        )}

      {/* Pagination */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
          {total.value} entries · Page {page.value} of {totalPages}
        </span>
        <div style={{ display: "flex", gap: "8px" }}>
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
