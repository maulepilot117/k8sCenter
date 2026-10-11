/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import type { ComponentType } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The networking status panels render a failed read from its reason (#608
 * U7, KTD5, KTD14): "not permitted" with no retry, "cannot be read" with
 * Try again, and a reload for a changed registration. An unknown answer
 * never reads as "not configured" (R4).
 *
 * As in NamespaceTopology_test.tsx, IS_BROWSER is stubbed to true for these
 * islands and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: BgpStatus } = await import("./BgpStatus.tsx");
const { default: IpamStatus } = await import("./IpamStatus.tsx");
const { default: CniOverview } = await import("./CniOverview.tsx");

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

type Reply = { status: number; body: unknown };

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let fetches = 0;
// Replies are consumed in order; the last one repeats.
let replies: Reply[] = [];

beforeEach(() => {
  fetches = 0;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => {
    fetches++;
    const reply = (replies.length > 1 ? replies.shift() : replies[0]) as Reply;
    return new Response(JSON.stringify(reply.body), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

function fail(status: number, reason?: string): Reply {
  return {
    status,
    body: { error: { code: status, message: "server wording", reason } },
  };
}

/** Lets fetches, body reads and state updates run (bounded). */
async function settle() {
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount(Island: ComponentType, ...r: Reply[]) {
  replies = r;
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<Island />, host as HTMLElement));
  await settle();
  return host;
}

function buttons(root: HTMLElement, label: string) {
  return [...root.querySelectorAll("button")].filter(
    (b) => b.textContent?.trim() === label,
  );
}

test("BGP that cannot be discovered reads as 'cannot be read', not 'not configured'", async () => {
  const root = await mount(BgpStatus, fail(502, "discovery_unavailable"), {
    status: 200,
    body: { data: { configured: false } },
  });
  expect(root.textContent).toContain("Could not read BGP status");
  expect(root.textContent).not.toContain("not configured");
  expect(root.textContent).not.toContain("server wording");

  // Try again reads once more and renders the new answer.
  act(() => (buttons(root, "Try again")[0] as HTMLButtonElement).click());
  await settle();
  expect(fetches).toBe(2);
  expect(root.textContent).toContain("BGP is not configured");
});

test("a refused IPAM read says not permitted and offers no recovery", async () => {
  const root = await mount(IpamStatus, fail(403, "forbidden"));
  expect(root.textContent).toContain("You do not have permission to read");
  expect(buttons(root, "Try again")).toHaveLength(0);
  expect(buttons(root, "Reload")).toHaveLength(0);
});

test("a CNI read for an unknown cluster asks for a reload", async () => {
  const root = await mount(CniOverview, fail(404, "cluster_unknown"));
  expect(root.textContent).toContain("registration changed");
  expect(buttons(root, "Reload")).toHaveLength(1);
  expect(root.textContent).not.toContain("No CNI Plugin Detected");
});
