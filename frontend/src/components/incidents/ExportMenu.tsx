import { useSignal } from "@preact/signals";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError, getAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID } from "@/lib/cluster.ts";
import { exportUrl, getIncident } from "@/lib/incident-api.ts";
import type { ExportFormat } from "@/lib/incident-types.ts";
import { simpleErrorText } from "./errors.ts";
import { BUTTON_SECONDARY } from "./ui.tsx";

/**
 * JSON and Markdown export. The access token lives in memory, so a plain
 * link cannot carry it: the file is fetched with the Authorization header and
 * saved from a Blob.
 */

/**
 * Fetches an export with the Authorization header and returns the file. On a
 * 401 the token has expired: one cheap read through api.ts refreshes it (or
 * sends the user to log in), then the export is fetched once more.
 */
export async function fetchExport(
  id: string,
  format: ExportFormat,
  signal?: AbortSignal,
): Promise<{ blob: Blob; filename: string }> {
  const url = exportUrl(id, format);
  const attempt = () => {
    const headers = new Headers({ "X-Cluster-ID": LOCAL_CLUSTER_ID });
    const token = getAccessToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return fetch(url, { headers, credentials: "include", signal });
  };
  let res = await attempt();
  if (res.status === 401) {
    await getIncident(id, { limit: 1 }, signal);
    res = await attempt();
  }
  if (!res.ok) {
    let body: { error?: Record<string, unknown> } | undefined;
    try {
      body = await res.json();
    } catch {
      // not JSON
    }
    const e = body?.error as { code?: number; message?: string } | undefined;
    throw new ApiError(
      res.status,
      e?.code ?? res.status,
      e?.message ?? res.statusText,
      body as ConstructorParameters<typeof ApiError>[3],
    );
  }
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const match = /filename="([^"]+)"/.exec(disposition);
  const filename =
    match?.[1] ?? `incident-${id}.${format === "markdown" ? "md" : "json"}`;
  return { blob: await res.blob(), filename };
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
      const { blob, filename } = await fetchExport(incidentId, format);
      saveBlob(blob, filename);
    } catch (err) {
      error.value = simpleErrorText(err, "The export could not be downloaded.");
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
