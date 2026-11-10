/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { type Signal, signal } from "@preact/signals-core";
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

function MountingChart({ mounted }: { mounted: HTMLElement[] }) {
  const isClient = useIsClient();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (isClient && ref.current) mounted.push(ref.current);
  }, [isClient, mounted]);
  if (!isClient) return <div>placeholder</div>;
  return <div ref={ref}>chart</div>;
}

function GatedPicker({ namespace }: { namespace: Signal<string> }) {
  const isClient = useIsClient();
  const value = useSignalValue(namespace);
  if (!isClient) return <span>placeholder</span>;
  return <span>{value}</span>;
}

function UngatedPicker({ namespace }: { namespace: Signal<string> }) {
  return <span>{useSignalValue(namespace)}</span>;
}

function GradientChart() {
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
  // Hydrate the server markup, as Astro does. Under React the effect first
  // runs while the placeholder is still in place; listing isClient makes it
  // run again once the client branch exists.
  setBrowser(false);
  const container = document.createElement("div");
  container.innerHTML = renderToString(<MountingChart mounted={mounted} />);
  document.body.appendChild(container);
  setBrowser(true);
  let root: ReturnType<typeof hydrateRoot> | undefined;
  await act(() => {
    root = hydrateRoot(container, <MountingChart mounted={mounted} />);
  });
  await act(() => {
    root?.render(<MountingChart mounted={mounted} />);
  });

  expect(mounted).toHaveLength(1);
  expect(mounted[0].textContent).toBe("chart");
  await act(() => root?.unmount());
  container.remove();
});

test("markup from a store the browser restored hydrates without an error behind useIsClient", async () => {
  const namespace = signal("all");
  const { html, container, errors, done } = await hydrate(
    () => <GatedPicker namespace={namespace} />,
    {
      restore: () => {
        namespace.value = "team-a";
      },
    },
  );
  expect(html).toBe("<span>placeholder</span>");
  expect(errors).toEqual([]);
  expect(container.textContent).toBe("team-a");
  done();
});

test("the same markup read straight from the store is a hydration error, which is what the gate prevents", async () => {
  const namespace = signal("all");
  const { errors, done } = await hydrate(
    () => <UngatedPicker namespace={namespace} />,
    {
      restore: () => {
        namespace.value = "team-a";
      },
    },
  );
  expect(errors.join(" ")).toMatch(/hydrat/i);
  done();
});

test("a chart gradient id from useId is the same on the server and after hydration", async () => {
  // Astro hands each island its own prefix, on both sides.
  const { html, container, errors, done } = await hydrate(
    () => <GradientChart />,
    {
      identifierPrefix: "r4",
    },
  );
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
