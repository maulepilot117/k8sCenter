import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import { listReceipts, type ReceiptPage } from "@/lib/change-api.ts";
import {
  clusterDisplayName,
  RECORDS_UNAVAILABLE,
  RECORDS_UNSUPPORTED,
  receiptHref,
  shortId,
} from "@/lib/change-copy.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { ChangeStateBadges } from "@/src/components/changes/ChangeStateBadges.tsx";

/**
 * The caller's own change receipts, newest first (GET /v1/changes). Receipts
 * shared with the caller are reachable by link only and are not listed here,
 * which the page says.
 *
 * While another page loads, the last page stays on screen and the pager
 * stays mounted with its buttons disabled, so the button a keyboard or
 * screen-reader user pressed keeps focus instead of being destroyed.
 *
 * The root element is identical during SSR and after hydration (the loading
 * state renders on the server), so there is no placeholder root to diverge.
 */

const ROOT_CLASS = "flex flex-col gap-5";
const PAGE_SIZE = 20;

/**
 * A pager button that stays focusable while inactive. A real `disabled`
 * attribute would blur the button the user just pressed (browsers move focus
 * off a control that becomes disabled), so inactivity is `aria-disabled`
 * plus a guarded handler.
 */
function PagerButton({
  label,
  inactive,
  onActivate,
}: {
  label: string;
  inactive: boolean;
  onActivate: () => void;
}) {
  return (
    <button
      type="button"
      aria-disabled={inactive}
      onClick={() => {
        if (!inactive) onActivate();
      }}
      class="cursor-pointer rounded-md border border-border-primary bg-transparent px-3 py-1.5 font-medium text-text-secondary aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
    >
      {label}
    </button>
  );
}

function listErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 503) return RECORDS_UNAVAILABLE;
    if (err.status === 404) return RECORDS_UNSUPPORTED;
  }
  return "Could not load your recorded changes.";
}

export default function ChangeReceiptList() {
  /** The page being requested. */
  const page = useSignal(1);
  /** Bumped to re-request the same page (a retry after an error). */
  const reload = useSignal(0);
  /**
   * The last page that loaded, with the page number it is. Kept while the
   * next one loads; the pager is always labelled from it, never from the
   * requested page, so a failed load cannot relabel the old rows.
   */
  const shown = useSignal<{ data: ReceiptPage; page: number } | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);

  // Navigation is relative to the page on screen. Asking for the page
  // already requested (after it failed) retries it.
  const goTo = (n: number) => {
    if (n === page.peek()) reload.value++;
    else page.value = n;
  };

  useEffect(() => {
    document.title = "Recorded changes - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  const requested = page.value;
  const attempt = reload.value;
  useEffect(() => {
    const controller = new AbortController();
    loading.value = true;
    listReceipts({ page: requested, pageSize: PAGE_SIZE }, controller.signal)
      .then((res) => {
        if (controller.signal.aborted) return;
        shown.value = { data: res, page: requested };
        error.value = null;
        loading.value = false;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        error.value = listErrorText(err);
        loading.value = false;
      });
    return () => controller.abort();
  }, [requested, attempt]);

  const data = shown.value?.data ?? null;
  const current = shown.value?.page ?? requested;
  const busy = loading.value;
  const failed = error.value !== null;
  const pages = data ? Math.max(1, Math.ceil(data.total / PAGE_SIZE)) : 1;

  return (
    <div class={ROOT_CLASS}>
      <div class="flex flex-col gap-1">
        <h1 class="m-0 text-2xl font-bold tracking-tight text-text-primary">
          Recorded changes
        </h1>
        <p class="m-0 text-sm text-text-muted">
          Changes you applied from YAML Apply with “Keep a record of this
          change” on. Each receipt records what the cluster accepted and,
          separately, whether the result was verified. Receipts shared with you
          open from their link and are not listed here.
        </p>
      </div>

      {busy && !data && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading recorded changes…
        </p>
      )}

      {error.value && (
        <div role="alert">
          <Alert variant="error">{error.value}</Alert>
        </div>
      )}

      {data && !error.value && data.total === 0 && (
        <p class="m-0 text-sm text-text-secondary">
          No recorded changes yet.{" "}
          <a href="/tools/yaml-apply" class="font-medium text-accent">
            Apply YAML
          </a>{" "}
          with “Keep a record of this change” on to create one.
        </p>
      )}

      {data && !failed && data.items.length > 0 && (
        <div
          aria-busy={busy}
          class="overflow-x-auto rounded-lg border border-border-subtle bg-surface"
        >
          <table class="w-full border-collapse text-left text-sm">
            <caption class="sr-only">Your recorded changes</caption>
            <thead>
              <tr class="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" class="px-3 py-2 font-semibold">
                  Receipt
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  State
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Cluster
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Documents
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Recorded
                </th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((r) => (
                <tr key={r.operationId} class="border-t border-border-subtle">
                  <td class="px-3 py-2 align-top">
                    <a
                      href={receiptHref(r.operationId)}
                      class="font-mono text-accent"
                      aria-label={`Change receipt ${r.operationId}`}
                    >
                      {shortId(r.operationId)}
                    </a>
                  </td>
                  <td class="px-3 py-2 align-top">
                    <ChangeStateBadges
                      state={r.state}
                      verification={r.verification.state}
                    />
                  </td>
                  <td class="px-3 py-2 align-top text-text-secondary">
                    {clusterDisplayName(r.clusterId)}
                  </td>
                  <td class="px-3 py-2 align-top text-text-secondary">
                    {r.documentCount}
                  </td>
                  <td class="px-3 py-2 align-top text-text-secondary">
                    <time dateTime={r.createdAt} title={r.createdAt}>
                      {timeAgo(r.createdAt)}
                    </time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {data && data.total > PAGE_SIZE && (
        <nav
          aria-label="Recorded changes pages"
          class="flex items-center justify-between gap-3 text-sm"
        >
          <PagerButton
            label="Previous"
            inactive={busy || current <= 1}
            onActivate={() => goTo(current - 1)}
          />
          <span role="status" class="text-text-muted">
            {busy
              ? `Loading page ${requested}…`
              : failed
                ? `Could not load page ${requested} of ${pages}.`
                : `Page ${current} of ${pages}`}
          </span>
          <PagerButton
            label="Next"
            inactive={busy || current >= pages}
            onActivate={() => goTo(current + 1)}
          />
        </nav>
      )}
    </div>
  );
}
