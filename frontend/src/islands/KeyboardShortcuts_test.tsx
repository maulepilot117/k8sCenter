/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The page shortcuts ("?" for help, "/" for search) must not fire while the
 * user is typing text. The YAML editor is a contenteditable element, not an
 * input, so typing either character there used to open the help or jump to
 * the search box.
 */
setupDom({ browser: true });

const { default: KeyboardShortcuts } = await import("./KeyboardShortcuts.tsx");

function press(target: EventTarget, key: string) {
  const event = new KeyboardEvent("keydown", {
    key,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

const helpOpen = () =>
  document.body.textContent?.includes("Keyboard Shortcuts") ?? false;

test("? opens the help from the page", () => {
  render(<KeyboardShortcuts />);
  press(document.body, "?");
  expect(helpOpen()).toBe(true);
});

test("? toggles the help closed again on a second press", () => {
  render(<KeyboardShortcuts />);
  press(document.body, "?");
  expect(helpOpen()).toBe(true);
  press(document.body, "?");
  expect(helpOpen()).toBe(false);
  press(document.body, "?");
  expect(helpOpen()).toBe(true);
});

test("Escape closes the help and blurs the focused element", () => {
  render(<KeyboardShortcuts />);
  const button = document.createElement("button");
  document.body.appendChild(button);
  button.focus();
  press(document.body, "?");
  expect(helpOpen()).toBe(true);
  expect(document.activeElement).toBe(button);

  press(document.body, "Escape");
  expect(helpOpen()).toBe(false);
  expect(document.activeElement).not.toBe(button);
});

test("/ focuses the search input and prevents the default", () => {
  render(<KeyboardShortcuts />);
  const search = document.createElement("input");
  search.setAttribute("data-search-input", "");
  document.body.appendChild(search);

  const event = press(document.body, "/");
  expect(document.activeElement).toBe(search);
  expect(event.defaultPrevented).toBe(true);
});

test("? typed in a contenteditable editor stays in the editor", () => {
  render(<KeyboardShortcuts />);
  const editor = document.createElement("div");
  editor.contentEditable = "true";
  const line = document.createElement("div");
  editor.appendChild(line);
  document.body.appendChild(editor);

  press(line, "?");
  expect(helpOpen()).toBe(false);
});

test("? typed in an input does not open the help", () => {
  render(<KeyboardShortcuts />);
  const field = document.createElement("input");
  document.body.appendChild(field);
  press(field, "?");
  expect(helpOpen()).toBe(false);
});

test("a key already handled closer to the target is left alone", () => {
  render(<KeyboardShortcuts />);
  const swallow = (e: Event) => e.preventDefault();
  document.body.addEventListener("keydown", swallow);
  press(document.body, "?");
  document.body.removeEventListener("keydown", swallow);
  expect(helpOpen()).toBe(false);
});
