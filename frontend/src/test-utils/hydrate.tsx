import type * as React from "react";
import { act, setBrowser } from "@/src/test-utils/render.tsx";

/**
 * Server-renders `node` with IS_BROWSER false and hydrates the HTML with it
 * true, as Astro does, calling `restore` in between the way the browser
 * restores stores from storage before an island hydrates. Returns the server
 * HTML, the container and every error React reported while hydrating: a
 * recoverable error is how React says it discarded the server HTML.
 *
 * Call `setupDom()` first, as for `render()`.
 */
export async function hydrate(
  node: () => React.ReactElement,
  {
    identifierPrefix,
    restore = () => {},
  }: { identifierPrefix?: string; restore?: () => void } = {},
) {
  const { hydrateRoot } = await import("react-dom/client");
  const { renderToString } = await import("react-dom/server");
  setBrowser(false);
  const html = renderToString(node(), { identifierPrefix });
  const container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  restore();
  setBrowser(true);
  const errors: string[] = [];
  const consoleError = console.error;
  console.error = (...args: unknown[]) =>
    errors.push(args.map(String).join(" "));
  let root: ReturnType<typeof hydrateRoot> | undefined;
  try {
    await act(() => {
      root = hydrateRoot(container, node(), {
        identifierPrefix,
        onRecoverableError: (error) => errors.push(String(error)),
      });
    });
  } finally {
    console.error = consoleError;
  }
  return {
    html,
    container,
    errors,
    done: () => {
      act(() => root?.unmount());
      container.remove();
    },
  };
}
