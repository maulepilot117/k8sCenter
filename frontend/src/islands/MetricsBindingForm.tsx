/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ApiError, apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { sameOrigin } from "@/lib/origin.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

/**
 * GET and PUT /v1/clusters/{id}/metrics. The tokens themselves are never
 * returned, only whether each is stored and whether the Alertmanager token
 * is stale (its URL moved to another host since it was stored).
 */
export interface MetricsBinding {
  clusterId: string;
  prometheusUrl: string;
  hasToken: boolean;
  alertmanagerUrl: string;
  hasAlertmanagerToken?: boolean;
  alertmanagerTokenStale?: boolean;
  updatedAt: string;
}

export type MetricsState = "configured" | "not-configured";

interface Props {
  clusterId: string;
  /** Called for every successful load, save and remove of this cluster's binding. */
  onBinding: (clusterId: string, state: MetricsState) => void;
}

const INPUT_CLASS =
  "w-full rounded-[9px] border border-border-subtle bg-elevated px-3 py-2.5 font-mono text-[13.5px] text-text-primary outline-none focus:border-accent";
const LABEL_CLASS =
  "mb-1.5 block text-[12.5px] font-semibold text-text-secondary";
const CHECK_CLASS = "inline-flex items-center gap-2 text-xs text-text-primary";

/**
 * The server's own message for a rejected binding (400) or one another save
 * changed meanwhile (409), a fixed sentence for an unreachable Prometheus or
 * Alertmanager (502, whose raw error never leaves the server).
 */
function metricsErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.status === 502) {
      return err.detail?.includes("Alertmanager")
        ? "Could not reach Alertmanager at that URL"
        : "Could not reach Prometheus at that URL";
    }
    if (err.status === 400 || err.status === 409) {
      return err.detail || fallback;
    }
  }
  return fallback;
}

export default function MetricsBindingForm({ clusterId, onBinding }: Props) {
  const [busy, setBusy] = useState(true);
  const [configured, setConfigured] = useState(false);
  const [promUrl, setPromUrl] = useState("");
  const [promToken, setPromToken] = useState("");
  const [hasPromToken, setHasPromToken] = useState(false);
  // A blank token field keeps the stored token; the clear box sends the
  // explicit empty token. A typed token still wins.
  const [clearPromToken, setClearPromToken] = useState(false);
  const [amUrl, setAmUrl] = useState("");
  const [amToken, setAmToken] = useState("");
  const [hasAmToken, setHasAmToken] = useState(false);
  const [clearAmToken, setClearAmToken] = useState(false);
  const [amStale, setAmStale] = useState(false);
  // The Alertmanager URL as stored, to tell an edit to another host.
  const [storedAmUrl, setStoredAmUrl] = useState("");
  const [baseId] = useState(
    () => `mb-${Math.random().toString(36).slice(2, 8)}`,
  );
  const onBindingRef = useLatest(onBinding);

  // Bumped by every load/save/remove and when the form unmounts (closed or
  // moved to another cluster). A response writes the form only while its
  // generation is current, so a late answer never fills (or is saved from)
  // another cluster's form. The badge callback is not gated: a save that
  // landed is true of its cluster whatever the form shows now.
  const gen = useRef(0);

  const applyBinding = useCallback((b: MetricsBinding) => {
    setPromUrl(b.prometheusUrl);
    setAmUrl(b.alertmanagerUrl ?? "");
    setStoredAmUrl(b.alertmanagerUrl ?? "");
    setHasPromToken(b.hasToken);
    setHasAmToken(b.hasAlertmanagerToken ?? false);
    setAmStale(b.alertmanagerTokenStale ?? false);
    setPromToken("");
    setAmToken("");
    setClearPromToken(false);
    setClearAmToken(false);
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    (async () => {
      try {
        const res = await apiGet<MetricsBinding>(
          `/v1/clusters/${clusterId}/metrics`,
        );
        if (gen.current !== g) return;
        applyBinding(res.data);
        setConfigured(true);
        onBindingRef.current(clusterId, "configured");
      } catch (err) {
        if (gen.current !== g) return;
        if (
          err instanceof ApiError &&
          err.reason === "metrics_not_configured"
        ) {
          onBindingRef.current(clusterId, "not-configured");
        } else {
          showToast(
            metricsErrorMessage(err, "Could not load the binding"),
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
      const body: Record<string, string> = {
        prometheusUrl: promUrl.trim(),
      };
      // Omitted keeps the stored token, "" clears it, a value replaces it.
      if (promToken !== "") body.token = promToken;
      else if (clearPromToken) body.token = "";
      if (amUrl.trim() !== "") body.alertmanagerUrl = amUrl.trim();
      if (amToken !== "") body.alertmanagerToken = amToken;
      else if (clearAmToken) body.alertmanagerToken = "";
      const res = await apiPut<MetricsBinding>(
        `/v1/clusters/${clusterId}/metrics`,
        body,
      );
      onBinding(clusterId, "configured");
      if (gen.current !== g) return;
      applyBinding(res.data);
      setConfigured(true);
      showToast("Metrics binding saved", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        metricsErrorMessage(err, "Failed to save the binding"),
        "error",
      );
    } finally {
      if (gen.current === g) setBusy(false);
    }
  }

  async function remove() {
    if (
      !confirm(
        "Remove this metrics binding? The Alertmanager URL and token are removed too.",
      )
    ) {
      return;
    }
    const g = ++gen.current;
    setBusy(true);
    try {
      await apiDelete(`/v1/clusters/${clusterId}/metrics`);
      onBinding(clusterId, "not-configured");
      if (gen.current !== g) return;
      applyBinding({
        clusterId,
        prometheusUrl: "",
        hasToken: false,
        alertmanagerUrl: "",
        updatedAt: "",
      });
      setConfigured(false);
      showToast("Metrics binding removed", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        metricsErrorMessage(err, "Failed to remove the binding"),
        "error",
      );
    } finally {
      if (gen.current === g) setBusy(false);
    }
  }

  // The same origin rule the server applies (lib/origin.ts), so an explicit
  // default port counts as a move just as it does there.
  const amUrlMoved =
    hasAmToken && amUrl.trim() !== "" && !sameOrigin(storedAmUrl, amUrl);
  // A stale token is reported with hasAlertmanagerToken false (the store
  // withholds a token whose origin no longer matches), so staleness alone
  // must prompt. Removing the Alertmanager URL clears the token server-side.
  const amNeedsEntry = amUrlMoved || (amStale && amUrl.trim() !== "");
  const amTokenMissing = amNeedsEntry && amToken === "" && !clearAmToken;
  const amMessageId = `${baseId}-am-token-msg`;

  return (
    <div
      data-testid="metrics-form"
      className="mt-2.5 flex max-w-[520px] flex-col gap-2.5"
    >
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-prom-url`}>
          Prometheus URL
        </label>
        <input
          id={`${baseId}-prom-url`}
          type="text"
          className={INPUT_CLASS}
          value={promUrl}
          autoComplete="off"
          placeholder="https://prometheus.example.com"
          onChange={(e) => setPromUrl(e.currentTarget.value)}
        />
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-prom-token`}>
          Prometheus bearer token (optional)
        </label>
        <input
          id={`${baseId}-prom-token`}
          type="password"
          className={INPUT_CLASS}
          value={promToken}
          autoComplete="new-password"
          placeholder="leave blank to keep the stored token"
          onChange={(e) => setPromToken(e.currentTarget.value)}
        />
        {hasPromToken && (
          <div className="mt-1.5 flex flex-col gap-1">
            <span className="text-xs text-text-muted">
              A token is stored for this binding.
            </span>
            <label className={CHECK_CLASS}>
              <input
                type="checkbox"
                checked={clearPromToken}
                disabled={promToken !== ""}
                onChange={(ev) => setClearPromToken(ev.currentTarget.checked)}
              />
              Clear the stored Prometheus token on save
            </label>
          </div>
        )}
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-am-url`}>
          Alertmanager URL (optional)
        </label>
        <input
          id={`${baseId}-am-url`}
          type="text"
          className={INPUT_CLASS}
          value={amUrl}
          autoComplete="off"
          placeholder="https://alertmanager.example.com"
          onChange={(e) => setAmUrl(e.currentTarget.value)}
        />
      </div>
      <div>
        <label className={LABEL_CLASS} htmlFor={`${baseId}-am-token`}>
          Alertmanager bearer token (optional)
        </label>
        <input
          id={`${baseId}-am-token`}
          type="password"
          className={INPUT_CLASS}
          value={amToken}
          autoComplete="new-password"
          required={amNeedsEntry && !clearAmToken}
          aria-required={amNeedsEntry && !clearAmToken}
          aria-invalid={amTokenMissing}
          aria-describedby={amNeedsEntry ? amMessageId : undefined}
          placeholder={
            amNeedsEntry
              ? "enter the token for this Alertmanager"
              : "leave blank to keep the stored token"
          }
          onChange={(e) => setAmToken(e.currentTarget.value)}
        />
        {amNeedsEntry && (
          <p id={amMessageId} className="mt-1.5 text-xs text-warning">
            A different Alertmanager host needs its token entered again. The
            stored token will not be sent there.
          </p>
        )}
        {(hasAmToken || amStale) && (
          <div className="mt-1.5 flex flex-col gap-1">
            {!amNeedsEntry && (
              <span className="text-xs text-text-muted">
                An Alertmanager token is stored for this binding.
              </span>
            )}
            <label className={CHECK_CLASS}>
              <input
                type="checkbox"
                checked={clearAmToken}
                disabled={amToken !== ""}
                onChange={(ev) => setClearAmToken(ev.currentTarget.checked)}
              />
              Clear the stored Alertmanager token on save
            </label>
          </div>
        )}
        <p className="mt-1.5 text-[11.5px] text-text-muted">
          A token shared by Prometheus and Alertmanager must be entered in both
          fields.
        </p>
      </div>
      <div className="flex gap-1.5">
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={busy || promUrl.trim() === "" || amTokenMissing}
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
