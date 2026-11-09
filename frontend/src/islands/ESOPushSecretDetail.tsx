/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import { type EvidenceTabKey, evidenceTabsFor } from "@/lib/eso-evidence.ts";
import type { PushSecret } from "@/lib/eso-types.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import ESOEvidencePanel from "@/src/islands/ESOEvidencePanel.tsx";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  namespace: string;
  name: string;
}

type TabKey = "overview" | EvidenceTabKey | "chain";

// No History tab: no collector records PushSecret attempts (D5, R12).
const TABS: Array<{ key: TabKey; label: string }> = [
  { key: "overview", label: "Overview" },
  ...evidenceTabsFor("pushsecrets"),
  { key: "chain", label: "Chain" },
];

function isEvidenceTab(tab: TabKey): tab is EvidenceTabKey {
  return tab !== "overview" && tab !== "chain";
}

const EM_DASH = "—";

function storeHref(kind: string, namespace: string, name: string): string {
  if (kind === "ClusterSecretStore") {
    return `/external-secrets/cluster-stores/${encodeURIComponent(name)}`;
  }
  return `/external-secrets/stores/${encodeURIComponent(namespace)}/${encodeURIComponent(
    name,
  )}`;
}

export default function ESOPushSecretDetail({ namespace, name }: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<PushSecret | null>(null);
  const [activeTab, setActiveTab] = useState<TabKey>("overview");
  // The last evidence tab opened; the panel stays mounted once opened.
  const [evidenceTab, setEvidenceTab] = useState<EvidenceTabKey | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await esoApi.getPushSecret(namespace, name);
        if (!cancelled) setData(res.data ?? null);
      } catch {
        if (!cancelled) setError("Failed to load PushSecret");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, name]);

  if (!isClient) return null;

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

  if (!data) return null;

  const ps = data;
  const showMessage =
    ps.status === "SyncFailed" ||
    (ps.readyMessage && ps.readyMessage.length > 0);
  const sourceSecretLink = ps.sourceSecretName
    ? resourceHref("secret", ps.namespace, ps.sourceSecretName)
    : null;

  return (
    <div className="p-6 space-y-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold text-text-primary">{ps.name}</h1>
        <StatusBadge status={ps.status} />
      </div>

      <div role="tablist" className="flex gap-1 border-b border-border-primary">
        {TABS.map(({ key, label }) => {
          const active = activeTab === key;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => {
                setActiveTab(key);
                if (isEvidenceTab(key)) setEvidenceTab(key);
              }}
              className={`px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
                active
                  ? "border-brand text-text-primary"
                  : "border-transparent text-text-muted hover:text-text-primary"
              }`}
            >
              {label}
            </button>
          );
        })}
      </div>

      {activeTab === "overview" && (
        <div role="tabpanel" className="space-y-4">
          <div className="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 className="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt className="text-text-muted">Namespace</dt>
                <dd className="text-text-primary">{ps.namespace}</dd>
              </div>
              <div>
                <dt className="text-text-muted">Name</dt>
                <dd className="text-text-primary">{ps.name}</dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="text-text-muted">UID</dt>
                <dd className="text-text-primary font-mono text-xs">
                  {ps.uid}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Source Secret</dt>
                <dd>
                  {ps.sourceSecretName ? (
                    sourceSecretLink ? (
                      <a
                        href={sourceSecretLink}
                        className="text-brand hover:underline"
                      >
                        {ps.sourceSecretName}
                      </a>
                    ) : (
                      <span className="text-text-primary">
                        {ps.sourceSecretName}
                      </span>
                    )
                  ) : (
                    <span className="text-text-muted">{EM_DASH}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Refresh Interval</dt>
                <dd className="text-text-primary">
                  {ps.refreshInterval || EM_DASH}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Last Sync</dt>
                <dd className="text-text-primary" title={ps.lastSyncTime ?? ""}>
                  {ps.lastSyncTime ? timeAgo(ps.lastSyncTime) : EM_DASH}
                </dd>
              </div>
              {ps.readyReason && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Ready Reason</dt>
                  <dd className="text-text-primary">{ps.readyReason}</dd>
                </div>
              )}
              {showMessage && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Message</dt>
                  <dd className="text-text-secondary">{ps.readyMessage}</dd>
                </div>
              )}
            </dl>
          </div>

          <div className="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 className="text-sm font-semibold text-text-primary mb-3">
              Push targets ({(ps.storeRefs ?? []).length})
            </h2>
            {ps.storeRefs && ps.storeRefs.length > 0 ? (
              <ul className="text-sm space-y-1">
                {ps.storeRefs.map((s) => (
                  <li key={`${s.kind}/${s.name}`}>
                    <a
                      href={storeHref(s.kind, ps.namespace, s.name)}
                      className="text-brand hover:underline"
                    >
                      {s.kind}/{s.name}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-text-muted">No store references.</p>
            )}
          </div>
        </div>
      )}

      {evidenceTab && (
        <div hidden={!isEvidenceTab(activeTab)}>
          <ESOEvidencePanel
            kind="pushsecrets"
            namespace={namespace}
            name={name}
            uid={ps.uid}
            activeTab={evidenceTab}
          />
        </div>
      )}

      {activeTab === "chain" && (
        <div
          role="tabpanel"
          className="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
        >
          Chain visualization coming in Phase&nbsp;I.
        </div>
      )}
    </div>
  );
}
