import {
  computed,
  effect,
  type ReadonlySignal,
  type Signal,
  signal,
} from "@preact/signals-core";
import { useEffect, useState } from "react";
import { useLatest } from "./use-latest.ts";

/**
 * React versions of @preact/signals' component hooks, for a shared hook or
 * component whose interface still hands signals to islands on Preact APIs.
 * Each behaves as its @preact/signals namesake. Component-local state that
 * never leaves the component uses useState instead; once an interface switches
 * to plain values these go with it.
 *
 * Reading one of these signals in a React-API component's render goes through
 * useSignalValue, which subscribes; a bare `.value` read in render does not.
 */

/** A signal created on first render and kept for the component's lifetime. */
export function useSignal<T>(initial: T): Signal<T> {
  return useState(() => signal(initial))[0];
}

/**
 * A computed signal kept for the component's lifetime. It recomputes when a
 * signal it reads changes, using the latest `compute` passed in.
 */
export function useComputed<T>(compute: () => T): ReadonlySignal<T> {
  const latest = useLatest(compute);
  return useState(() => computed(() => latest.current()))[0];
}

/**
 * Runs `run` after mount and again whenever a signal it read changes; the
 * cleanup it returns runs before each re-run and on unmount.
 */
export function useSignalEffect(run: () => void | (() => void)): void {
  const latest = useLatest(run);
  useEffect(() => effect(() => latest.current()), []);
}
