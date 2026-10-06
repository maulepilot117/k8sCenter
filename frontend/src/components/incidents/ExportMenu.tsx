import { useSignal } from "@preact/signals";
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
  const pending = useSignal<ExportFormat | null>(null);
  const error = useSignal<string | null>(null);

  const download = async (format: ExportFormat) => {
    if (pending.value) return;
    pending.value = format;
    error.value = null;
    try {
      const { blob, filename } = await exportIncident(incidentId, format);
      saveBlob(blob, filename);
    } catch (err) {
      error.value = exportErrorText(err);
    } finally {
      pending.value = null;
    }
  };

  return (
    <div
      class="flex flex-wrap items-center gap-2"
      role="group"
      aria-label="Export"
    >
      <span class="text-sm text-text-secondary">Export</span>
      <button
        type="button"
        aria-disabled={pending.value !== null}
        onClick={() => download("json")}
        class={BUTTON_SECONDARY}
      >
        {pending.value === "json" ? "Exporting…" : "JSON"}
      </button>
      <button
        type="button"
        aria-disabled={pending.value !== null}
        onClick={() => download("markdown")}
        class={BUTTON_SECONDARY}
      >
        {pending.value === "markdown" ? "Exporting…" : "Markdown"}
      </button>
      {error.value && (
        <div role="alert" class="w-full">
          <Alert variant="error">{error.value}</Alert>
        </div>
      )}
    </div>
  );
}
