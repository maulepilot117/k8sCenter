import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
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
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-1">
        Certificates
      </h1>
      <p class="text-sm text-text-muted mb-6">
        cert-manager certificates across all namespaces.
        {expiringOnly && " Showing expiring and expired certificates only."}
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, issuer..."
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {certs.value.length} certificates
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

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
                      class="font-medium text-brand hover:underline"
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
                    {(c.dnsNames ?? []).join(", ") || "\u2014"}
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
        <div class="mt-4 flex items-center justify-between">
          <p class="text-sm text-text-muted">
            {filtered.length} certificates &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div class="flex gap-2">
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

      {!loading.value && !error.value && filtered.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {certs.value.length === 0
              ? "No certificates found. Certificates will appear here once cert-manager issues them."
              : "No certificates match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}
