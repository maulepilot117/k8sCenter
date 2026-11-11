import { afterAll, expect, test } from "bun:test";
import "react";
import type { PinRecord } from "@/lib/preferences.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: IconRail } = await import("./IconRail.tsx");
const { default: SecondaryNav } = await import("./SecondaryNav.tsx");
const { default: QuickActionsFab } = await import("./QuickActionsFab.tsx");
const { pins, pinsLoaded } = await import("@/src/lib/pin-store.ts");
const { selectedCluster } = await import("@/src/lib/cluster.ts");

function pin(name: string): PinRecord {
  return {
    id: `pin-${name}`,
    name,
    clusterId: selectedCluster.value,
    config: {
      schemaVersion: 1,
      resourceKind: "deployments",
      group: "",
      version: "",
      namespace: "shop",
      name,
      uid: `uid-${name}`,
      displayKind: "Deployment",
    },
  } as unknown as PinRecord;
}

afterAll(() => {
  globalThis.fetch = bunFetch;
});

/** The pin list endpoint answers with whatever the store holds right now. */
function stubPinsApi() {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ data: pins.value }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof fetch;
}

stubPinsApi();

test("IconRail marks the section that owns the path", () => {
  const { unmount } = render(<IconRail currentPath="/workloads/deployments" />);
  const ui = body();
  expect(
    ui.getByRole("link", { name: "Workloads" }).getAttribute("aria-current"),
  ).toBe("page");
  expect(
    ui.getByRole("link", { name: "Overview" }).getAttribute("aria-current"),
  ).toBeNull();
  expect(ui.getByRole("link", { name: "Settings" })).toBeTruthy();
  expect(ui.getByRole("link", { name: "k8sCenter home" })).toBeTruthy();
  unmount();

  render(<IconRail currentPath="/settings/general" />);
  expect(
    body().getByRole("link", { name: "Settings" }).getAttribute("aria-current"),
  ).toBe("page");
  expect(
    body()
      .getByRole("link", { name: "Workloads" })
      .getAttribute("aria-current"),
  ).toBeNull();
});

test("IconRail shows a tooltip while a section is hovered", async () => {
  render(<IconRail currentPath="/" />);
  const link = body().getByRole("link", { name: "Workloads" });
  fireEvent.mouseEnter(link.parentElement as HTMLElement);
  await waitFor(() =>
    expect(document.querySelector(".rail-tooltip")?.textContent).toBe(
      "Workloads",
    ),
  );
  fireEvent.mouseLeave(
    body().getByRole("link", { name: "Workloads" })
      .parentElement as HTMLElement,
  );
  await waitFor(() =>
    expect(document.querySelector(".rail-tooltip")).toBeNull(),
  );
});

test("SecondaryNav lists the domain's items and filters them", () => {
  stubPinsApi();
  render(<SecondaryNav currentPath="/workloads/deployments" />);
  const ui = body();
  ui.getByText("Workloads");
  const active = ui
    .getAllByRole("link")
    .filter((a: HTMLElement) => a.getAttribute("aria-current") === "page");
  expect(active.map((a: HTMLElement) => a.getAttribute("href"))).toEqual([
    "/workloads/deployments",
  ]);
  const filter = ui.getByLabelText("Filter in this section");
  fireEvent.input(filter, { target: { value: "zzz-no-such-item" } });
  expect(ui.queryAllByRole("link").length).toBe(0);
});

test("SecondaryNav shows the active cluster's pins and follows the pin store", async () => {
  pins.value = [pin("web")];
  pinsLoaded.value = true;
  stubPinsApi();
  try {
    render(<SecondaryNav currentPath="/workloads/deployments" />);
    const ui = body();
    await waitFor(() => ui.getByText("web"));
    expect(ui.queryByText("api")).toBeNull();

    await act(async () => {
      pins.value = [...pins.value, pin("api")];
    });
    await waitFor(() => ui.getByText("api"));
    ui.getByText("web");

    // A pin addressed to another cluster is not shown.
    await act(async () => {
      pins.value = [
        ...pins.value,
        { ...pin("elsewhere"), clusterId: "some-other-cluster" },
      ];
    });
    expect(ui.queryByText("elsewhere")).toBeNull();
  } finally {
    pins.value = [];
    pinsLoaded.value = false;
  }
});

test("QuickActionsFab opens, links its actions, and closes", () => {
  render(<QuickActionsFab />);
  const ui = body();
  expect(ui.queryAllByRole("link").length).toBe(0);

  fireEvent.click(ui.getByRole("button", { name: "Open quick actions" }));
  const hrefs = Object.fromEntries(
    ui
      .getAllByRole("link")
      .map((a: HTMLElement) => [a.textContent, a.getAttribute("href")]),
  );
  expect(hrefs).toEqual({
    "New Deployment": "/workloads/deployments/new",
    "New Service": "/networking/services/new",
    "Apply YAML": "/tools/yaml-apply",
    "Scale Resource": "#",
  });

  fireEvent.click(ui.getByRole("link", { name: "Apply YAML" }));
  expect(ui.queryAllByRole("link").length).toBe(0);

  fireEvent.click(ui.getByRole("button", { name: "Open quick actions" }));
  fireEvent.click(ui.getByRole("button", { name: "Close quick actions" }));
  expect(ui.queryAllByRole("link").length).toBe(0);
});

test("QuickActionsFab closes on Escape and hands focus back to its button", () => {
  render(<QuickActionsFab />);
  const ui = body();
  fireEvent.click(ui.getByRole("button", { name: "Open quick actions" }));
  ui.getByRole("link", { name: "New Service" }).focus();

  fireEvent.keyDown(document, { key: "Escape" });

  expect(ui.queryAllByRole("link").length).toBe(0);
  expect(document.activeElement).toBe(
    ui.getByRole("button", { name: "Open quick actions" }),
  );
});

test("QuickActionsFab leaves focus alone on an Escape meant for another component", () => {
  render(<QuickActionsFab />);
  const ui = body();
  const field = document.createElement("input");
  document.body.appendChild(field);
  fireEvent.click(ui.getByRole("button", { name: "Open quick actions" }));
  field.focus();

  fireEvent.keyDown(document, { key: "Escape" });

  expect(ui.queryAllByRole("link").length).toBe(0);
  expect(document.activeElement).toBe(field);
  field.remove();
});

test("QuickActionsFab closes on a press outside it, not on one inside its menu", () => {
  render(<QuickActionsFab />);
  const ui = body();
  fireEvent.click(ui.getByRole("button", { name: "Open quick actions" }));

  fireEvent.mouseDown(ui.getByRole("link", { name: "Apply YAML" }));
  expect(ui.getAllByRole("link").length).toBe(4);

  fireEvent.mouseDown(document.body);
  expect(ui.queryAllByRole("link").length).toBe(0);
});
