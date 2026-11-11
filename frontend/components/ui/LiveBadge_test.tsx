import { afterEach, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { LiveBadge } = await import("@/components/ui/LiveBadge.tsx");
const { wsStatus } = await import("@/lib/ws.ts");
const { LOCAL_CLUSTER_ID, switchCluster } = await import(
  "@/src/lib/cluster.ts"
);

/**
 * The socket behind wsStatus stays connected on every page (the
 * notification bell holds it open), but it only ever carries the LOCAL
 * cluster's events. A remote page must therefore never claim "Live": it
 * says the operator has to refresh instead (R-8 U14).
 */

afterEach(() => {
  act(() => {
    wsStatus.value = "disconnected";
    switchCluster(LOCAL_CLUSTER_ID, "local");
  });
});

function mount(): Element {
  return render(<LiveBadge />).container;
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
