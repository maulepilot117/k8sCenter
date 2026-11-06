/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * The page shortcuts ("?" for help, "/" for search) must not fire while the
 * user is typing text. The YAML editor is a contenteditable element, not an
 * input, so typing either character there used to open the help or jump to
 * the search box.
 *
 * As in CiliumConfigEditor_test.tsx, IS_BROWSER is stubbed to true and put
 * back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: KeyboardShortcuts } = await import("./KeyboardShortcuts.tsx");

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  document.body.innerHTML = "";
});

function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<KeyboardShortcuts />, host as HTMLElement));
  return host;
}

function press(target: EventTarget, key: string) {
  act(() => {
    target.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
  });
}

const helpOpen = () =>
  document.body.textContent?.includes("Keyboard Shortcuts") ?? false;

test("? opens the help from the page", () => {
  mount();
  press(document.body, "?");
  expect(helpOpen()).toBe(true);
});

test("? typed in a contenteditable editor stays in the editor", () => {
  mount();
  const editor = document.createElement("div");
  editor.contentEditable = "true";
  const line = document.createElement("div");
  editor.appendChild(line);
  document.body.appendChild(editor);

  press(line, "?");
  expect(helpOpen()).toBe(false);
});
