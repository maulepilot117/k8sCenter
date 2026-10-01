/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { LiveBadge } from "@/components/ui/LiveBadge.tsx";
import { wsStatus } from "@/lib/ws.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";

/**
 * The socket behind wsStatus stays connected on every page (the
 * notification bell holds it open), but it only ever carries the LOCAL
 * cluster's events. A remote page must therefore never claim "Live": it
 * says the operator has to refresh instead (R-8 U14).
 */

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

let host: HTMLElement | null = null;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  wsStatus.value = "disconnected";
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

function mount(): HTMLElement {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<LiveBadge />, host as HTMLElement));
  return host;
}

test("a connected local cluster reads Live", () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  wsStatus.value = "connected";
  expect(mount().textContent).toBe("Live");
});

test("a disconnected local cluster shows no badge", () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  wsStatus.value = "disconnected";
  expect(mount().textContent).toBe("");
});

test("a remote selection asks for a refresh even while the socket is connected", () => {
  switchCluster("remote-1", "gen-1");
  wsStatus.value = "connected";
  const root = mount();
  expect(root.textContent).toBe("Refresh to update");
  expect(root.textContent).not.toContain("Live");
});
