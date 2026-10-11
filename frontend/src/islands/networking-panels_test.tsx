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
const { default: CiliumSubsystems } = await import("./CiliumSubsystems.tsx");
const { default: NodeConnectivity } = await import("./NodeConnectivity.tsx");

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

function ok(data: unknown): Reply {
  return { status: 200, body: { data } };
}

test("subsystems name a list the cluster refused instead of breaking on it", async () => {
  const root = await mount(
    CiliumSubsystems,
    ok({
      configured: true,
      encryption: {
        enabled: false,
        mode: "",
        nodesEncrypted: 0,
        nodesTotal: 0,
      },
      mesh: { enabled: false, engine: "none" },
      clusterMesh: { enabled: false },
      // No endpoints: the remote CiliumEndpoint list was refused.
      coverage: [
        {
          source: "ciliumendpoints",
          status: "forbidden",
          reasonCode: "forbidden",
        },
      ],
    }),
  );
  expect(root.textContent).toContain(
    "You are not allowed to list Cilium endpoints",
  );
  // The other sections still render, and the endpoints are not zeros.
  expect(root.textContent).toContain("Encryption");
  const endpoints = [...root.querySelectorAll("div")].find(
    (d) => d.firstElementChild?.textContent?.trim() === "Endpoints",
  ) as HTMLElement;
  expect(endpoints.textContent).toContain("Not available");
  expect(endpoints.textContent).not.toContain("Total");
});

test("a node whose agent exec timed out shows an exec problem, not ill health", async () => {
  const root = await mount(
    NodeConnectivity,
    ok({
      configured: true,
      execEnabled: true,
      partial: true,
      nodes: [
        { nodeName: "node-a", healthState: "Ok" },
        { nodeName: "node-b", healthState: "Unknown", execError: "timeout" },
        { nodeName: "node-c", healthState: "Failure", message: "unreachable" },
      ],
    }),
  );
  const row = (name: string) =>
    [...root.querySelectorAll("[data-node]")].find(
      (el) => el.getAttribute("data-node") === name,
    ) as HTMLElement;

  expect(row("node-b").textContent).toContain("Exec problem");
  expect(row("node-b").textContent).toContain("timed out");
  expect(row("node-b").textContent).not.toContain("Unknown");
  // A node that is really unhealthy still reads as health, not exec.
  expect(row("node-c").textContent).toContain("Failure");
  expect(row("node-c").textContent).not.toContain("Exec problem");
});
