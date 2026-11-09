/** @jsxImportSource react */
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ApiError, apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

/**
 * GET and PUT /v1/clusters/{id}/compliance-identity: the identity the daily
 * compliance snapshot of a remote cluster is taken as.
 */
export interface ComplianceIdentity {
  username: string;
  groups: string[];
  revision: number;
  updatedAt: string;
}

export type ComplianceIdentityState = "configured" | "not-configured";

interface Props {
  clusterId: string;
  /** Called for every successful load, save and clear of this cluster's identity. */
  onIdentity: (clusterId: string, state: ComplianceIdentityState) => void;
}

const INPUT_CLASS =
  "w-full rounded-[9px] border border-border-subtle bg-elevated px-3 py-2.5 font-mono text-[13.5px] text-text-primary outline-none focus:border-accent";
const LABEL_CLASS =
  "mb-1.5 block text-[12.5px] font-semibold text-text-secondary";

/** One group per line or comma-separated; trimmed, blanks dropped. */
export function parseGroups(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((g) => g.trim())
    .filter((g) => g !== "");
}

function identityErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.reason === "impersonation_denied") {
      return "This cluster's registration credential may not impersonate that identity. Grant it the impersonate verb on the user (or serviceaccount) and on each group, then save again.";
    }
    if (err.reason === "cluster_unknown") {
      return "This cluster is no longer registered. Reload the page to refresh the list.";
    }
    if (err.status === 400) return err.detail || fallback;
  }
  return fallback;
}

// Badge ordering across form instances for one cluster. A save or clear can
// still answer after its form has closed and another form for the same
// cluster has saved or cleared again, so the badge takes the answer of the
// latest-issued change only, and a load does not overwrite a change still
// in flight (review #2).
const latestChange = new Map<string, number>();
const changesInFlight = new Map<string, number>();
let changeTicket = 0;

function beginChange(clusterId: string): number {
  const ticket = ++changeTicket;
  latestChange.set(clusterId, ticket);
  changesInFlight.set(clusterId, (changesInFlight.get(clusterId) ?? 0) + 1);
  return ticket;
}

function endChange(clusterId: string) {
  const left = (changesInFlight.get(clusterId) ?? 1) - 1;
  if (left > 0) changesInFlight.set(clusterId, left);
  else changesInFlight.delete(clusterId);
}

function isLatestChange(clusterId: string, ticket: number): boolean {
  return latestChange.get(clusterId) === ticket;
}

function changeInFlight(clusterId: string): boolean {
  return changesInFlight.has(clusterId);
}

export default function ComplianceIdentityForm({
  clusterId,
  onIdentity,
}: Props) {
  const [busy, setBusy] = useState(true);
  const [configured, setConfigured] = useState(false);
  const [username, setUsername] = useState("");
  const [groups, setGroups] = useState("");
  const baseId = useId();
  const onIdentityRef = useLatest(onIdentity);

  // Bumped by every load/save/clear and on unmount, so a late answer never
  // fills the form of another cluster. The badge is ordered separately, by
  // the module's change tickets, so a form closed mid-save still badges.
  const gen = useRef(0);

  const apply = useCallback((id: ComplianceIdentity) => {
    setUsername(id.username);
    setGroups((id.groups ?? []).join("\n"));
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    (async () => {
      try {
        const res = await apiGet<ComplianceIdentity>(
          `/v1/clusters/${clusterId}/compliance-identity`,
        );
        if (gen.current !== g) return;
        apply(res.data);
        setConfigured(true);
        if (!changeInFlight(clusterId)) {
          onIdentityRef.current(clusterId, "configured");
        }
      } catch (err) {
        if (gen.current !== g) return;
        if (
          err instanceof ApiError &&
          err.reason === "compliance_history_not_configured"
        ) {
          if (!changeInFlight(clusterId)) {
            onIdentityRef.current(clusterId, "not-configured");
          }
        } else {
          showToast(
            identityErrorMessage(err, "Could not load the identity"),
            "error",
          );
        }
      } finally {
        if (gen.current === g) setBusy(false);
      }
    })();
    return () => {
      gen.current++;
    };
  }, [clusterId, apply]);

  async function save() {
    const g = ++gen.current;
    const ticket = beginChange(clusterId);
    setBusy(true);
    try {
      const res = await apiPut<ComplianceIdentity>(
        `/v1/clusters/${clusterId}/compliance-identity`,
        {
          username: username.trim(),
          groups: parseGroups(groups),
        },
      );
      if (isLatestChange(clusterId, ticket)) {
        onIdentity(clusterId, "configured");
      }
      if (gen.current !== g) return;
      apply(res.data);
      setConfigured(true);
      showToast("Compliance identity saved", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        identityErrorMessage(err, "Failed to save the identity"),
        "error",
      );
    } finally {
      endChange(clusterId);
      if (gen.current === g) setBusy(false);
    }
  }

  async function clear() {
    if (
      !confirm(
        "Clear this compliance identity? Daily compliance snapshots on this cluster stop until one is set again.",
      )
    ) {
      return;
    }
    const g = ++gen.current;
    const ticket = beginChange(clusterId);
    setBusy(true);
    try {
      await apiDelete(`/v1/clusters/${clusterId}/compliance-identity`);
      if (isLatestChange(clusterId, ticket)) {
        onIdentity(clusterId, "not-configured");
      }
      if (gen.current !== g) return;
      setUsername("");
      setGroups("");
      setConfigured(false);
      showToast("Compliance identity cleared", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        identityErrorMessage(err, "Failed to clear the identity"),
        "error",
      );
    } finally {
      endChange(clusterId);
      if (gen.current === g) setBusy(false);
    }
  }

  return (
    <div
      data-testid="compliance-identity-form"
      className="mt-2.5 flex max-w-[520px] flex-col gap-2.5"
    >
      <p className="text-xs text-text-muted">
        Daily compliance snapshots on this cluster are taken as this identity.
        The cluster's registration credential must be allowed to impersonate it.
      </p>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-user`}>
          Compliance identity username
        </label>
        <input
          id={`${baseId}-user`}
          type="text"
          className={INPUT_CLASS}
          value={username}
          placeholder="system:serviceaccount:kubecenter:compliance"
          onChange={(e) => {
            setUsername(e.currentTarget.value);
          }}
        />
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-groups`}>
          Compliance identity groups
        </label>
        <textarea
          id={`${baseId}-groups`}
          rows={3}
          className={INPUT_CLASS}
          value={groups}
          placeholder="one group per line, or comma-separated"
          onChange={(e) => {
            setGroups(e.currentTarget.value);
          }}
        />
      </div>
      <div className="flex gap-1.5">
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={busy || username.trim() === ""}
          onClick={save}
        >
          Save
        </Button>
        {configured && (
          <Button
            type="button"
            variant="danger"
            size="sm"
            disabled={busy}
            onClick={clear}
          >
            Clear identity
          </Button>
        )}
      </div>
    </div>
  );
}
