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
 * `getSnapshot`, so server and client markup agree. preact/compat ignores
 * `getServerSnapshot`, which is why `getSnapshot` itself returns IS_BROWSER
 * rather than a constant `true`: it is false on the server either way.
 *
 * An effect that mounts a library into a ref rendered only in the client
 * branch must list `isClient` in its dependencies (or use a ref callback).
 * Otherwise, under React, it runs once during hydration while the ref is
 * still null and never runs again.
 */
export function useIsClient(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
