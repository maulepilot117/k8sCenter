/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { signal } from "@preact/signals-core";
import { useEffect, useId, useRef } from "react";
import { hydrate } from "@/src/test-utils/hydrate.tsx";
import { act, render, setBrowser, setupDom } from "@/src/test-utils/render.tsx";

setupDom();

const { hydrateRoot } = await import("react-dom/client");
const { renderToString } = await import("react-dom/server");
const { useIsClient } = await import("./use-is-client.ts");
const { useSignalValue } = await import("./use-signal-value.ts");

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

test("markup from a store the browser restored hydrates without an error behind useIsClient", async () => {
  const namespace = signal("all");
  function Picker() {
    const isClient = useIsClient();
    const value = useSignalValue(namespace);
    if (!isClient) return <span>placeholder</span>;
    return <span>{value}</span>;
  }
  const { html, container, errors, done } = await hydrate(() => <Picker />, {
    restore: () => {
      namespace.value = "team-a";
    },
  });
  expect(html).toBe("<span>placeholder</span>");
  expect(errors).toEqual([]);
  expect(container.textContent).toBe("team-a");
  done();
});

test("the same markup read straight from the store is a hydration error, which is what the gate prevents", async () => {
  const namespace = signal("all");
  function Picker() {
    return <span>{useSignalValue(namespace)}</span>;
  }
  const { errors, done } = await hydrate(() => <Picker />, {
    restore: () => {
      namespace.value = "team-a";
    },
  });
  expect(errors.join(" ")).toMatch(/hydrat/i);
  done();
});

test("a chart gradient id from useId is the same on the server and after hydration", async () => {
  function Chart() {
    const id = useId();
    return (
      <svg>
        <defs>
          <linearGradient id={id} />
        </defs>
        <rect fill={`url(#${id})`} />
      </svg>
    );
  }
  // Astro hands each island its own prefix, on both sides.
  const { html, container, errors, done } = await hydrate(() => <Chart />, {
    identifierPrefix: "r4",
  });
  const serverId = /<linearGradient id="([^"]+)"/.exec(html)?.[1];
  const gradient = container.querySelector("linearGradient");
  expect(errors).toEqual([]);
  expect(serverId).toContain("r4");
  expect(gradient?.id).toBe(serverId);
  expect(container.querySelector("rect")?.getAttribute("fill")).toBe(
    `url(#${serverId})`,
  );
  done();
});
