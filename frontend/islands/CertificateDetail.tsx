import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet, apiPost } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import {
  ExpiryBadge,
  StatusBadge,
} from "@/components/ui/CertificateBadges.tsx";
import type {
  Certificate,
  CertificateDetail,
  CertificateRequest,
  Challenge,
  Order,
  ThresholdSource,
} from "@/lib/certmanager-types.ts";

interface Props {
  namespace: string;
  name: string;
}

function formatDate(iso?: string): string {
  if (!iso) return "\u2014";
  return new Date(iso).toLocaleString();
}

// keySourceLabel returns the per-key source attribution shown next to
// each threshold value. e.g. "60d (From Issuer letsencrypt-prod)".
// When source is undefined (response from a backend that didn't run
// ApplyThresholds), falls through to Default.
function keySourceLabel(
  source: ThresholdSource | undefined,
  cert: Certificate,
): string {
  switch (source) {
    case "certificate":
      return "From this certificate";
    case "issuer":
      return `From Issuer ${cert.issuerRef.name}`;
    case "clusterissuer":
      return `From ClusterIssuer ${cert.issuerRef.name}`;
    default:
      return "Default";
  }
}

// thresholdResolutionTooltip explains the resolution chain in plain
// English so an operator unfamiliar with the annotation contract can
// read the tile without diving into docs.
function thresholdResolutionTooltip(): string {
  return "Set kubecenter.io/cert-warn-threshold-days or kubecenter.io/cert-critical-threshold-days on a Certificate, its Issuer, or its ClusterIssuer to override these values. Resolution chain: cert annotation > issuer > clusterissuer > package default. Each key resolves independently — a cert can override warn alone and inherit critical from its issuer.";
}

export default function CertificateDetailIsland({ namespace, name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const detail = useSignal<CertificateDetail | null>(null);
  const actionMsg = useSignal<string | null>(null);
  const confirmReissue = useSignal(false);

  async function fetchDetail() {
    loading.value = true;
    error.value = null;
    try {
      const res = await apiGet<CertificateDetail>(
        `/v1/certificates/certificates/${namespace}/${name}`,
      );
      detail.value = res.data ?? null;
    } catch {
      error.value = "Failed to load certificate details";
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchDetail();
  }, [namespace, name]);

  if (!IS_BROWSER) return null;

  async function handleRenew() {
    actionMsg.value = null;
    try {
      await apiPost(
        `/v1/certificates/certificates/${namespace}/${name}/renew`,
        {},
      );
      actionMsg.value = "Renewal triggered. Refreshing\u2026";
      globalThis.setTimeout(() => {
        fetchDetail();
        actionMsg.value = null;
      }, 1500);
    } catch {
      actionMsg.value = "Renew failed. Check cert-manager logs.";
    }
  }

  async function handleReissue() {
    confirmReissue.value = false;
    actionMsg.value = null;
    try {
      await apiPost(
        `/v1/certificates/certificates/${namespace}/${name}/reissue`,
        {},
      );
      actionMsg.value = "Re-issue triggered. Refreshing\u2026";
      globalThis.setTimeout(() => {
        fetchDetail();
        actionMsg.value = null;
      }, 1500);
    } catch {
      actionMsg.value = "Re-issue failed. Check cert-manager logs.";
    }
  }

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <p class="text-sm text-danger p-6">{error.value}</p>;
  }

  if (!detail.value) return null;

  const cert = detail.value.certificate;
  const requests: CertificateRequest[] = detail.value.certificateRequests ?? [];
  const orders: Order[] = detail.value.orders ?? [];
  const challenges: Challenge[] = detail.value.challenges ?? [];

  return (
    <div class="p-6 space-y-6">
      {/* Header */}
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{cert.name}</h1>
        <StatusBadge status={cert.status} />
        <ExpiryBadge daysRemaining={cert.daysRemaining} />
      </div>

      {/* Actions */}
      <div class="flex flex-wrap items-center gap-3">
        <Button type="button" variant="primary" onClick={handleRenew}>
          Renew
        </Button>
        <Button
          type="button"
          variant="danger"
          onClick={() => {
            confirmReissue.value = true;
          }}
        >
          Re-issue
        </Button>
        {actionMsg.value && (
          <span class="text-sm text-text-muted">{actionMsg.value}</span>
        )}
      </div>

      {/* Re-issue confirmation modal */}
      {confirmReissue.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-bg-elevated border border-border-primary rounded-lg p-6 max-w-md w-full shadow-xl">
            <h2 class="text-lg font-semibold text-text-primary mb-3">
              Confirm Re-issue
            </h2>
            <p class="text-sm text-text-secondary mb-6">
              Re-issue will delete Secret{" "}
              <span class="font-medium text-text-primary">
                {cert.secretName}
              </span>{" "}
              in{" "}
              <span class="font-medium text-text-primary">
                {cert.namespace}
              </span>
              . Applications using this Secret will briefly lose TLS until
              cert-manager completes re-issuance. Continue?
            </p>
            <div class="flex gap-3 justify-end">
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  confirmReissue.value = false;
                }}
              >
                Cancel
              </Button>
              <Button type="button" variant="danger" onClick={handleReissue}>
                Yes, re-issue
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Details */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
        <h2 class="text-sm font-semibold text-text-primary mb-4">Details</h2>
        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
          <div>
            <dt class="text-text-muted">Namespace</dt>
            <dd class="text-text-primary">{cert.namespace}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Issuer</dt>
            <dd class="text-text-primary">
              {cert.issuerRef.kind}/{cert.issuerRef.name}
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Secret</dt>
            <dd>
              <a
                href={`/workloads/secrets/${cert.namespace}/${cert.secretName}`}
                class="text-brand hover:underline"
              >
                {cert.secretName}
              </a>
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Common Name</dt>
            <dd class="text-text-primary">{cert.commonName || "\u2014"}</dd>
          </div>
          <div class="sm:col-span-2">
            <dt class="text-text-muted">DNS Names</dt>
            <dd class="text-text-primary">
              {(cert.dnsNames ?? []).join(", ") || "\u2014"}
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Not Before</dt>
            <dd class="text-text-primary">{formatDate(cert.notBefore)}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Not After</dt>
            <dd class="text-text-primary">{formatDate(cert.notAfter)}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Renewal Time</dt>
            <dd class="text-text-primary">{formatDate(cert.renewalTime)}</dd>
          </div>
          {cert.warningThresholdDays !== undefined &&
            cert.warningThresholdDays > 0 && (
            <div class="sm:col-span-2">
              <dt class="text-text-muted">Expiry Thresholds</dt>
              <dd
                class="text-text-primary"
                title={thresholdResolutionTooltip()}
              >
                Warns at {cert.warningThresholdDays}d{" "}
                <span class="text-xs text-text-muted">
                  ({keySourceLabel(cert.warningThresholdSource, cert)})
                </span>
                , critical at {cert.criticalThresholdDays ?? "—"}d{" "}
                <span class="text-xs text-text-muted">
                  ({keySourceLabel(cert.criticalThresholdSource, cert)})
                </span>
                {cert.thresholdConflict && (
                  <span
                    class="ml-2 rounded px-1.5 py-0.5 text-xs font-medium"
                    style={{
                      backgroundColor:
                        "color-mix(in srgb, var(--status-warning) 15%, transparent)",
                      color: "var(--status-warning)",
                    }}
                    title="Resolved threshold pair would have violated critical < warning. Using package defaults until you fix one of the annotations."
                  >
                    Override conflict — using defaults
                  </span>
                )}
              </dd>
            </div>
          )}
          {cert.reason && (
            <div class="sm:col-span-2">
              <dt class="text-text-muted">Reason</dt>
              <dd class="text-text-primary">{cert.reason}</dd>
            </div>
          )}
          {cert.message && (
            <div class="sm:col-span-2">
              <dt class="text-text-muted">Message</dt>
              <dd class="text-text-secondary">{cert.message}</dd>
            </div>
          )}
        </dl>
      </div>

      {/* CertificateRequests */}
      {requests.length > 0 && (
        <div class="rounded-lg border border-border-primary">
          <h2 class="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Certificate Requests ({requests.length})
          </h2>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary bg-surface">
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Status
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Created
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {requests.map((cr) => (
                  <tr key={cr.uid} class="hover:bg-hover/30">
                    <td class="px-3 py-2 font-medium text-text-primary">
                      {cr.name}
                    </td>
                    <td class="px-3 py-2">
                      <StatusBadge status={cr.status} />
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
                      {cr.reason || "\u2014"}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
                      {formatDate(cr.createdAt)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Orders */}
      {orders.length > 0 && (
        <div class="rounded-lg border border-border-primary">
          <h2 class="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Orders ({orders.length})
          </h2>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary bg-surface">
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    State
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {orders.map((o) => (
                  <tr key={o.uid} class="hover:bg-hover/30">
                    <td class="px-3 py-2 font-medium text-text-primary">
                      {o.name}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">{o.state}</td>
                    <td class="px-3 py-2 text-text-secondary">
                      {o.reason || "\u2014"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Challenges */}
      {challenges.length > 0 && (
        <div class="rounded-lg border border-border-primary">
          <h2 class="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Challenges ({challenges.length})
          </h2>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary bg-surface">
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Type
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    DNS
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    State
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {challenges.map((ch) => (
                  <tr key={ch.uid} class="hover:bg-hover/30">
                    <td class="px-3 py-2 font-medium text-text-primary">
                      {ch.name}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">{ch.type}</td>
                    <td class="px-3 py-2 text-text-secondary">
                      {ch.dnsName || "\u2014"}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">{ch.state}</td>
                    <td class="px-3 py-2 text-text-secondary">
                      {ch.reason || "\u2014"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
