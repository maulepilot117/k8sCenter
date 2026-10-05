import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { ApiError } from "@/lib/api.ts";
import { listReceipts, type ReceiptPage } from "@/lib/change-api.ts";
import { receiptHref, shortId } from "@/lib/change-copy.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { ChangeStateBadges } from "@/src/components/changes/ChangeStateBadges.tsx";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";

/**
 * The caller's own change receipts, newest first (GET /v1/changes). Receipts
 * shared with the caller are reachable by link only and are not listed here,
 * which the page says.
 *
 * The root element is identical during SSR and after hydration (the loading
 * state renders on the server), so there is no placeholder root to diverge.
 */

const ROOT_CLASS = "flex flex-col gap-5";
const PAGE_SIZE = 20;

type ListState =
  | { status: "loading" }
  | { status: "ready"; page: ReceiptPage }
  | { status: "error"; message: string };

function listErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 503) {
      return "Change records are unavailable: the server has no database configured, or it cannot be reached.";
    }
    if (err.status === 404) {
      return "This server does not keep change records.";
    }
  }
  return "Could not load your recorded changes.";
}

export default function ChangeReceiptList() {
  const page = useSignal(1);
  const state = useSignal<ListState>({ status: "loading" });

  useEffect(() => {
    document.title = "Recorded changes - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  const current = page.value;
  useEffect(() => {
    const controller = new AbortController();
    state.value = { status: "loading" };
    listReceipts({ page: current, pageSize: PAGE_SIZE }, controller.signal)
      .then((res) => {
        if (!controller.signal.aborted)
          state.value = { status: "ready", page: res };
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          state.value = { status: "error", message: listErrorText(err) };
        }
      });
    return () => controller.abort();
  }, [current]);

  const s = state.value;
  const pages =
    s.status === "ready" ? Math.max(1, Math.ceil(s.page.total / PAGE_SIZE)) : 1;

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

      {s.status === "loading" && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading recorded changes…
        </p>
      )}

      {s.status === "error" && (
        <div
          role="alert"
          class="rounded-md border border-danger bg-danger-dim px-4 py-3 text-sm text-danger"
        >
          {s.message}
        </div>
      )}

      {s.status === "ready" && s.page.total === 0 && (
        <p class="m-0 text-sm text-text-secondary">
          No recorded changes yet.{" "}
          <a href="/tools/yaml-apply" class="font-medium text-accent">
            Apply YAML
          </a>{" "}
          with “Keep a record of this change” on to create one.
        </p>
      )}

      {s.status === "ready" && s.page.items.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-subtle bg-surface">
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
              {s.page.items.map((r) => (
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
                    {r.clusterId === LOCAL_CLUSTER_ID
                      ? "Local cluster"
                      : r.clusterId}
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

      {s.status === "ready" && s.page.total > PAGE_SIZE && (
        <nav
          aria-label="Recorded changes pages"
          class="flex items-center justify-between gap-3 text-sm"
        >
          <button
            type="button"
            disabled={current <= 1}
            onClick={() => {
              page.value = current - 1;
            }}
            class="cursor-pointer rounded-md border border-border-primary bg-transparent px-3 py-1.5 font-medium text-text-secondary disabled:cursor-not-allowed disabled:opacity-50"
          >
            Previous
          </button>
          <span class="text-text-muted">
            Page {current} of {pages}
          </span>
          <button
            type="button"
            disabled={current >= pages}
            onClick={() => {
              page.value = current + 1;
            }}
            class="cursor-pointer rounded-md border border-border-primary bg-transparent px-3 py-1.5 font-medium text-text-secondary disabled:cursor-not-allowed disabled:opacity-50"
          >
            Next
          </button>
        </nav>
      )}
    </div>
  );
}
