/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { useEffect, useRef } from "react";
import { act, render, setBrowser, setupDom } from "@/src/test-utils/render.tsx";

setupDom();

const { hydrateRoot } = await import("react-dom/client");
const { renderToString } = await import("react-dom/server");
const { useIsClient } = await import("./use-is-client.ts");

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

test("an effect that lists isClient mounts into the client-only ref exactly once", async () => {
  const mounted: HTMLElement[] = [];
  function Chart() {
    const isClient = useIsClient();
    const ref = useRef<HTMLDivElement>(null);
    useEffect(() => {
      if (isClient && ref.current) mounted.push(ref.current);
    }, [isClient]);
    if (!isClient) return <div>placeholder</div>;
    return <div ref={ref}>chart</div>;
  }

  // Hydrate the server markup, as Astro does. Under React the effect first
  // runs while the placeholder is still in place; listing isClient makes it
  // run again once the client branch exists.
  setBrowser(false);
  const container = document.createElement("div");
  container.innerHTML = renderToString(<Chart />);
  document.body.appendChild(container);
  setBrowser(true);
  let root: ReturnType<typeof hydrateRoot> | undefined;
  await act(() => {
    root = hydrateRoot(container, <Chart />);
  });
  await act(() => {
    root?.render(<Chart />);
  });

  expect(mounted).toHaveLength(1);
  expect(mounted[0].textContent).toBe("chart");
  await act(() => root?.unmount());
  container.remove();
});
