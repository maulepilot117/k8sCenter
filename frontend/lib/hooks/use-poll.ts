import type { Signal } from "@preact/signals";
import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { ApiError, apiGet } from "@/lib/api.ts";
import { failureOf } from "@/lib/networking-state.ts";

// Inlined rather than imported from "fresh/runtime" — see lib/nav.ts for why:
// this file is shared by both the Fresh tree and the Astro/Bun port, and the
// bare specifier would otherwise resolve to the always-false test shim under
// Astro. Value-neutral for Fresh: identical to @fresh/core's own IS_BROWSER.
const IS_BROWSER = typeof document !== "undefined";

interface UsePollOptions<T> {
  /** Polling interval in milliseconds. */
  interval: number;
  /** Return false to stop polling (e.g., when configured: false). */
  shouldContinuePolling?: (data: T) => boolean;
  /** Set to false to disable polling entirely. */
  enabled?: boolean;
}

/** The HTTP status and endpoint reason of the last failed fetch. */
export interface PollFailure {
  status: number;
  reason?: string;
}

interface UsePollResult<T> {
  data: Signal<T | null>;
  loading: Signal<boolean>;
  error: Signal<string | null>;
  /**
   * Status and reason of the last failed fetch, or null after a success or
   * for a failure with no HTTP response. Islands map it to a state with
   * lib/networking-state.ts rather than parsing the message.
   */
  failure: Signal<PollFailure | null>;
  /** Trigger an immediate refetch outside the polling cycle. */
  refetch: () => Promise<void>;
  /** Timestamp of the last successful fetch. */
  lastFetchedAt: Signal<Date | null>;
}

const MAX_CONSECUTIVE_FAILURES = 3;

/**
 * Polls an API endpoint at a fixed interval with error backoff.
 * Skips ticks when the tab is hidden. Pauses after 3 consecutive failures
 * and resumes on visibility change. Stops polling if shouldContinuePolling
 * returns false, and for good after a 403: a refused read stays refused, and
 * polling it again only repeats the refusal (#608 R5).
 */
export function usePoll<T>(
  url: string,
  opts: UsePollOptions<T>,
): UsePollResult<T> {
  const data = useSignal<T | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const failure = useSignal<PollFailure | null>(null);
  const lastFetchedAt = useSignal<Date | null>(null);
  const intervalRef = useRef<number | null>(null);
  const failureCount = useRef(0);
  const stoppedRef = useRef(false);
  const doFetchRef = useRef<(() => Promise<void>) | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    if (opts.enabled === false) return;

    // A new url starts from a clean slate: a refusal or failures recorded
    // for the previous one say nothing about this one.
    stoppedRef.current = false;
    failureCount.current = 0;
    let active = true;

    const startPolling = () => {
      intervalRef.current = setInterval(() => {
        if (document.hidden) return;
        if (stoppedRef.current) return;
        doFetch();
      }, opts.interval) as unknown as number;
    };

    const doFetch = async () => {
      try {
        const resp = await apiGet<T>(url);
        data.value = resp.data;
        error.value = null;
        failure.value = null;
        failureCount.current = 0;
        lastFetchedAt.value = new Date();

        // Check if polling should continue
        if (
          opts.shouldContinuePolling &&
          !opts.shouldContinuePolling(resp.data)
        ) {
          stoppedRef.current = true;
          if (intervalRef.current !== null) {
            clearInterval(intervalRef.current);
            intervalRef.current = null;
          }
        }

        // A manual refetch can succeed while polling is paused after
        // failures; resume it rather than leave the panel frozen.
        if (active && !stoppedRef.current && intervalRef.current === null) {
          startPolling();
        }
      } catch (err) {
        error.value =
          err instanceof Error ? err.message : "Failed to fetch data";
        failure.value = failureOf(err);
        failureCount.current++;

        // A 403 is final: stop, and do not resume on visibility change.
        if (err instanceof ApiError && err.status === 403) {
          stoppedRef.current = true;
        }

        // Pause polling after consecutive failures
        if (
          stoppedRef.current ||
          failureCount.current >= MAX_CONSECUTIVE_FAILURES
        ) {
          if (intervalRef.current !== null) {
            clearInterval(intervalRef.current);
            intervalRef.current = null;
          }
        }
      } finally {
        loading.value = false;
      }
    };

    doFetchRef.current = doFetch;

    // Initial fetch
    doFetch();

    // Start polling
    startPolling();

    // Resume polling on visibility change after failure pause
    const onVisibilityChange = () => {
      if (
        !document.hidden &&
        intervalRef.current === null &&
        !stoppedRef.current
      ) {
        failureCount.current = 0;
        doFetch();
        startPolling();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      active = false;
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [url, opts.interval, opts.enabled]);

  const refetch = async () => {
    if (doFetchRef.current) await doFetchRef.current();
  };

  return { data, loading, error, failure, refetch, lastFetchedAt };
}
