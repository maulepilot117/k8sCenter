import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import {
  namespaceFromQuery,
  namespaceSwitchNotice,
  podChipStatus,
  podPhasesForChip,
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

describe("podChipStatus", () => {
  test("Failed holds only phase Failed, matching the dashboard count", () => {
    expect(podChipStatus("Failed")).toBe("failed");
    // Unknown is a lost node, not a failure, and pods.failed excludes it.
    expect(podChipStatus("Unknown")).toBe("unknown");
    expect(podChipStatus(undefined)).toBe("unknown");
  });

  test("Running holds Running and Succeeded; Pending holds Pending", () => {
    expect(podChipStatus("Running")).toBe("running");
    expect(podChipStatus("Succeeded")).toBe("running");
    expect(podChipStatus("Pending")).toBe("pending");
  });

  test("no chip matches an Unknown pod", () => {
    const chips = POD_CHIPS.map((c) => c.toLowerCase());
    expect(chips).not.toContain(podChipStatus("Unknown"));
  });
});

describe("namespaceFromQuery", () => {
  test("reads all and namespace names", () => {
    expect(namespaceFromQuery("?status=failed&namespace=all")).toBe("all");
    expect(namespaceFromQuery("?namespace=minigolf")).toBe("minigolf");
  });

  test("ignores an absent, empty or invalid namespace", () => {
    expect(namespaceFromQuery("?status=failed")).toBeNull();
    expect(namespaceFromQuery("?namespace=")).toBeNull();
    expect(namespaceFromQuery("?namespace=Not_A_Namespace")).toBeNull();
    expect(namespaceFromQuery("?namespace=a/b")).toBeNull();
    expect(namespaceFromQuery(`?namespace=${"a".repeat(64)}`)).toBeNull();
  });

  test("names the switch it makes for every page", () => {
    expect(namespaceSwitchNotice("all")).toBe(
      "Namespace switched to all namespaces for every page, not just this table.",
    );
    expect(namespaceSwitchNotice("minigolf")).toContain("to minigolf for");
  });
});

describe("podPhasesForChip", () => {
  test("asks the server for exactly the phases the dashboard counts", () => {
    expect(podPhasesForChip("failed")).toEqual(["Failed"]);
    expect(podPhasesForChip("running")).toEqual(["Running", "Succeeded"]);
    expect(podPhasesForChip("pending")).toEqual(["Pending"]);
  });

  test("sends no phase for All or an unknown chip", () => {
    expect(podPhasesForChip("all")).toEqual([]);
    expect(podPhasesForChip("progressing")).toEqual([]);
  });

  test("the server filter and the row check agree for every chip", () => {
    // Live WS events are checked client-side with podChipStatus; a pod the
    // server returns for a chip must also pass that check, or it vanishes.
    for (const chip of ["running", "pending", "failed"]) {
      for (const phase of podPhasesForChip(chip)) {
        expect(podChipStatus(phase)).toBe(chip);
      }
    }
  });
});
