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
 * Renders through @testing-library/react on the same React runtime the
 * browser uses.
 */
import { afterAll, afterEach, mock } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { act, cleanup, within } from "@testing-library/react";
import * as React from "react";

export {
  act,
  fireEvent,
  render,
  waitFor,
} from "@testing-library/react";

const IS_BROWSER_MODULE = "@/src/lib/is-browser.ts";

/**
 * React's shared internals, for the one field the act check reads. React
 * sets `actQueue` while an act() scope is open and parks every update made
 * outside act in it until the scope closes.
 */
const reactInternals = (
  React as unknown as {
    __CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE: {
      actQueue: unknown[] | null;
    };
  }
).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;

/**
 * Fails a test that ends with an act() scope still open: an
 * `act(async ...)` it did not await, or one whose callback never settles.
 * An open scope cannot be closed from outside React. It swallows every later
 * update, in this file and in every file after it in the same process, so
 * components stop rendering in tests far from the cause. Failing here names
 * the test that caused it; tests after it may fail too until it is fixed.
 */
function failOnOpenActScope(): void {
  if (reactInternals.actQueue === null) return;
  throw new Error(
    "This test left a React act() scope open: await every act(async ...) " +
      "(and every helper that returns one), and make sure its callback " +
      "settles. Until it is fixed, later tests in this run stop rendering " +
      "updates and fail too.",
  );
}

export interface DomOptions {
  /** What IS_BROWSER reads as in this file. Default false, as on the server. */
  browser?: boolean;
}

export function setupDom({ browser = false }: DomOptions = {}): void {
  const registeredHere = !GlobalRegistrator.isRegistered;
  if (registeredHere) GlobalRegistrator.register({ url: "http://localhost/" });
  setBrowser(browser);

  afterEach(() => {
    cleanup();
    failOnOpenActScope();
  });
  afterAll(async () => {
    cleanup();
    // React's scheduler may still hold a render task queued by an update made
    // outside act. It reads `window.event` as soon as it runs, so let it run
    // while this file's document still exists.
    for (let i = 0; i < 3; i++) await new Promise((r) => setTimeout(r, 0));
    setBrowser(false);
    if (registeredHere) GlobalRegistrator.unregister();
  });
}

/**
 * Change what IS_BROWSER reads as mid-file, for a test that renders the
 * server pass and then the browser pass. afterAll still puts false back.
 */
export function setBrowser(value: boolean): void {
  mock.module(IS_BROWSER_MODULE, () => ({ IS_BROWSER: value }));
}

/** Testing Library queries bound to the current document's body. */
export function body(): ReturnType<typeof within> {
  return within(document.body);
}

/**
 * Lets queued fetch responses, timers set to 0 and the renders they cause
 * finish, in ten rounds of the macrotask queue.
 */
export function settle(): Promise<void> {
  return act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });
}

/** A JSON response with `data` in the API's `{ data }` envelope. */
export function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
