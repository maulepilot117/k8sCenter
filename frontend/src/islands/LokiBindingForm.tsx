/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ApiError, apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { sameOrigin } from "@/lib/origin.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

/**
 * GET and PUT /v1/clusters/{id}/logs. The token itself is never returned:
 * hasToken says a usable one is stored, and tokenStale that a stored one was
 * entered for another host and must be entered again before it is used.
 */
export interface LokiBinding {
  clusterId: string;
  url: string;
  tenantId: string;
  hasToken: boolean;
  tokenStale: boolean;
  updatedAt: string;
}

/**
 * "configured": log search uses the Loki URL set here. "not-configured":
 * log search finds the cluster's in-cluster Loki itself.
 */
export type LokiState = "configured" | "not-configured";

interface Props {
  clusterId: string;
  /** Called for every successful load, save and remove of this cluster's binding. */
  onBinding: (clusterId: string, state: LokiState) => void;
}

const INPUT_CLASS =
  "w-full rounded-[9px] border border-border-subtle bg-elevated px-3 py-2.5 font-mono text-[13.5px] text-text-primary outline-none focus:border-accent";
const LABEL_CLASS =
  "mb-1.5 block text-[12.5px] font-semibold text-text-secondary";
const CHECK_CLASS = "inline-flex items-center gap-2 text-xs text-text-primary";

/**
 * The server's own fixed message for a rejected binding (400), one another
 * save changed meanwhile (409) or a store that could not be read (503). Every
 * one of them is a fixed sentence; raw errors never leave the server.
 */
function lokiErrorMessage(err: unknown, fallback: string): string {
  if (
    err instanceof ApiError &&
    (err.status === 400 || err.status === 409 || err.status === 503)
  ) {
    return err.detail || fallback;
  }
  return fallback;
}

export default function LokiBindingForm({ clusterId, onBinding }: Props) {
  const [busy, setBusy] = useState(true);
  const [configured, setConfigured] = useState(false);
  const [url, setUrl] = useState("");
  const [tenant, setTenant] = useState("");
  const [token, setToken] = useState("");
  const [hasToken, setHasToken] = useState(false);
  const [tokenStale, setTokenStale] = useState(false);
  // A blank token field keeps the stored token; the clear box sends the
  // explicit empty token. A typed token still wins.
  const [clearToken, setClearToken] = useState(false);
  // The URL as stored, to tell an edit to another host.
  const [storedUrl, setStoredUrl] = useState("");
  // The stored binding could not be read. Saving then would replace a
  // tenant (and judge a token) the form never saw, so Save stays off.
  const [loadFailed, setLoadFailed] = useState(false);
  const [baseId] = useState(
    () => `lb-${Math.random().toString(36).slice(2, 8)}`,
  );
  const onBindingRef = useLatest(onBinding);

  // Bumped by every load/save/remove and when the form unmounts (closed or
  // moved to another cluster). A response writes the form only while its
  // generation is current, so a late answer never fills (or is saved from)
  // another cluster's form. The badge callback is not gated: a save that
  // landed is true of its cluster whatever the form shows now.
  const gen = useRef(0);

  const applyBinding = useCallback((b: LokiBinding) => {
    setUrl(b.url);
    setStoredUrl(b.url);
    setTenant(b.tenantId ?? "");
    setHasToken(b.hasToken);
    setTokenStale(b.tokenStale ?? false);
    setToken("");
    setClearToken(false);
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    (async () => {
      try {
        const res = await apiGet<LokiBinding>(`/v1/clusters/${clusterId}/logs`);
        if (gen.current !== g) return;
        applyBinding(res.data);
        setConfigured(true);
        onBindingRef.current(clusterId, "configured");
      } catch (err) {
        if (gen.current !== g) return;
        if (err instanceof ApiError && err.reason === "loki_not_configured") {
          onBindingRef.current(clusterId, "not-configured");
        } else {
          setLoadFailed(true);
          showToast(
            lokiErrorMessage(err, "Could not load the Loki binding"),
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
  }, [clusterId, applyBinding]);

  async function save() {
    const g = ++gen.current;
    setBusy(true);
    try {
      // The URL and tenant are replaced on every save, so the current tenant
      // is always sent: omitting it would clear the stored one.
      const body: Record<string, string> = {
        url: url.trim(),
        tenantId: tenant.trim(),
      };
      // Omitted keeps the stored token, "" clears it, a value replaces it.
      if (token !== "") body.token = token;
      else if (clearToken) body.token = "";
      const res = await apiPut<LokiBinding>(
        `/v1/clusters/${clusterId}/logs`,
        body,
      );
      onBinding(clusterId, "configured");
      if (gen.current !== g) return;
      applyBinding(res.data);
      setConfigured(true);
      showToast("Loki binding saved", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        lokiErrorMessage(err, "Failed to save the Loki binding"),
        "error",
      );
    } finally {
      if (gen.current === g) setBusy(false);
    }
  }

  async function remove() {
    if (
      !confirm(
        "Remove this Loki binding? The token and tenant are removed too, and log search goes back to finding the cluster's own Loki.",
      )
    ) {
      return;
    }
    const g = ++gen.current;
    setBusy(true);
    const removed = () => {
      onBinding(clusterId, "not-configured");
      if (gen.current !== g) return;
      applyBinding({
        clusterId,
        url: "",
        tenantId: "",
        hasToken: false,
        tokenStale: false,
        updatedAt: "",
      });
      setConfigured(false);
      showToast("Loki binding removed", "success");
    };
    try {
      await apiDelete(`/v1/clusters/${clusterId}/logs`);
      removed();
    } catch (err) {
      // Already gone (another admin removed it): the outcome asked for.
      if (err instanceof ApiError && err.reason === "loki_not_configured") {
        removed();
        return;
      }
      if (gen.current !== g) return;
      showToast(
        lokiErrorMessage(err, "Failed to remove the Loki binding"),
        "error",
      );
    } finally {
      if (gen.current === g) setBusy(false);
    }
  }

  // The same origin rule the server applies (lib/origin.ts), so an explicit
  // default port counts as a move just as it does there.
  const urlMoved = hasToken && url.trim() !== "" && !sameOrigin(storedUrl, url);
  // A stale token is reported with hasToken false (the store withholds a
  // token whose origin no longer matches), and the server refuses any save
  // that would keep it, so staleness alone must prompt.
  const needsEntry = urlMoved || tokenStale;
  const tokenMissing = needsEntry && token === "" && !clearToken;
  const messageId = `${baseId}-token-msg`;

  return (
    <div
      data-testid="loki-form"
      className="mt-2.5 flex max-w-[520px] flex-col gap-2.5"
    >
      <p className="text-xs text-text-muted">
        Optional. Without a Loki URL, log search finds the cluster's own Loki
        Service and reaches it through the cluster's API server.
      </p>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-url`}>
          Loki URL
        </label>
        <input
          id={`${baseId}-url`}
          type="text"
          className={INPUT_CLASS}
          value={url}
          autoComplete="off"
          placeholder="https://loki.example.com"
          onChange={(e) => setUrl(e.currentTarget.value)}
        />
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-token`}>
          Bearer token (optional)
        </label>
        <input
          id={`${baseId}-token`}
          type="password"
          className={INPUT_CLASS}
          value={token}
          autoComplete="new-password"
          required={needsEntry && !clearToken}
          aria-required={needsEntry && !clearToken}
          aria-invalid={tokenMissing}
          aria-describedby={needsEntry ? messageId : undefined}
          placeholder={
            needsEntry
              ? "enter the token for this Loki"
              : "leave blank to keep the stored token"
          }
          onChange={(e) => setToken(e.currentTarget.value)}
        />
        {needsEntry && (
          <p id={messageId} className="mt-1.5 text-xs text-warning">
            A different Loki host or port needs its token entered again. The
            stored token will not be sent there.
          </p>
        )}
        {(hasToken || tokenStale) && (
          <div className="mt-1.5 flex flex-col gap-1">
            {!needsEntry && (
              <span className="text-xs text-text-muted">
                A token is stored for this binding.
              </span>
            )}
            <label className={CHECK_CLASS}>
              <input
                type="checkbox"
                checked={clearToken}
                disabled={token !== ""}
                onChange={(ev) => setClearToken(ev.currentTarget.checked)}
              />
              Clear the stored token on save
            </label>
          </div>
        )}
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-tenant`}>
          Tenant ID (optional)
        </label>
        <input
          id={`${baseId}-tenant`}
          type="text"
          className={INPUT_CLASS}
          value={tenant}
          autoComplete="off"
          placeholder="team-a"
          onChange={(e) => setTenant(e.currentTarget.value)}
        />
        <p className="mt-1.5 text-[11.5px] text-text-muted">
          Sent as X-Scope-OrgID. Separate several tenants with |.
        </p>
      </div>
      {loadFailed && (
        <p role="alert" className="text-xs text-error">
          The stored binding could not be loaded, so it cannot be saved here.
          Close and reopen the form to try again.
        </p>
      )}
      <div className="flex gap-1.5">
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={busy || loadFailed || url.trim() === "" || tokenMissing}
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
            onClick={remove}
          >
            Remove binding
          </Button>
        )}
      </div>
    </div>
  );
}
