import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://localhost/" });

// IS_BROWSER is fixed when is-browser.ts first loads, and bun shares that
// module across test files, so each test sets it explicitly. The default
// (false, as on the server) is put back afterwards.
const setBrowser = (value: boolean) =>
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: value }));

const { cleanup, render } = await import("@testing-library/preact");
const { renderToString } = await import("react-dom/server");
const { useEffect, useRef } = await import("react");
const { useIsClient } = await import("./use-is-client.ts");

afterEach(() => cleanup());
afterAll(() => {
  setBrowser(false);
  GlobalRegistrator.unregister();
});

function Gated() {
  const isClient = useIsClient();
  if (!isClient) return <p>placeholder</p>;
  return <p>client</p>;
}

test("server rendering emits the placeholder, not the client branch", () => {
  setBrowser(false);
  expect(renderToString(<Gated />)).toBe("<p>placeholder</p>");
});

test("useIsClient is true once mounted in the browser", () => {
  setBrowser(true);
  const r = render(<Gated />);
  expect(r.container.textContent).toBe("client");
});

test("an effect that lists isClient mounts into a client-only ref once", () => {
  setBrowser(true);
  let mounts = 0;
  function Editor() {
    const isClient = useIsClient();
    const host = useRef<HTMLDivElement>(null);
    useEffect(() => {
      if (!host.current) return;
      mounts++;
    }, [isClient]);
    if (!isClient) return <p>loading</p>;
    return <div ref={host} />;
  }
  render(<Editor />);
  expect(mounts).toBe(1);
});
