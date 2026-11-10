import { useSyncExternalStore } from "react";
import { IS_BROWSER } from "./is-browser.ts";

const subscribe = () => () => {};
const getSnapshot = () => IS_BROWSER;
const getServerSnapshot = () => false;

/**
 * False on the server and during hydration, true afterwards. Gate any markup
 * that only the browser can render (it reads storage, the DOM or a browser
 * library) behind it: `if (!isClient) return <placeholder />`. The server
 * HTML stays in place until the client branch replaces it, which
 * `client:only` would not give.
 *
 * React uses `getServerSnapshot` while hydrating and then re-renders with
 * `getSnapshot`, so server and client markup agree. A render that is not a
 * hydration (`createRoot`, as the tests use) reads only `getSnapshot`, which
 * returns IS_BROWSER rather than `true` so a test that leaves IS_BROWSER false
 * renders the server branch.
 *
 * An effect that mounts a library into a ref rendered only in the client
 * branch must list `isClient` in its dependencies (or use a ref callback).
 * Otherwise, under React, it runs once during hydration while the ref is
 * still null and never runs again.
 */
export function useIsClient(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
