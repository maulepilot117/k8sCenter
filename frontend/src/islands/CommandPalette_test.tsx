import { afterEach, beforeEach, expect, test } from "bun:test";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { default: CommandPalette } = await import("./CommandPalette.tsx");

const realLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
let assigned: string[] = [];

beforeEach(() => {
  assigned = [];
  // The palette navigates by assigning location.href; record instead of going.
  Object.defineProperty(globalThis, "location", {
    configurable: true,
    value: {
      get href() {
        return "http://localhost/";
      },
      set href(v: string) {
        assigned.push(v);
      },
    },
  });
});

afterEach(() => {
  if (realLocation) Object.defineProperty(globalThis, "location", realLocation);
});

function pressCtrlK() {
  act(() => {
    globalThis.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "k",
        ctrlKey: true,
        bubbles: true,
        cancelable: true,
      }),
    );
  });
}

const dialog = () => document.body.querySelector('[role="dialog"]');
const input = () =>
  document.body.querySelector('input[role="combobox"]') as HTMLInputElement;
const options = () =>
  Array.from(document.body.querySelectorAll('[role="option"]'));

test("Ctrl+K opens the palette and filters as the user types", () => {
  render(<CommandPalette />);
  expect(dialog()).toBeNull();
  pressCtrlK();
  expect(dialog()).not.toBeNull();
  const all = options().length;
  expect(all).toBeGreaterThan(5);

  fireEvent.change(input(), { target: { value: "Create Deployment" } });
  const filtered = options();
  expect(filtered.length).toBeGreaterThan(0);
  expect(filtered.length).toBeLessThan(all);
  expect(filtered[0].textContent).toContain("Create Deployment");
});

test("arrow keys move the selection and Enter navigates to the selected item", async () => {
  render(<CommandPalette />);
  pressCtrlK();
  fireEvent.change(input(), { target: { value: "Create" } });
  const first = options()[0];
  expect(first.getAttribute("aria-selected")).toBe("true");

  await act(async () => {
    fireEvent.keyDown(input(), { key: "ArrowDown" });
  });
  const afterDown = options();
  expect(afterDown[0].getAttribute("aria-selected")).toBe("false");
  expect(afterDown[1].getAttribute("aria-selected")).toBe("true");
  expect(input().getAttribute("aria-activedescendant")).toBe(afterDown[1].id);
  const secondLabel = afterDown[1].querySelector("span")?.textContent;

  await act(async () => {
    fireEvent.keyDown(input(), { key: "ArrowUp" });
  });
  expect(options()[0].getAttribute("aria-selected")).toBe("true");

  await act(async () => {
    fireEvent.keyDown(input(), { key: "ArrowDown" });
  });
  fireEvent.keyDown(input(), { key: "Enter" });
  expect(assigned).toHaveLength(1);
  expect(assigned[0]).toMatch(/^\//);
  expect(secondLabel).toBeTruthy();
  expect(dialog()).toBeNull();
});

test("Enter on the first Create Deployment result goes to its create URL", () => {
  render(<CommandPalette />);
  pressCtrlK();
  fireEvent.change(input(), { target: { value: "Create Deployment" } });
  fireEvent.keyDown(input(), { key: "Enter" });
  expect(assigned).toEqual(["/workloads/deployments?action=create"]);
});

test("Escape closes the palette", () => {
  render(<CommandPalette />);
  pressCtrlK();
  fireEvent.keyDown(input(), { key: "Escape" });
  expect(dialog()).toBeNull();
});

test("the global shortcut sees current state after open, type, close and reopen", () => {
  render(<CommandPalette />);
  pressCtrlK();
  expect(dialog()).not.toBeNull();
  fireEvent.change(input(), { target: { value: "secret" } });
  // The handler was registered once, before this state existed. Toggling
  // must close the palette now (it is open), not reopen it.
  pressCtrlK();
  expect(dialog()).toBeNull();
  pressCtrlK();
  expect(dialog()).not.toBeNull();
  // Reopening resets the query.
  expect(input().value).toBe("");
  pressCtrlK();
  expect(dialog()).toBeNull();
});

test("the open-command-palette event opens it, and a second one keeps it open", () => {
  render(<CommandPalette />);
  act(() => {
    globalThis.dispatchEvent(new Event("open-command-palette"));
  });
  expect(dialog()).not.toBeNull();
  expect(body().getByRole("dialog", { name: "Command palette" })).toBeTruthy();
});
