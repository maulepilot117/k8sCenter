import { type FormEvent, useRef, useState } from "react";
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
import { useLatest } from "@/src/lib/use-latest.ts";
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
  const [namespace, setNamespace] = useState("");
  const [kind, setKind] = useState<CaptureKind>("Deployment");
  const [name, setName] = useState("");
  const [sources, setSources] = useState<CaptureSourceId[]>(
    CAPTURE_SOURCES.map((s) => s.id),
  );
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<CaptureResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);
  const onCapturedRef = useLatest(onCaptured);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (inFlight.current) return;
    if (!namespace.trim() || !name.trim()) {
      setError("Enter the namespace and name of the object to capture.");
      setResult(null);
      return;
    }
    if (sources.length === 0) {
      setError("Choose at least one source.");
      setResult(null);
      return;
    }
    inFlight.current = true;
    setRunning(true);
    setError(null);
    setResult(null);
    try {
      setResult(
        await captureEvidence(incidentId, LOCAL_CLUSTER_ID, {
          namespace: namespace.trim(),
          kind,
          name: name.trim(),
          sources,
        }),
      );
      onCapturedRef.current();
    } catch (err) {
      setError(captureErrorText(err));
      if (
        err instanceof ApiError &&
        (err.reason === "incident_capture_outcome_unknown" ||
          err.reason === "incident_closed")
      ) {
        onCapturedRef.current();
      }
    } finally {
      inFlight.current = false;
      setRunning(false);
    }
  };

  const toggle = (id: CaptureSourceId, on: boolean) => {
    setSources((current) =>
      on
        ? CAPTURE_SOURCES.map((s) => s.id).filter(
            (s) => s === id || current.includes(s),
          )
        : current.filter((s) => s !== id),
    );
  };

  return (
    <section aria-labelledby="incident-capture-heading" className={PANEL}>
      <h2 id="incident-capture-heading" className={HEADING}>
        Capture evidence
      </h2>
      {closed ? (
        <p className="m-0 text-sm text-text-secondary">
          This incident is closed. Reopen it to capture more evidence.
        </p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-3">
          <p className="m-0 text-xs text-text-muted">
            Captures from the local cluster, under your own access.
          </p>
          <div className="grid gap-3 sm:grid-cols-3">
            <Input
              id="capture-namespace"
              label="Namespace"
              required
              value={namespace}
              onChange={(ev) => setNamespace(ev.currentTarget.value)}
            />
            <div className="space-y-1">
              <label
                htmlFor="capture-kind"
                className="block text-sm font-medium text-text-secondary"
              >
                Kind
              </label>
              <select
                id="capture-kind"
                value={kind}
                onChange={(ev) =>
                  setKind(ev.currentTarget.value as CaptureKind)
                }
                className={FIELD}
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
              value={name}
              onChange={(ev) => setName(ev.currentTarget.value)}
            />
          </div>
          <fieldset className="m-0 flex flex-wrap gap-4 border-0 p-0">
            <legend className="mb-1 p-0 text-sm font-medium text-text-secondary">
              Sources
            </legend>
            {CAPTURE_SOURCES.map((s) => (
              <label
                key={s.id}
                className="inline-flex items-center gap-2 text-sm text-text-primary"
              >
                <input
                  type="checkbox"
                  checked={sources.includes(s.id)}
                  onChange={(ev) => toggle(s.id, ev.currentTarget.checked)}
                  className={FOCUS_RING}
                />
                {s.label}
              </label>
            ))}
          </fieldset>
          <div>
            <button
              type="submit"
              aria-disabled={running}
              className={BUTTON_PRIMARY}
            >
              {running ? "Capturing…" : "Capture"}
            </button>
          </div>
        </form>
      )}
      <div role="status" aria-live="polite" className="flex flex-col gap-2">
        {running && (
          <p className="m-0 text-sm text-text-muted">Capturing evidence…</p>
        )}
        {error && <Alert variant="error">{error}</Alert>}
        {result && (
          <div className="flex flex-col gap-2" data-capture-result="true">
            <p className="m-0 text-sm text-text-primary">
              Capture {completenessLabel(result.completeness).toLowerCase()}:{" "}
              {result.inserted} new item{result.inserted === 1 ? "" : "s"}{" "}
              recorded
              {result.deduplicated > 0
                ? `, ${result.deduplicated} already recorded`
                : ""}
              {result.dropped > 0
                ? `, ${result.dropped} dropped as invalid`
                : ""}
              .
            </p>
            <ul className="m-0 flex list-none flex-col gap-1 p-0">
              {result.sources.map((s) => (
                <li
                  key={s.id}
                  className="flex flex-wrap items-center gap-2 text-sm text-text-secondary"
                >
                  <span className="font-medium text-text-primary">
                    {captureSourceLabel(s.id)}
                  </span>
                  <CompletenessBadge value={s.completeness} />
                  <span>
                    {s.items} item{s.items === 1 ? "" : "s"}
                  </span>
                  {s.detail && (
                    <span className="text-text-muted">{s.detail}</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </section>
  );
}
