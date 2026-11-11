import { useRef, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import { exportIncident } from "@/lib/incident-api.ts";
import type { ExportFormat } from "@/lib/incident-types.ts";
import { simpleErrorText } from "./errors.ts";
import { BUTTON_SECONDARY } from "./ui.tsx";

/**
 * JSON and Markdown export. The file is fetched through `exportIncident`
 * (the shared authenticated transport) and saved from a Blob, because the
 * access token lives in memory and a plain link cannot carry it.
 */

function exportErrorText(err: unknown): string {
  if (err instanceof ApiError && err.status === 429) {
    return "Exports are rate-limited. Try again shortly.";
  }
  return simpleErrorText(err, "The export could not be downloaded.");
}

function saveBlob(blob: Blob, filename: string) {
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 0);
}

export function ExportMenu({ incidentId }: { incidentId: string }) {
  const [pending, setPending] = useState<ExportFormat | null>(null);
  const [error, setError] = useState<string | null>(null);
  // A second click can land before the render that shows `pending`.
  const inFlight = useRef(false);

  const download = async (format: ExportFormat) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(format);
    setError(null);
    try {
      const { blob, filename } = await exportIncident(incidentId, format);
      saveBlob(blob, filename);
    } catch (err) {
      setError(exportErrorText(err));
    } finally {
      inFlight.current = false;
      setPending(null);
    }
  };

  return (
    <div
      className="flex flex-wrap items-center gap-2"
      role="group"
      aria-label="Export"
    >
      <span className="text-sm text-text-secondary">Export</span>
      <button
        type="button"
        aria-disabled={pending !== null}
        onClick={() => download("json")}
        className={BUTTON_SECONDARY}
      >
        {pending === "json" ? "Exporting…" : "JSON"}
      </button>
      <button
        type="button"
        aria-disabled={pending !== null}
        onClick={() => download("markdown")}
        className={BUTTON_SECONDARY}
      >
        {pending === "markdown" ? "Exporting…" : "Markdown"}
      </button>
      {error && (
        <div role="alert" className="w-full">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </div>
  );
}
