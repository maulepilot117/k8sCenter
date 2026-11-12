import { expect, test } from "bun:test";
import { type Signal, signal } from "@/src/lib/signals.ts";
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

function CountingReader({
  count,
  onRender,
}: {
  count: Signal<number>;
  onRender: () => void;
}) {
  onRender();
  return <span data-testid="v">{useSignalValue(count)}</span>;
}

function ClusterTarget({ seen }: { seen: string[] }) {
  const id = useSignalValue(selectedCluster);
  const generation = useSignalValue(selectedClusterGeneration);
  const epoch = useSignalValue(clusterEpoch);
  seen.push(`${id}/${generation}/${epoch}`);
  return null;
}

function NameLabel({ name }: { name: Signal<string> }) {
  return <b>{useSignalValue(name)}</b>;
}

function CountingItalicReader({
  count,
  onRender,
}: {
  count: Signal<number>;
  onRender: () => void;
}) {
  onRender();
  return <i>{useSignalValue(count)}</i>;
}

function TwoSignalReader({
  other,
  count,
}: {
  other: Signal<string>;
  count: Signal<number>;
}) {
  useSignalValue(other);
  return <i>{useSignalValue(count)}</i>;
}

test("a signal write re-renders a reader exactly once", async () => {
  const count = signal(1);
  let renders = 0;
  const r = render(
    <CountingReader
      count={count}
      onRender={() => {
        renders++;
      }}
    />,
  );
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
  render(<ClusterTarget seen={seen} />);
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
  expect(renderToString(<NameLabel name={name} />)).toBe("<b>remote-1</b>");
});

test("unmounting closes the subscription, and later writes do not render", async () => {
  const count = signal(0);
  const subs = countSubscriptions(count);
  let renders = 0;
  const r = render(
    <CountingItalicReader
      count={count}
      onRender={() => {
        renders++;
      }}
    />,
  );
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
  render(<TwoSignalReader other={other} count={count} />);
  await act(() => {
    other.value = "b";
  });
  await act(() => {
    other.value = "c";
  });
  expect(subs.opened()).toBe(1);
});
