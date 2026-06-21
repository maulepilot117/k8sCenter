import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import {
  ExpiryBadge,
  StatusBadge,
} from "@/components/ui/CertificateBadges.tsx";
import type { Certificate } from "@/lib/certmanager-types.ts";

const PAGE_SIZE = 100;

export default function CertificatesList() {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const certs = useSignal<Certificate[]>([]);
  const search = useSignal("");
  const page = useSignal(1);

  // Check URL param for pre-filter
  const expiringOnly = IS_BROWSER &&
    new URLSearchParams(globalThis.location.search).get("status") ===
      "expiring";

  async function fetchData() {
    try {
      const res = await apiGet<Certificate[]>(
        "/v1/certificates/certificates",
      );
      certs.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      error.value = "Failed to load certificates";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  if (!IS_BROWSER) return null;

  const filtered = certs.value.filter((c) => {
    if (
      expiringOnly &&
      c.status !== "Expiring" &&
      c.status !== "Expired"
    ) {
      return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
      return (
        c.name.toLowerCase().includes(q) ||
        c.namespace.toLowerCase().includes(q) ||
        c.issuerRef.name.toLowerCase().includes(q)
      );
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
        <a
          href="/security/certificates/new"
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
            textDecoration: "none",
            border: "none",
            cursor: "pointer",
            whiteSpace: "nowrap",
          }}
        >
          Create Certificate
        </a>
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
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, issuer..."
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {certs.value.length} certificates
        </span>
      </div>

      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error.value}
        </p>
      )}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Issuer
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  DNS Names
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Expires
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((c) => (
                <tr key={c.uid} class="hover:bg-hover/30">
                  <td class="px-3 py-2">
                    <a
                      href={`/security/certificates/${c.namespace}/${c.name}`}
                      class="font-medium hover:underline"
                      style={{ color: "var(--accent)" }}
                    >
                      {c.name}
                    </a>
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {c.namespace}
                  </td>
                  <td class="px-3 py-2">
                    <StatusBadge status={c.status} />
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {c.issuerRef.name}
                  </td>
                  <td class="px-3 py-2 text-text-secondary text-xs truncate max-w-[200px]">
                    {(c.dnsNames ?? []).join(", ") || "—"}
                  </td>
                  <td class="px-3 py-2">
                    <ExpiryBadge daysRemaining={c.daysRemaining} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p class="text-sm text-text-muted">
            {filtered.length} certificates &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div style={{ display: "flex", gap: "8px" }}>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value--;
              }}
              disabled={page.value <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value++;
              }}
              disabled={page.value >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty state */}
      {!loading.value && !error.value && filtered.length === 0 && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p
              style={{
                color: "var(--text-muted)",
                fontSize: "14px",
                margin: 0,
              }}
            >
              {certs.value.length === 0
                ? "No certificates found. Certificates will appear here once cert-manager issues them."
                : "No certificates match your filters."}
            </p>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
