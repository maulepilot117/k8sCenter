/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
// Loads preact/compat, as ToastProvider does on every ChromeLayout page, so
// `onChange` on a text input is a per-keystroke listener.
import "react";
import { dashboardData, type SourceState } from "@/lib/dashboard/data.ts";
import type { WidgetDef } from "@/lib/dashboard/types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { default: WidgetHost } = await import("./WidgetHost.tsx");
const { default: WidgetPalette } = await import("./WidgetPalette.tsx");
const { registerWidget } = await import("@/lib/dashboard/registry.ts");

const SEEDED = new Set<string>();

function seed(key: string, over: Partial<SourceState>): void {
  SEEDED.add(key);
  dashboardData.signalFor(key).value = {
    data: null,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
    ...over,
  };
}

afterEach(() => {
  for (const key of SEEDED) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
  SEEDED.clear();
});

/** A host-only definition: it is passed to WidgetHost directly, not registered. */
function hostDef(over: Partial<WidgetDef> = {}): WidgetDef {
  return {
    id: "host-test-widget",
    title: "Host Test",
    family: "cluster",
    scopes: ["overview"],
    sources: ["recent-events"],
    modes: ["normal"],
    minW: 2,
    minH: 2,
    defaultW: 3,
    defaultH: 3,
    render: () => <p data-testid="host-body">body shown</p>,
    ...over,
  };
}

const state = (host: HTMLElement) => host.getAttribute("data-widget-state");
const hostEl = () => body().getByTestId("widget-host");

test("a required source still loading shows the skeleton, not the body", () => {
  seed("recent-events", { loading: true });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("loading");
  expect(body().queryByTestId("host-body")).toBeNull();
});

test("a required source with data shows the widget body", () => {
  seed("recent-events", { data: [] });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("ready");
  expect(body().getByTestId("host-body").textContent).toBe("body shown");
});

test("a failed source shows the error card naming the widget and the message", () => {
  seed("recent-events", { error: "boom 500", errorKind: "failure" });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("error");
  const card = body().getByTestId("widget-error");
  expect(card.textContent).toContain("Host Test could not be loaded.");
  expect(card.textContent).toContain("boom 500");
  expect(body().queryByTestId("host-body")).toBeNull();
});

test("a forbidden source shows the permission card with no message", () => {
  seed("recent-events", { error: "Forbidden", errorKind: "permission" });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("permission");
  expect(body().getByTestId("widget-permission")).toBeTruthy();
  expect(body().queryByText("Forbidden")).toBeNull();
});

test("a source the deployment does not serve shows the unavailable card", () => {
  seed("recent-events", { error: "503", errorKind: "absent" });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("unavailable");
  expect(body().getByTestId("widget-unavailable")).toBeTruthy();
});

test("an unanswerable source shows the unsupported card with the backend message", () => {
  seed("recent-events", {
    error: "pick one mesh",
    errorKind: "unsupported",
  });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("unsupported");
  expect(body().getByTestId("widget-unsupported").textContent).toContain(
    "pick one mesh",
  );
});

test("a family that reports its feature absent shows unavailable without rendering the body", () => {
  seed("recent-events", { data: [] });
  seed("certificates-status", { data: { detected: false } });
  render(<WidgetHost def={hostDef({ familyStatus: "certificates-status" })} />);
  expect(state(hostEl())).toBe("unavailable");
  expect(body().queryByTestId("host-body")).toBeNull();
});

test("failed refresh over held data keeps the body and adds the stale notice", () => {
  seed("recent-events", { data: [], error: "refresh failed" });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("ready");
  expect(body().getByTestId("widget-stale")).toBeTruthy();
  expect(body().getByTestId("host-body")).toBeTruthy();
});

test("a parameterised source is read under the key the params build", () => {
  const def = hostDef({
    sources: ["diagnostics-summary"],
    params: { namespace: [] },
  });
  seed("diagnostics-summary", { data: { wrong: true } });
  const { unmount } = render(
    <WidgetHost def={def} params={{ namespace: "team-a" }} />,
  );
  // The bare key has data but the key carrying the namespace never fetched.
  expect(state(hostEl())).toBe("loading");
  unmount();
});

test("loading turns into the body when the source resolves after mount", () => {
  seed("recent-events", { loading: true });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("loading");
  act(() => {
    seed("recent-events", { data: [] });
  });
  expect(state(hostEl())).toBe("ready");
  expect(body().getByTestId("host-body")).toBeTruthy();
});

test("a ready body turns into the error card when the source is replaced by a failure with no data", () => {
  seed("recent-events", { data: [] });
  render(<WidgetHost def={hostDef()} />);
  expect(state(hostEl())).toBe("ready");
  act(() => {
    seed("recent-events", { data: null, error: "gone", errorKind: "failure" });
  });
  expect(state(hostEl())).toBe("error");
});

test("a coverage row for a headline widget withholds the body once the summary lands", () => {
  const def = hostDef({ id: "pods-tile", sources: ["recent-events"] });
  seed("recent-events", { data: [] });
  seed("dashboard-summary", {
    data: {
      coverage: [
        {
          section: "pods",
          status: "unavailable",
          reasonCode: "unreachable",
          detail: "cannot list pods",
        },
      ],
    },
  });
  render(<WidgetHost def={def} />);
  expect(state(hostEl())).toBe("coverage-unavailable");
  expect(body().getByTestId("widget-coverage-unavailable")).toBeTruthy();
  expect(body().queryByTestId("host-body")).toBeNull();
});

test("the coverage verdict follows the summary when it changes after mount", () => {
  const def = hostDef({ id: "pods-tile", sources: ["recent-events"] });
  seed("recent-events", { data: [] });
  seed("dashboard-summary", { data: {} });
  render(<WidgetHost def={def} />);
  expect(state(hostEl())).toBe("ready");
  act(() => {
    seed("dashboard-summary", {
      data: {
        coverage: [
          { section: "pods", status: "forbidden", reasonCode: "forbidden" },
        ],
      },
    });
  });
  expect(state(hostEl())).toBe("coverage-forbidden");
  expect(body().getByTestId("widget-coverage-forbidden")).toBeTruthy();
});

// Palette ---------------------------------------------------------------

for (const title of ["Qzx One", "Qzx Two", "Qzx Three"]) {
  registerWidget({
    // `fixture-` ids and a valid size: the registry is shared with later test
    // files, which skip `fixture-` ids and check every widget's minimum size.
    id: `fixture-palette-${title.toLowerCase().replace(/ /g, "-")}`,
    title,
    family: "cluster",
    scopes: ["overview"],
    sources: [],
    modes: ["normal"],
    minW: 2,
    minH: 2,
    defaultW: 2,
    defaultH: 2,
    render: () => <span />,
  });
}

function renderPalette() {
  const added: string[] = [];
  let closed = 0;
  render(
    <WidgetPalette
      scope="overview"
      placed={[]}
      columns={12}
      familyStatuses={{}}
      viewerIsAdmin={null}
      onAdd={(def) => added.push(def.id)}
      onClose={() => {
        closed++;
      }}
    />,
  );
  return { added, closed: () => closed };
}

const search = () => body().getByTestId("widget-search") as HTMLInputElement;
const options = () =>
  Array.from(
    document.querySelectorAll<HTMLElement>('[role="option"]'),
  ) as HTMLElement[];
const selectedId = () =>
  options()
    .find((o) => o.getAttribute("aria-selected") === "true")
    ?.id.replace("widget-option-", "");

function type(text: string) {
  fireEvent.input(search(), { target: { value: text } });
}

test("typing filters the list to the matching widgets", async () => {
  renderPalette();
  const before = options().length;
  expect(before).toBeGreaterThanOrEqual(3);
  type("Qzx");
  await waitFor(() => expect(options().length).toBe(3));
  expect(options().every((o) => o.textContent?.includes("Qzx"))).toBe(true);
  type("Qzx Two");
  await waitFor(() => expect(options().length).toBe(1));
  expect(options()[0].textContent).toContain("Qzx Two");
  type("nothing-matches-this-at-all");
  await waitFor(() => expect(options().length).toBe(0));
  expect(body().getByText("No widget matches that search.")).toBeTruthy();
});

test("the first addable row is selected after a search, and arrows move the selection", async () => {
  renderPalette();
  type("Qzx");
  await waitFor(() => expect(options().length).toBe(3));
  const order = options().map((o) => o.id.replace("widget-option-", ""));
  expect(selectedId()).toBe(order[0]);

  fireEvent.keyDown(search(), { key: "ArrowDown" });
  expect(selectedId()).toBe(order[1]);
  fireEvent.keyDown(search(), { key: "ArrowDown" });
  expect(selectedId()).toBe(order[2]);
  // At the end it stays put.
  fireEvent.keyDown(search(), { key: "ArrowDown" });
  expect(selectedId()).toBe(order[2]);
  fireEvent.keyDown(search(), { key: "ArrowUp" });
  expect(selectedId()).toBe(order[1]);
  expect(search().getAttribute("aria-activedescendant")).toBe(
    `widget-option-${order[1]}`,
  );
});

test("Enter adds the selected widget", async () => {
  const { added } = renderPalette();
  type("Qzx");
  await waitFor(() => expect(options().length).toBe(3));
  const order = options().map((o) => o.id.replace("widget-option-", ""));
  fireEvent.keyDown(search(), { key: "ArrowDown" });
  fireEvent.keyDown(search(), { key: "Enter" });
  expect(added).toEqual([order[1]]);
});

test("a click adds that widget, and hovering moves the selection", async () => {
  const { added } = renderPalette();
  type("Qzx");
  await waitFor(() => expect(options().length).toBe(3));
  const order = options().map((o) => o.id.replace("widget-option-", ""));
  fireEvent.mouseEnter(options()[2]);
  expect(selectedId()).toBe(order[2]);
  fireEvent.click(options()[1]);
  expect(added).toEqual([order[1]]);
});

test("Escape closes the palette", () => {
  const { closed } = renderPalette();
  fireEvent.keyDown(search(), { key: "Escape" });
  expect(closed()).toBeGreaterThanOrEqual(1);
});
