/**
 * Test preload (bunfig.toml): loads react-dom and Testing Library once, with
 * a document present, before any test file runs.
 *
 * react-dom decides once, when it first loads, whether it runs in a browser
 * and whether the `input` event exists. Loaded without a document, it falls
 * back to an old-IE polyfill for `onChange` on text fields that never fires,
 * so typing in a test does nothing while clicks still work. A test file's own
 * imports load react-dom before its `setupDom()` call can register happy-dom,
 * and Bun evaluates CommonJS packages such as react-dom ahead of the module
 * code that imports them, so only a preload reliably comes first.
 *
 * react-dom and its scheduler also keep the timer functions they find when
 * they load. happy-dom replaces those with its window's own, which stop
 * firing once the window is removed, and then every update made outside
 * act() (a fetch or timer callback, anything `waitFor` waits on) never
 * renders. So Bun's own timers are put back for the duration of the load.
 *
 * happy-dom is registered only while the two packages load and removed again,
 * so a test file that never calls `setupDom()` still sees Bun's own fetch,
 * Request, Response and WebSocket.
 */
import { GlobalRegistrator } from "@happy-dom/global-registrator";

const TIMER_GLOBALS = [
  "setTimeout",
  "clearTimeout",
  "setInterval",
  "clearInterval",
  "setImmediate",
  "clearImmediate",
  "queueMicrotask",
  "MessageChannel",
] as const;

const globals = globalThis as unknown as Record<string, unknown>;
const bunTimers = Object.fromEntries(
  TIMER_GLOBALS.map((name) => [name, globals[name]]),
);

GlobalRegistrator.register({ url: "http://localhost/" });
try {
  Object.assign(globals, bunTimers);
  await import("react-dom/client");
  await import("@testing-library/react");
} finally {
  await GlobalRegistrator.unregister();
}
