import { useSignal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";

interface UsePollOptions<T> {
  /** Polling interval in milliseconds. */
  interval: number;
  /** Return false to stop polling (e.g., when configured: false). */
  shouldContinuePolling?: (data: T) => boolean;
  /** Set to false to disable polling entirely. */
  enabled?: boolean;
}

interface UsePollResult<T> {
  data: Signal<T | null>;
  loading: Signal<boolean>;
  error: Signal<string | null>;
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
 * returns false.
 */
export function usePoll<T>(
  url: string,
  opts: UsePollOptions<T>,
): UsePollResult<T> {
  const data = useSignal<T | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const lastFetchedAt = useSignal<Date | null>(null);
  const intervalRef = useRef<number | null>(null);
  const failureCount = useRef(0);
  const stoppedRef = useRef(false);
  const doFetchRef = useRef<(() => Promise<void>) | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    if (opts.enabled === false) return;

    const doFetch = async () => {
      try {
        const resp = await apiGet<T>(url);
        data.value = resp.data;
        error.value = null;
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
      } catch (err) {
        error.value = err instanceof Error
          ? err.message
          : "Failed to fetch data";
        failureCount.current++;

        // Pause polling after consecutive failures
        if (failureCount.current >= MAX_CONSECUTIVE_FAILURES) {
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
    intervalRef.current = setInterval(() => {
      if (document.hidden) return;
      if (stoppedRef.current) return;
      doFetch();
    }, opts.interval) as unknown as number;

    // Resume polling on visibility change after failure pause
    const onVisibilityChange = () => {
      if (
        !document.hidden && intervalRef.current === null &&
        !stoppedRef.current
      ) {
        failureCount.current = 0;
        doFetch();
        intervalRef.current = setInterval(() => {
          if (document.hidden) return;
          if (stoppedRef.current) return;
          doFetch();
        }, opts.interval) as unknown as number;
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
      }
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [url, opts.interval, opts.enabled]);

  const refetch = async () => {
    if (doFetchRef.current) await doFetchRef.current();
  };

  return { data, loading, error, refetch, lastFetchedAt };
}
