/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import {
  ExpiryBadge,
  StatusBadge,
} from "@/components/ui/CertificateBadges.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet } from "@/lib/api.ts";
import type { Certificate } from "@/lib/certmanager-types.ts";
import CertificateWizard from "@/src/islands/CertificateWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const PAGE_SIZE = 100;

export default function CertificatesList() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [certs, setCerts] = useState<Certificate[]>([]);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [wizardOpen, setWizardOpen] = useState(false);
  const isClient = useIsClient();

  // Check URL param for pre-filter
  const expiringOnly =
    isClient &&
    new URLSearchParams(globalThis.location.search).get("status") ===
      "expiring";

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") setWizardOpen(true);
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    (async () => {
      try {
        const res = await apiGet<Certificate[]>(
          "/v1/certificates/certificates",
        );
        setCerts(Array.isArray(res.data) ? res.data : []);
        setError(null);
      } catch {
        setError("Failed to load certificates");
      }
      setLoading(false);
    })();
  }, []);

  if (!isClient) return null;

  const filtered = certs.filter((c) => {
    if (expiringOnly && c.status !== "Expiring" && c.status !== "Expired") {
      return false;
    }
    if (search) {
      const q = search.toLowerCase();
      return (
        c.name.toLowerCase().includes(q) ||
        c.namespace.toLowerCase().includes(q) ||
        c.issuerRef.name.toLowerCase().includes(q)
      );
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  const currentPage = Math.min(page, totalPages);
  if (page > totalPages) setPage(totalPages);
  const displayed = filtered.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE,
  );

  return (
    <div
      style={{
        padding: "24px",
        display: "flex",
        flexDirection: "column",
        gap: "20px",
      }}
    >
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
        }}
      >
        <div>
          <h1 style={{ fontSize: "24px", fontWeight: 700, margin: 0 }}>
            Certificates
          </h1>
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              margin: "4px 0 0",
            }}
          >
            cert-manager certificates across all namespaces.
            {expiringOnly && " Showing expiring and expired certificates only."}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setWizardOpen(true)}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "6px",
            padding: "8px 16px",
            fontSize: "13px",
            fontWeight: 600,
            color: "var(--bg-base)",
            background: "var(--accent)",
            borderRadius: "9px",
            border: "none",
            cursor: "pointer",
            whiteSpace: "nowrap",
            fontFamily: "inherit",
          }}
        >
          Create Certificate
        </button>

        {wizardOpen && (
          <CertificateWizard onClose={() => setWizardOpen(false)} />
        )}
      </div>

      {/* Filters */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "16px",
        }}
      >
        <div style={{ flex: 1, maxWidth: "320px" }}>
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
              setPage(1);
            }}
            placeholder="Filter by name, namespace, issuer..."
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {certs.length} certificates
        </span>
      </div>

      {loading && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner className="text-brand" />
        </div>
      )}

      {error && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error}
        </p>
      )}

      {!loading && !error && filtered.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-border-primary">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border-primary bg-surface">
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Issuer
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  DNS Names
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Expires
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {displayed.map((c) => (
                <tr key={c.uid} className="hover:bg-hover/30">
                  <td className="px-3 py-2">
                    <a
                      href={`/security/certificates/${c.namespace}/${c.name}`}
                      className="font-medium hover:underline"
                      style={{ color: "var(--accent)" }}
                    >
                      {c.name}
                    </a>
                  </td>
                  <td className="px-3 py-2 text-text-secondary">
                    {c.namespace}
                  </td>
                  <td className="px-3 py-2">
                    <StatusBadge status={c.status} />
                  </td>
                  <td className="px-3 py-2 text-text-secondary">
                    {c.issuerRef.name}
                  </td>
                  <td className="px-3 py-2 text-text-secondary text-xs truncate max-w-[200px]">
                    {(c.dnsNames ?? []).join(", ") || "—"}
                  </td>
                  <td className="px-3 py-2">
                    <ExpiryBadge daysRemaining={c.daysRemaining} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading && !error && filtered.length > PAGE_SIZE && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p className="text-sm text-text-muted">
            {filtered.length} certificates &middot; Page {currentPage} of{" "}
            {totalPages}
          </p>
          <div style={{ display: "flex", gap: "8px" }}>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage - 1);
              }}
              disabled={currentPage <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage + 1);
              }}
              disabled={currentPage >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty state */}
      {!loading && !error && filtered.length === 0 && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p
              style={{
                color: "var(--text-muted)",
                fontSize: "14px",
                margin: 0,
              }}
            >
              {certs.length === 0
                ? "No certificates found. Certificates will appear here once cert-manager issues them."
                : "No certificates match your filters."}
            </p>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
