import { useRef } from "react";

/**
 * A ref that always holds the value from the latest render, for code that
 * runs later than the render that set it up: a handler registered once, a
 * timer, a socket callback, or the rest of an async function after an await.
 * Reading `.current` there sees the current value, where a captured variable
 * would see the one from the render that created the closure.
 */
export function useLatest<T>(value: T): { current: T } {
  const ref = useRef(value);
  ref.current = value;
  return ref;
}
