/**
 * The one test setup for components on React APIs.
 *
 *     import { body, render, setupDom } from "@/src/test-utils/render.tsx";
 *
 *     setupDom({ browser: true });
 *     const { default: Island } = await import("./Island.tsx");
 *
 *     test("...", () => {
 *       render(<Island />);
 *       body().getByRole("button", { name: "Save" });
 *     });
 *
 * - `setupDom` registers happy-dom for this file only and removes it in
 *   afterAll. It is deliberately not a global preload: happy-dom replaces
 *   `fetch`, `Request`, `Response` and `WebSocket`, and the server tests need
 *   Bun's own.
 * - bun shares modules across test files, so `setupDom` sets IS_BROWSER for
 *   this file every time (false unless `browser: true`) and puts false back
 *   in afterAll. Import the component under test after calling it.
 * - Query through `body()` or what `render()` returns, never Testing
 *   Library's `screen`: `screen` binds to the document that existed when the
 *   library first loaded, which may be another file's or none.
 *
 * Renders through @testing-library/preact while the app runs on Preact
 * (its query API matches @testing-library/react), so a converted component
 * and the unconverted components it renders run on one runtime, as in the
 * browser. When the real React runtime is installed, only this file's
 * imports change.
 */
import { afterAll, afterEach, mock } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { cleanup, within } from "@testing-library/preact";

export {
  act,
  fireEvent,
  render,
  waitFor,
} from "@testing-library/preact";

const IS_BROWSER_MODULE = "@/src/lib/is-browser.ts";

export interface DomOptions {
  /** What IS_BROWSER reads as in this file. Default false, as on the server. */
  browser?: boolean;
}

export function setupDom({ browser = false }: DomOptions = {}): void {
  const registeredHere = !GlobalRegistrator.isRegistered;
  if (registeredHere) GlobalRegistrator.register({ url: "http://localhost/" });
  mock.module(IS_BROWSER_MODULE, () => ({ IS_BROWSER: browser }));

  afterEach(() => cleanup());
  afterAll(() => {
    cleanup();
    mock.module(IS_BROWSER_MODULE, () => ({ IS_BROWSER: false }));
    if (registeredHere) GlobalRegistrator.unregister();
  });
}

/** Testing Library queries bound to the current document's body. */
export function body(): ReturnType<typeof within> {
  return within(document.body);
}
