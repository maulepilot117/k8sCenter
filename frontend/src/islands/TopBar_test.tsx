/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { batch } from "@preact/signals-core";
// Loads preact/compat, as ToastProvider does on every ChromeLayout page.
import "react";
import {
  act,
  body,
  fireEvent,
  render,
  setBrowser,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// happy-dom replaces fetch; Bun's own must be put back for later server tests.
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: TopBarV2 } = await import("./TopBarV2.tsx");
const { default: ClusterSwitcher } = await import("./ClusterSwitcher.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");
const { LOCAL_CLUSTER_ID, LOCAL_GENERATION, selectedCluster, switchCluster } =
  await import("@/src/lib/cluster.ts");

const REMOTE = "remote-1";
const REMOTE_GENERATION = "2026-01-01T00:00:00Z";

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function stubFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/v1/clusters")) {
      return json([
        { id: "local", name: "local", isLocal: true, status: "connected" },
        {
          id: REMOTE,
          name: "prod-east",
          status: "connected",
          createdAt: REMOTE_GENERATION,
        },
      ]);
    }
    if (url.includes("/v1/resources/namespaces")) {
      return json([
        { metadata: { name: "team-a" } },
        { metadata: { name: "team-b" } },
      ]);
    }
    if (url.includes("/v1/auth/me")) {
      return json({ user: { id: "u1", username: "ada", roles: ["admin"] } });
    }
    return json([]);
  }) as unknown as typeof fetch;
}

/** The notification bell opens a socket; none of these tests need a server. */
class IdleSocket extends EventTarget {
  static readonly OPEN = 1;
  readyState = 0;
  send() {}
  close() {}
}

beforeEach(() => {
  stubFetch();
  globalThis.WebSocket = IdleSocket as unknown as typeof WebSocket;
  setBrowser(true);
  switchCluster(LOCAL_CLUSTER_ID, LOCAL_GENERATION);
  selectedNamespace.value = "all";
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
});

const trigger = () => body().getByRole("button", { name: /Active cluster/ });
const nsSelect = () =>
  body().getByLabelText("Select namespace") as HTMLSelectElement;

test("switching cluster updates the top bar and the namespace selector in one render pass", async () => {
  render(<TopBarV2 />);
  await waitFor(() => expect(nsSelect().options.length).toBeGreaterThan(2));
  expect(trigger().getAttribute("aria-label")).toContain("local");
  expect(nsSelect().value).toBe("all");

  // Every DOM mutation batch records what the top bar and the selector show
  // together; a render that applied one store and not the other would leave a
  // snapshot pairing the new cluster with the old namespace, or the reverse.
  const snapshots: string[] = [];
  const observer = new MutationObserver(() => {
    snapshots.push(`${trigger().textContent}|${nsSelect().value}`);
  });
  observer.observe(document.body, {
    subtree: true,
    childList: true,
    attributes: true,
    characterData: true,
  });

  act(() => {
    batch(() => {
      switchCluster(REMOTE, REMOTE_GENERATION);
      selectedNamespace.value = "team-b";
    });
  });
  observer.disconnect();

  expect(selectedCluster.value).toBe(REMOTE);
  expect(trigger().textContent).toContain("prod-east");
  expect(nsSelect().value).toBe("team-b");
  const torn = snapshots.filter((s) => {
    const switched = s.includes("prod-east");
    const moved = s.endsWith("|team-b");
    return switched !== moved;
  });
  expect(torn).toEqual([]);
});

test("a restored remote cluster and namespace appear after the server pass, not in it", async () => {
  switchCluster(REMOTE, REMOTE_GENERATION);
  selectedNamespace.value = "team-a";

  // The server pass: the placeholder, which cannot know what storage holds.
  setBrowser(false);
  const server = render(<TopBarV2 />);
  expect(trigger().getAttribute("aria-label")).toContain("local");
  expect(nsSelect().value).toBe("all");
  server.unmount();

  // The client pass shows the restored values.
  setBrowser(true);
  render(<TopBarV2 />);
  await waitFor(() => expect(nsSelect().value).toBe("team-a"));
  expect(trigger().getAttribute("aria-label")).toMatch(/remote-1|prod-east/);
});

test("the cluster list closes when focus leaves the switcher", async () => {
  const outside = document.createElement("button");
  outside.textContent = "outside";
  document.body.appendChild(outside);
  render(<ClusterSwitcher />);

  act(() => {
    fireEvent.click(trigger());
  });
  const list = body().getByRole("listbox");
  expect(list).toBeTruthy();

  // Focus moving between the switcher's own elements keeps it open.
  act(() => {
    trigger().focus();
  });
  expect(body().queryByRole("listbox")).not.toBeNull();
  act(() => {
    (body().getByRole("listbox") as HTMLElement).focus();
  });
  expect(body().queryByRole("listbox")).not.toBeNull();

  // Focus moving to an element outside closes it.
  act(() => {
    outside.focus();
  });
  await waitFor(() => expect(body().queryByRole("listbox")).toBeNull());
  outside.remove();
});
