import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import {
  statusFilterFromQuery,
  syncStatusParam,
} from "@/src/islands/ResourceTable.tsx";

const POD_CHIPS = ["All", "Running", "Pending", "Failed"];
const PAGE_URL = "http://localhost/workloads/pods";

beforeAll(() => GlobalRegistrator.register({ url: PAGE_URL }));
afterAll(() => GlobalRegistrator.unregister());

describe("statusFilterFromQuery", () => {
  test("opens on the chip the dashboard's Failed pods link names", () => {
    expect(statusFilterFromQuery("?status=failed", POD_CHIPS)).toBe("failed");
  });

  test("matches the chip case-insensitively", () => {
    expect(statusFilterFromQuery("?status=Pending", POD_CHIPS)).toBe("pending");
  });

  test("ignores a status this table has no chip for", () => {
    // Progressing is a Deployment chip, not a pod one.
    expect(statusFilterFromQuery("?status=progressing", POD_CHIPS)).toBeNull();
  });

  test("leaves the filter alone without a status parameter", () => {
    expect(statusFilterFromQuery("", POD_CHIPS)).toBeNull();
    expect(statusFilterFromQuery("?status=", POD_CHIPS)).toBeNull();
    expect(statusFilterFromQuery("?action=create", POD_CHIPS)).toBeNull();
  });
});

describe("syncStatusParam", () => {
  test("writes the chosen chip into the URL", () => {
    history.replaceState(null, "", "/workloads/pods");
    syncStatusParam("failed");
    expect(location.search).toBe("?status=failed");
  });

  test("drops the parameter for All, so a restored view does not reopen Failed", () => {
    // The finding this guards: open the Failed link, restore a saved view
    // whose status is "all", reload -- the table must not come back on Failed.
    history.replaceState(null, "", "/workloads/pods?status=failed");
    syncStatusParam("all");
    expect(location.search).toBe("");
    expect(statusFilterFromQuery(location.search, POD_CHIPS)).toBeNull();
  });

  test("keeps other parameters and the history entry's state", () => {
    history.replaceState({ idx: 3 }, "", "/workloads/pods?action=create");
    syncStatusParam("pending");
    const params = new URLSearchParams(location.search);
    expect(params.get("action")).toBe("create");
    expect(params.get("status")).toBe("pending");
    expect(history.state).toEqual({ idx: 3 });
  });

  test("writes nothing when the URL already names the chip", () => {
    history.replaceState(null, "", "/workloads/pods?status=failed");
    const original = history.replaceState.bind(history);
    let writes = 0;
    history.replaceState = (...args: Parameters<History["replaceState"]>) => {
      writes++;
      original(...args);
    };
    try {
      syncStatusParam("failed");
    } finally {
      history.replaceState = original;
    }
    expect(writes).toBe(0);
  });
});
