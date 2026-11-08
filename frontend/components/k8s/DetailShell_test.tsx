/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import { useState } from "react";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { default: DetailShell } = await import(
  "@/components/k8s/DetailShell.tsx"
);

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "yaml", label: "YAML" },
];

function Harness() {
  const [active, setActive] = useState("overview");
  return (
    <DetailShell
      title="web-1"
      subtitle="Deployment"
      status={{ label: "Healthy", tone: "ok" }}
      actions={<button type="button">Scale</button>}
      tabs={TABS}
      active={active}
      onTab={setActive}
    >
      {active === "overview" ? <p>overview body</p> : <p>yaml body</p>}
    </DetailShell>
  );
}

test("clicking a tab shows its content and marks it selected", () => {
  render(<Harness />);
  const q = body();
  expect(q.getByText("overview body")).toBeTruthy();
  expect(
    q.getByRole("tab", { name: "Overview" }).getAttribute("aria-selected"),
  ).toBe("true");
  expect(
    q.getByRole("tab", { name: "YAML" }).getAttribute("aria-selected"),
  ).toBe("false");

  fireEvent.click(q.getByRole("tab", { name: "YAML" }));

  expect(q.getByText("yaml body")).toBeTruthy();
  expect(q.queryByText("overview body")).toBeNull();
  expect(
    q.getByRole("tab", { name: "YAML" }).getAttribute("aria-selected"),
  ).toBe("true");
  expect(
    q.getByRole("tab", { name: "Overview" }).getAttribute("aria-selected"),
  ).toBe("false");
});

test("onTab receives the clicked tab id; the shell keeps no selection of its own", () => {
  const onTab = mock((_id: string) => {});
  render(
    <DetailShell title="t" tabs={TABS} active="overview" onTab={onTab}>
      <p>content</p>
    </DetailShell>,
  );
  fireEvent.click(body().getByRole("tab", { name: "YAML" }));
  expect(onTab).toHaveBeenCalledTimes(1);
  expect(onTab.mock.calls[0]?.[0]).toBe("yaml");
  // Selection is the caller's: without a new `active` nothing changes.
  expect(
    body().getByRole("tab", { name: "Overview" }).getAttribute("aria-selected"),
  ).toBe("true");
});

test("renders header, actions and status, with no rail by default", () => {
  render(<Harness />);
  const q = body();
  expect(q.getByRole("heading", { name: "web-1" })).toBeTruthy();
  expect(q.getByText("Deployment")).toBeTruthy();
  expect(q.getByText("Healthy")).toBeTruthy();
  expect(q.getByRole("button", { name: "Scale" })).toBeTruthy();
});

test("a rail renders beside the body", () => {
  render(
    <DetailShell
      title="t"
      tabs={TABS}
      active="overview"
      onTab={() => {}}
      rail={<aside>live metrics</aside>}
    >
      <p>content</p>
    </DetailShell>,
  );
  expect(body().getByText("live metrics")).toBeTruthy();
  expect(body().getByText("content")).toBeTruthy();
});

test("has no keyboard or URL-hash handling: a hash change does not switch tabs", () => {
  render(<Harness />);
  window.location.hash = "#yaml";
  fireEvent.keyDown(body().getByRole("tab", { name: "Overview" }), {
    key: "ArrowRight",
  });
  expect(body().getByText("overview body")).toBeTruthy();
});
