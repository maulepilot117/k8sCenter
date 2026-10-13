import { useSignal } from "@preact/signals";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError, errorExtra } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID } from "@/lib/cluster.ts";
import {
  captureEvidence,
  incidentErrorNumber,
  isRemoteCaptureRefusal,
} from "@/lib/incident-api.ts";
import {
  CAPTURE_KINDS,
  CAPTURE_SOURCES,
  type CaptureKind,
  type CaptureResponse,
  type CaptureSourceId,
  captureSourceLabel,
  completenessLabel,
} from "@/lib/incident-types.ts";
import { busyText } from "./errors.ts";
import {
  BUTTON_PRIMARY,
  CompletenessBadge,
  FIELD,
  FOCUS_RING,
  HEADING,
  PANEL,
} from "./ui.tsx";

/**
 * The owner's capture form: one local-cluster object, chosen sources, and a
 * polite live region that reports the per-source outcome or the reason
 * nothing was recorded. Capture is pinned to the local cluster: Release D
 * captures nowhere else, and pinning it means the operator's cluster
 * selection can never send a capture somewhere it would be refused.
 */

/** The message for a failed capture; every reason the endpoint sends. */
export function captureErrorText(err: unknown): string {
  if (isRemoteCaptureRefusal(err, LOCAL_CLUSTER_ID)) {
    return "Evidence capture is supported on the local cluster only. Nothing was recorded.";
  }
  if (!(err instanceof ApiError))
    return "Capture failed. Nothing was recorded.";
  switch (err.reason) {
    case "incident_busy":
      return busyText(
        `Capture did not run: ${err.detail ?? "the incident is busy"}.`,
        err,
      );
    case "incident_capture_outcome_unknown":
      return "The capture may or may not have been recorded. Retrying is safe: evidence that was already recorded is not duplicated.";
    case "incident_closed":
      return "This incident is closed. Reopen it to capture evidence.";
    case "incident_capture_unavailable":
      return "Evidence capture is not available on this deployment.";
    case "evidence_limit_exceeded": {
      const max = incidentErrorNumber(err, "max");
      const which = errorExtra(err, "limit");
      return `This capture would exceed the incident's evidence limit${which ? ` (${which}` : ""}${which && max !== undefined ? `, max ${max})` : which ? ")" : ""}. Nothing was recorded.`;
    }
    case "scope_limit_exceeded": {
      const max = incidentErrorNumber(err, "max");
      return `This incident already holds evidence from the most distinct scopes it can${max !== undefined ? ` (${max})` : ""}. Nothing was recorded.`;
    }
  }
  if (err.status === 400) {
    return `The capture target is invalid: ${err.body?.error?.detail || err.detail || "check the namespace, kind and name"}.`;
  }
  if (err.status === 403)
    return "Only the incident owner may capture evidence.";
  if (err.status === 503 && err.detail) return `${err.detail}.`;
  return "Capture failed. Nothing was recorded.";
}

export function CapturePanel({
  incidentId,
  closed,
  onCaptured,
}: {
  incidentId: string;
  closed: boolean;
  /** Called after any capture that may have written evidence. */
  onCaptured: () => void;
}) {
  const namespace = useSignal("");
  const kind = useSignal<CaptureKind>("Deployment");
  const name = useSignal("");
  const sources = useSignal<CaptureSourceId[]>(
    CAPTURE_SOURCES.map((s) => s.id),
  );
  const running = useSignal(false);
  const result = useSignal<CaptureResponse | null>(null);
  const error = useSignal<string | null>(null);

  const submit = async (e: Event) => {
    e.preventDefault();
    if (running.value) return;
    if (!namespace.value.trim() || !name.value.trim()) {
      error.value = "Enter the namespace and name of the object to capture.";
      result.value = null;
      return;
    }
    if (sources.value.length === 0) {
      error.value = "Choose at least one source.";
      result.value = null;
      return;
    }
    running.value = true;
    error.value = null;
    result.value = null;
    try {
      result.value = await captureEvidence(incidentId, LOCAL_CLUSTER_ID, {
        namespace: namespace.value.trim(),
        kind: kind.value,
        name: name.value.trim(),
        sources: sources.value,
      });
      onCaptured();
    } catch (err) {
      error.value = captureErrorText(err);
      if (
        err instanceof ApiError &&
        (err.reason === "incident_capture_outcome_unknown" ||
          err.reason === "incident_closed")
      ) {
        onCaptured();
      }
    } finally {
      running.value = false;
    }
  };

  const toggle = (id: CaptureSourceId, on: boolean) => {
    sources.value = on
      ? CAPTURE_SOURCES.map((s) => s.id).filter(
          (s) => s === id || sources.value.includes(s),
        )
      : sources.value.filter((s) => s !== id);
  };

  const r = result.value;
  return (
    <section aria-labelledby="incident-capture-heading" class={PANEL}>
      <h2 id="incident-capture-heading" class={HEADING}>
        Capture evidence
      </h2>
      {closed ? (
        <p class="m-0 text-sm text-text-secondary">
          This incident is closed. Reopen it to capture more evidence.
        </p>
      ) : (
        <form onSubmit={submit} class="flex flex-col gap-3">
          <p class="m-0 text-xs text-text-muted">
            Captures from the local cluster, under your own access.
          </p>
          <div class="grid gap-3 sm:grid-cols-3">
            <Input
              id="capture-namespace"
              label="Namespace"
              required
              value={namespace.value}
              onInput={(ev) => {
                namespace.value = ev.currentTarget.value;
              }}
            />
            <div class="space-y-1">
              <label
                for="capture-kind"
                class="block text-sm font-medium text-text-secondary"
              >
                Kind
              </label>
              <select
                id="capture-kind"
                value={kind.value}
                onChange={(ev) => {
                  kind.value = ev.currentTarget.value as CaptureKind;
                }}
                class={FIELD}
              >
                {CAPTURE_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </div>
            <Input
              id="capture-name"
              label="Name"
              required
              value={name.value}
              onInput={(ev) => {
                name.value = ev.currentTarget.value;
              }}
            />
          </div>
          <fieldset class="m-0 flex flex-wrap gap-4 border-0 p-0">
            <legend class="mb-1 p-0 text-sm font-medium text-text-secondary">
              Sources
            </legend>
            {CAPTURE_SOURCES.map((s) => (
              <label
                key={s.id}
                class="inline-flex items-center gap-2 text-sm text-text-primary"
              >
                <input
                  type="checkbox"
                  checked={sources.value.includes(s.id)}
                  onChange={(ev) => toggle(s.id, ev.currentTarget.checked)}
                  class={FOCUS_RING}
                />
                {s.label}
              </label>
            ))}
          </fieldset>
          <div>
            <button
              type="submit"
              aria-disabled={running.value}
              class={BUTTON_PRIMARY}
            >
              {running.value ? "Capturing…" : "Capture"}
            </button>
          </div>
        </form>
      )}
      <div role="status" aria-live="polite" class="flex flex-col gap-2">
        {running.value && (
          <p class="m-0 text-sm text-text-muted">Capturing evidence…</p>
        )}
        {error.value && <Alert variant="error">{error.value}</Alert>}
        {r && (
          <div class="flex flex-col gap-2" data-capture-result="true">
            <p class="m-0 text-sm text-text-primary">
              Capture {completenessLabel(r.completeness).toLowerCase()}:{" "}
              {r.inserted} new item{r.inserted === 1 ? "" : "s"} recorded
              {r.deduplicated > 0 ? `, ${r.deduplicated} already recorded` : ""}
              {r.dropped > 0 ? `, ${r.dropped} dropped as invalid` : ""}.
            </p>
            <ul class="m-0 flex list-none flex-col gap-1 p-0">
              {r.sources.map((s) => (
                <li
                  key={s.id}
                  class="flex flex-wrap items-center gap-2 text-sm text-text-secondary"
                >
                  <span class="font-medium text-text-primary">
                    {captureSourceLabel(s.id)}
                  </span>
                  <CompletenessBadge value={s.completeness} />
                  <span>
                    {s.items} item{s.items === 1 ? "" : "s"}
                  </span>
                  {s.detail && <span class="text-text-muted">{s.detail}</span>}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </section>
  );
}
