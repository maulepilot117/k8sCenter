/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import {
  ExpiryBadge,
  StatusBadge,
} from "@/components/ui/CertificateBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import type {
  Certificate,
  CertificateDetail,
  CertificateRequest,
  Challenge,
  Order,
  ThresholdSource,
} from "@/lib/certmanager-types.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

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
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [detail, setDetail] = useState<CertificateDetail | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);
  const [confirmReissue, setConfirmReissue] = useState(false);
  const isClient = useIsClient();

  const fetchDetail = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await apiGet<CertificateDetail>(
        `/v1/certificates/certificates/${namespace}/${name}`,
      );
      setDetail(res.data ?? null);
    } catch {
      setError("Failed to load certificate details");
    } finally {
      setLoading(false);
    }
  }, [namespace, name]);

  useEffect(() => {
    fetchDetail();
  }, [fetchDetail]);

  if (!isClient) return null;

  async function handleRenew() {
    setActionMsg(null);
    try {
      await apiPost(
        `/v1/certificates/certificates/${namespace}/${name}/renew`,
        {},
      );
      setActionMsg("Renewal triggered. Refreshing\u2026");
      globalThis.setTimeout(() => {
        fetchDetail();
        setActionMsg(null);
      }, 1500);
    } catch {
      setActionMsg("Renew failed. Check cert-manager logs.");
    }
  }

  async function handleReissue() {
    setConfirmReissue(false);
    setActionMsg(null);
    try {
      await apiPost(
        `/v1/certificates/certificates/${namespace}/${name}/reissue`,
        {},
      );
      setActionMsg("Re-issue triggered. Refreshing\u2026");
      globalThis.setTimeout(() => {
        fetchDetail();
        setActionMsg(null);
      }, 1500);
    } catch {
      setActionMsg("Re-issue failed. Check cert-manager logs.");
    }
  }

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-danger p-6">{error}</p>;
  }

  if (!detail) return null;

  const cert = detail.certificate;
  const requests: CertificateRequest[] = detail.certificateRequests ?? [];
  const orders: Order[] = detail.orders ?? [];
  const challenges: Challenge[] = detail.challenges ?? [];

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold text-text-primary">{cert.name}</h1>
        <StatusBadge status={cert.status} />
        <ExpiryBadge daysRemaining={cert.daysRemaining} />
      </div>

      {/* Actions */}
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="primary" onClick={handleRenew}>
          Renew
        </Button>
        <Button
          type="button"
          variant="danger"
          onClick={() => {
            setConfirmReissue(true);
          }}
        >
          Re-issue
        </Button>
        {actionMsg && (
          <span className="text-sm text-text-muted">{actionMsg}</span>
        )}
      </div>

      {/* Re-issue confirmation modal */}
      {confirmReissue && (
        <div className="fixed inset-0 z-50 flex items-center justify-center glass-scrim">
          <div className="glass-elevated rounded-2xl p-6 max-w-md w-full">
            <h2 className="text-lg font-semibold text-text-primary mb-3">
              Confirm Re-issue
            </h2>
            <p className="text-sm text-text-secondary mb-6">
              Re-issue will delete Secret{" "}
              <span className="font-medium text-text-primary">
                {cert.secretName}
              </span>{" "}
              in{" "}
              <span className="font-medium text-text-primary">
                {cert.namespace}
              </span>
              . Applications using this Secret will briefly lose TLS until
              cert-manager completes re-issuance. Continue?
            </p>
            <div className="flex gap-3 justify-end">
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setConfirmReissue(false);
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
      <div className="rounded-lg border border-border-primary bg-elevated p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-4">
          Details
        </h2>
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
          <div>
            <dt className="text-text-muted">Namespace</dt>
            <dd className="text-text-primary">{cert.namespace}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Issuer</dt>
            <dd className="text-text-primary">
              {cert.issuerRef.kind}/{cert.issuerRef.name}
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Secret</dt>
            <dd>
              <a
                href={`/config/secrets/${cert.namespace}/${cert.secretName}`}
                className="text-brand hover:underline"
              >
                {cert.secretName}
              </a>
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Common Name</dt>
            <dd className="text-text-primary">{cert.commonName || "\u2014"}</dd>
          </div>
          <div className="sm:col-span-2">
            <dt className="text-text-muted">DNS Names</dt>
            <dd className="text-text-primary">
              {(cert.dnsNames ?? []).join(", ") || "\u2014"}
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Not Before</dt>
            <dd className="text-text-primary">{formatDate(cert.notBefore)}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Not After</dt>
            <dd className="text-text-primary">{formatDate(cert.notAfter)}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Renewal Time</dt>
            <dd className="text-text-primary">
              {formatDate(cert.renewalTime)}
            </dd>
          </div>
          {cert.warningThresholdDays !== undefined &&
            cert.warningThresholdDays > 0 && (
              <div className="sm:col-span-2">
                <dt className="text-text-muted">Expiry Thresholds</dt>
                <dd
                  className="text-text-primary"
                  title={thresholdResolutionTooltip()}
                >
                  Warns at {cert.warningThresholdDays}d{" "}
                  <span className="text-xs text-text-muted">
                    ({keySourceLabel(cert.warningThresholdSource, cert)})
                  </span>
                  , critical at {cert.criticalThresholdDays ?? "—"}d{" "}
                  <span className="text-xs text-text-muted">
                    ({keySourceLabel(cert.criticalThresholdSource, cert)})
                  </span>
                  {cert.thresholdConflict && (
                    <span
                      className="ml-2 rounded px-1.5 py-0.5 text-xs font-medium"
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
            <div className="sm:col-span-2">
              <dt className="text-text-muted">Reason</dt>
              <dd className="text-text-primary">{cert.reason}</dd>
            </div>
          )}
          {cert.message && (
            <div className="sm:col-span-2">
              <dt className="text-text-muted">Message</dt>
              <dd className="text-text-secondary">{cert.message}</dd>
            </div>
          )}
        </dl>
      </div>

      {/* CertificateRequests */}
      {requests.length > 0 && (
        <div className="rounded-lg border border-border-primary">
          <h2 className="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Certificate Requests ({requests.length})
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Status
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Created
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {requests.map((cr) => (
                  <tr key={cr.uid} className="hover:bg-hover/30">
                    <td className="px-3 py-2 font-medium text-text-primary">
                      {cr.name}
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge status={cr.status} />
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {cr.reason || "\u2014"}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
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
        <div className="rounded-lg border border-border-primary">
          <h2 className="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Orders ({orders.length})
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    State
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {orders.map((o) => (
                  <tr key={o.uid} className="hover:bg-hover/30">
                    <td className="px-3 py-2 font-medium text-text-primary">
                      {o.name}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">{o.state}</td>
                    <td className="px-3 py-2 text-text-secondary">
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
        <div className="rounded-lg border border-border-primary">
          <h2 className="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Challenges ({challenges.length})
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Type
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    DNS
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    State
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Reason
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {challenges.map((ch) => (
                  <tr key={ch.uid} className="hover:bg-hover/30">
                    <td className="px-3 py-2 font-medium text-text-primary">
                      {ch.name}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">{ch.type}</td>
                    <td className="px-3 py-2 text-text-secondary">
                      {ch.dnsName || "\u2014"}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {ch.state}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
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
