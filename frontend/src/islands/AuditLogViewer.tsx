/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { Input } from "@/components/ui/Input.tsx";
import type { Column, Row } from "@/components/ui/ResourceTable.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { api } from "@/lib/api.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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

export default function AuditLogViewer() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage, pageRef] = useStateRef(1);
  const [loading, setLoading] = useState(true);

  // Filters
  const [filterUser, setFilterUser] = useState("");
  const [filterAction, setFilterAction] = useState("");
  const [filterKind, setFilterKind] = useState("");

  const pageSize = 50;

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchLogs` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchLogs(pageRef.current);
  }, []);

  async function fetchLogs(forPage: number) {
    setLoading(true);
    try {
      const params = new URLSearchParams();
      params.set("page", String(forPage));
      params.set("pageSize", String(pageSize));
      if (filterUser) params.set("user", filterUser);
      if (filterAction) params.set("action", filterAction);
      if (filterKind) params.set("kind", filterKind);

      const res = await api<AuditEntry[]>(
        `/v1/audit/logs?${params.toString()}`,
        { method: "GET" },
      );
      setEntries(res.data ?? []);
      setTotal(res.metadata?.total ?? 0);
    } catch {
      setEntries([]);
    } finally {
      setLoading(false);
    }
  }

  function applyFilters() {
    setPage(1);
    fetchLogs(1);
  }

  function nextPage() {
    if (pageRef.current * pageSize < total) {
      setPage(pageRef.current + 1);
      fetchLogs(pageRef.current);
    }
  }

  function prevPage() {
    if (pageRef.current > 1) {
      setPage(pageRef.current - 1);
      fetchLogs(pageRef.current);
    }
  }

  const totalPages = Math.ceil(total / pageSize) || 1;

  const columns: Column[] = [
    { key: "time", label: "Time", width: "160px" },
    { key: "user", label: "User", width: "140px" },
    { key: "action", label: "Action", width: "140px" },
    { key: "resource", label: "Resource", width: "2fr" },
    { key: "result", label: "Result", width: "110px" },
    { key: "detail", label: "Detail", width: "2fr" },
  ];

  const buildRows = (): Row[] => {
    if (loading && entries.length === 0) {
      return [
        {
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
        },
      ];
    }
    if (entries.length === 0) {
      return [
        {
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
        },
      ];
    }
    return entries.map((e, i) => ({
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
            tone={
              e.result === "success"
                ? "ok"
                : e.result === "denied"
                  ? "warn"
                  : "crit"
            }
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
          value={filterUser}
          onChange={(e) => {
            setFilterUser(e.currentTarget.value);
          }}
        />
        <Input
          label="Action"
          type="text"
          placeholder="create, login, reveal…"
          value={filterAction}
          onChange={(e) => {
            setFilterAction(e.currentTarget.value);
          }}
        />
        <Input
          label="Resource Kind"
          type="text"
          placeholder="deployment, secret…"
          value={filterKind}
          onChange={(e) => {
            setFilterKind(e.currentTarget.value);
          }}
        />
        <Button
          type="button"
          variant="secondary"
          onClick={applyFilters}
          loading={loading}
        >
          Filter
        </Button>
      </div>

      {/* Results */}
      {loading && entries.length === 0 ? (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "32px 0",
          }}
        >
          <Spinner className="text-accent" />
        </div>
      ) : (
        <ResourceTable columns={columns} rows={buildRows()} chevron={false} />
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
          {total} entries · Page {page} of {totalPages}
        </span>
        <div style={{ display: "flex", gap: "8px" }}>
          <Button
            type="button"
            variant="ghost"
            onClick={prevPage}
            disabled={page <= 1}
          >
            Previous
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={nextPage}
            disabled={page >= totalPages}
          >
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
