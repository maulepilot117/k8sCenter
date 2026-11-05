/** @jsxImportSource preact */
import { afterAll, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { hydrate, render } from "preact";
import { act } from "preact/test-utils";
import { renderToString } from "preact-render-to-string";

/**
 * The server render and the first browser render of ResourceDetail must have
 * the same element structure.
 *
 * Preact's hydration adopts the server's elements positionally and does not
 * re-apply their props. When the metrics rail was gated on IS_BROWSER, the
 * server emitted DetailShell's single-column body while the browser rendered
 * the two-column grid, so every element below it was adopted one level off:
 * the grid kept the content card's styles, and the loaded tab ended up inside
 * the loading spinner's centred flex box. The YAML editor in that box sized
 * itself to whichever lines CodeMirror had rendered, so its width changed as
 * the page scrolled.
 */

const env = { browser: false };
const mockIsBrowser = () =>
  mock.module("../lib/is-browser.ts", () => ({ IS_BROWSER: env.browser }));
mockIsBrowser();

// The real rail is SSR-safe in the same way: nothing on the server, a card in
// the browser.
mock.module("./MetricsRail.tsx", () => ({
  default: () => (env.browser ? <aside data-testid="rail" /> : null),
}));
mock.module("../lib/ws.ts", () => ({
  EVENT_DELETED: "DELETED",
  EVENT_MODIFIED: "MODIFIED",
  EVENT_RESYNC: "RESYNC",
  subscribe: () => () => {},
}));

const { default: ResourceDetail } = await import("./ResourceDetail.tsx");

afterAll(() => {
  if (GlobalRegistrator.isRegistered) GlobalRegistrator.unregister();
});

test("hydration keeps the rail grid and the content card on their own elements", () => {
  const props = {
    kind: "deployments",
    title: "Deployment",
    namespace: "default",
    name: "web",
  };

  const ssr = renderToString(<ResourceDetail {...props} />);

  GlobalRegistrator.register({
    url: "http://localhost/workloads/deployments/default/web",
  });
  env.browser = true;
  mockIsBrowser();
  // Keep the page in its loading state: nothing the effects ask for answers.
  globalThis.fetch = (() => new Promise(() => {})) as unknown as typeof fetch;

  const host = document.createElement("div");
  host.innerHTML = ssr;
  document.body.appendChild(host);
  act(() => hydrate(<ResourceDetail {...props} />, host));

  try {
    const grid = [...host.querySelectorAll("div")].find((d) =>
      (d.getAttribute("style") ?? "").includes("grid-template-columns"),
    );
    expect(grid).toBeDefined();

    // The content card sits in the grid's first column, not in the grid's
    // own element.
    const card = [...host.querySelectorAll("div")].find((d) =>
      (d.getAttribute("style") ?? "").includes("var(--bg-surface)"),
    );
    expect(card).toBeDefined();
    expect(card).not.toBe(grid);
    expect(card?.parentElement?.parentElement).toBe(grid ?? null);
    expect(grid?.querySelector("[data-testid=rail]")).not.toBeNull();
  } finally {
    act(() => render(null, host));
    host.remove();
  }
});
