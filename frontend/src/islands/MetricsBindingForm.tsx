import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Button } from "@/components/ui/Button.tsx";
import { ApiError, apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { showToast } from "@/src/islands/ToastProvider.tsx";

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

function originOf(raw: string): string {
  try {
    return new URL(raw.trim()).origin;
  } catch {
    return raw.trim();
  }
}

export default function MetricsBindingForm({ clusterId, onBinding }: Props) {
  const busy = useSignal(true);
  const configured = useSignal(false);
  const promUrl = useSignal("");
  const promToken = useSignal("");
  const hasPromToken = useSignal(false);
  // A blank token field keeps the stored token; the clear box sends the
  // explicit empty token. A typed token still wins.
  const clearPromToken = useSignal(false);
  const amUrl = useSignal("");
  const amToken = useSignal("");
  const hasAmToken = useSignal(false);
  const clearAmToken = useSignal(false);
  const amStale = useSignal(false);
  // The Alertmanager URL as stored, to tell an edit to another host.
  const storedAmUrl = useSignal("");
  const baseId = useRef(`mb-${Math.random().toString(36).slice(2, 8)}`).current;

  // Bumped by every load/save/remove and when the form unmounts (closed or
  // moved to another cluster). A response writes the form only while its
  // generation is current, so a late answer never fills (or is saved from)
  // another cluster's form. The badge callback is not gated: a save that
  // landed is true of its cluster whatever the form shows now.
  const gen = useRef(0);

  function applyBinding(b: MetricsBinding) {
    promUrl.value = b.prometheusUrl;
    amUrl.value = b.alertmanagerUrl ?? "";
    storedAmUrl.value = b.alertmanagerUrl ?? "";
    hasPromToken.value = b.hasToken;
    hasAmToken.value = b.hasAlertmanagerToken ?? false;
    amStale.value = b.alertmanagerTokenStale ?? false;
    promToken.value = "";
    amToken.value = "";
    clearPromToken.value = false;
    clearAmToken.value = false;
  }

  useEffect(() => {
    const g = ++gen.current;
    (async () => {
      try {
        const res = await apiGet<MetricsBinding>(
          `/v1/clusters/${clusterId}/metrics`,
        );
        if (gen.current !== g) return;
        applyBinding(res.data);
        configured.value = true;
        onBinding(clusterId, "configured");
      } catch (err) {
        if (gen.current !== g) return;
        if (
          err instanceof ApiError &&
          err.reason === "metrics_not_configured"
        ) {
          onBinding(clusterId, "not-configured");
        } else {
          showToast(
            metricsErrorMessage(err, "Could not load the binding"),
            "error",
          );
        }
      } finally {
        if (gen.current === g) busy.value = false;
      }
    })();
    return () => {
      gen.current++;
    };
  }, [clusterId]);

  async function save() {
    const g = ++gen.current;
    busy.value = true;
    try {
      const body: Record<string, string> = {
        prometheusUrl: promUrl.value.trim(),
      };
      // Omitted keeps the stored token, "" clears it, a value replaces it.
      if (promToken.value !== "") body.token = promToken.value;
      else if (clearPromToken.value) body.token = "";
      if (amUrl.value.trim() !== "") body.alertmanagerUrl = amUrl.value.trim();
      if (amToken.value !== "") body.alertmanagerToken = amToken.value;
      else if (clearAmToken.value) body.alertmanagerToken = "";
      const res = await apiPut<MetricsBinding>(
        `/v1/clusters/${clusterId}/metrics`,
        body,
      );
      onBinding(clusterId, "configured");
      if (gen.current !== g) return;
      applyBinding(res.data);
      configured.value = true;
      showToast("Metrics binding saved", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        metricsErrorMessage(err, "Failed to save the binding"),
        "error",
      );
    } finally {
      if (gen.current === g) busy.value = false;
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
    busy.value = true;
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
      configured.value = false;
      showToast("Metrics binding removed", "success");
    } catch (err) {
      if (gen.current !== g) return;
      showToast(
        metricsErrorMessage(err, "Failed to remove the binding"),
        "error",
      );
    } finally {
      if (gen.current === g) busy.value = false;
    }
  }

  const amUrlMoved =
    hasAmToken.value &&
    amUrl.value.trim() !== "" &&
    originOf(amUrl.value) !== originOf(storedAmUrl.value);
  const amNeedsEntry = hasAmToken.value && (amStale.value || amUrlMoved);
  const amTokenMissing =
    amNeedsEntry && amToken.value === "" && !clearAmToken.value;
  const amMessageId = `${baseId}-am-token-msg`;

  return (
    <div
      data-testid="metrics-form"
      class="mt-2.5 flex max-w-[520px] flex-col gap-2.5"
    >
      <div>
        <label class={LABEL_CLASS} for={`${baseId}-prom-url`}>
          Prometheus URL
        </label>
        <input
          id={`${baseId}-prom-url`}
          type="text"
          class={INPUT_CLASS}
          value={promUrl.value}
          placeholder="https://prometheus.example.com"
          onInput={(e) => {
            promUrl.value = e.currentTarget.value;
          }}
        />
      </div>
      <div>
        <label class={LABEL_CLASS} for={`${baseId}-prom-token`}>
          Prometheus bearer token (optional)
        </label>
        <input
          id={`${baseId}-prom-token`}
          type="password"
          class={INPUT_CLASS}
          value={promToken.value}
          placeholder="leave blank to keep the stored token"
          onInput={(e) => {
            promToken.value = e.currentTarget.value;
          }}
        />
        {hasPromToken.value && (
          <div class="mt-1.5 flex flex-col gap-1">
            <span class="text-xs text-text-muted">
              A token is stored for this binding.
            </span>
            <label class={CHECK_CLASS}>
              <input
                type="checkbox"
                checked={clearPromToken.value}
                disabled={promToken.value !== ""}
                onChange={(ev) => {
                  clearPromToken.value = ev.currentTarget.checked;
                }}
              />
              Clear the stored Prometheus token on save
            </label>
          </div>
        )}
      </div>
      <div>
        <label class={LABEL_CLASS} for={`${baseId}-am-url`}>
          Alertmanager URL (optional)
        </label>
        <input
          id={`${baseId}-am-url`}
          type="text"
          class={INPUT_CLASS}
          value={amUrl.value}
          placeholder="https://alertmanager.example.com"
          onInput={(e) => {
            amUrl.value = e.currentTarget.value;
          }}
        />
      </div>
      <div>
        <label class={LABEL_CLASS} for={`${baseId}-am-token`}>
          Alertmanager bearer token (optional)
        </label>
        <input
          id={`${baseId}-am-token`}
          type="password"
          class={INPUT_CLASS}
          value={amToken.value}
          required={amNeedsEntry && !clearAmToken.value}
          aria-required={amNeedsEntry && !clearAmToken.value}
          aria-invalid={amTokenMissing}
          aria-describedby={amNeedsEntry ? amMessageId : undefined}
          placeholder={
            amNeedsEntry
              ? "enter the token for this Alertmanager"
              : "leave blank to keep the stored token"
          }
          onInput={(e) => {
            amToken.value = e.currentTarget.value;
          }}
        />
        {amNeedsEntry && (
          <p id={amMessageId} class="mt-1.5 text-xs text-warning">
            A different Alertmanager host needs its token entered again. The
            stored token will not be sent there.
          </p>
        )}
        {hasAmToken.value && (
          <div class="mt-1.5 flex flex-col gap-1">
            {!amNeedsEntry && (
              <span class="text-xs text-text-muted">
                An Alertmanager token is stored for this binding.
              </span>
            )}
            <label class={CHECK_CLASS}>
              <input
                type="checkbox"
                checked={clearAmToken.value}
                disabled={amToken.value !== ""}
                onChange={(ev) => {
                  clearAmToken.value = ev.currentTarget.checked;
                }}
              />
              Clear the stored Alertmanager token on save
            </label>
          </div>
        )}
        <p class="mt-1.5 text-[11.5px] text-text-muted">
          A token shared by Prometheus and Alertmanager must be entered in both
          fields.
        </p>
      </div>
      <div class="flex gap-1.5">
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={busy.value || promUrl.value.trim() === "" || amTokenMissing}
          onClick={save}
        >
          Save
        </Button>
        {configured.value && (
          <Button
            type="button"
            variant="danger"
            size="sm"
            disabled={busy.value}
            onClick={remove}
          >
            Remove binding
          </Button>
        )}
      </div>
    </div>
  );
}
