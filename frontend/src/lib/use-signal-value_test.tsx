/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { type Signal, signal } from "@preact/signals-core";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom();

const { renderToString } = await import("react-dom/server");
const { useSignalValue } = await import("./use-signal-value.ts");
const {
  clusterEpoch,
  selectedCluster,
  selectedClusterGeneration,
  switchCluster,
} = await import("./cluster.ts");

/** Wraps `s.subscribe` to count subscriptions opened, and still open. */
function countSubscriptions<T>(s: Signal<T>): {
  open: () => number;
  opened: () => number;
} {
  let open = 0;
  let opened = 0;
  const subscribe = s.subscribe.bind(s);
  s.subscribe = (fn) => {
    open++;
    opened++;
    const unsubscribe = subscribe(fn);
    return () => {
      open--;
      unsubscribe();
    };
  };
  return { open: () => open, opened: () => opened };
}

test("a signal write re-renders a reader exactly once", async () => {
  const count = signal(1);
  let renders = 0;
  function Reader() {
    renders++;
    return <span data-testid="v">{useSignalValue(count)}</span>;
  }
  const r = render(<Reader />);
  expect(r.getByTestId("v").textContent).toBe("1");
  const before = renders;

  await act(() => {
    count.value = 2;
  });

  expect(r.getByTestId("v").textContent).toBe("2");
  expect(renders - before).toBe(1);
});

test("a cluster switch never renders a mix of old and new values", async () => {
  switchCluster("cluster-a", "gen-a");
  const seen: string[] = [];
  function Target() {
    const id = useSignalValue(selectedCluster);
    const generation = useSignalValue(selectedClusterGeneration);
    const epoch = useSignalValue(clusterEpoch);
    seen.push(`${id}/${generation}/${epoch}`);
    return null;
  }
  render(<Target />);
  const startEpoch = clusterEpoch.value;

  await act(() => {
    switchCluster("cluster-b", "gen-b");
  });

  expect(seen.at(-1)).toBe(`cluster-b/gen-b/${startEpoch + 1}`);
  for (const entry of seen) {
    const [id, generation] = entry.split("/");
    expect(generation).toBe(id === "cluster-a" ? "gen-a" : "gen-b");
  }
});

test("server rendering returns the signal's current value", () => {
  const name = signal("remote-1");
  function Label() {
    return <b>{useSignalValue(name)}</b>;
  }
  expect(renderToString(<Label />)).toBe("<b>remote-1</b>");
});

test("unmounting closes the subscription, and later writes do not render", async () => {
  const count = signal(0);
  const subs = countSubscriptions(count);
  let renders = 0;
  function Reader() {
    renders++;
    return <i>{useSignalValue(count)}</i>;
  }
  const r = render(<Reader />);
  expect(subs.open()).toBe(1);

  // Effect cleanups run after paint, so flush them before checking.
  await act(() => {
    r.unmount();
  });
  expect(subs.open()).toBe(0);
  const after = renders;
  await act(() => {
    count.value = 5;
  });
  expect(renders).toBe(after);
});

test("re-rendering for another reason keeps the same subscription", async () => {
  const count = signal(0);
  const subs = countSubscriptions(count);
  const other = signal("a");
  function Reader() {
    useSignalValue(other);
    return <i>{useSignalValue(count)}</i>;
  }
  render(<Reader />);
  await act(() => {
    other.value = "b";
  });
  await act(() => {
    other.value = "c";
  });
  expect(subs.opened()).toBe(1);
});
