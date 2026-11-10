/** @jsxImportSource react */
import { afterEach, describe, expect, test } from "bun:test";
import type { SourceState } from "@/lib/dashboard/data.ts";
import { PARAM_KEY_NAMESPACE, sourceKeyFor } from "@/lib/dashboard/params.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { getWidget } = await import("@/lib/dashboard/registry.ts");
// Only the widgets under test, not index.ts, so the test loads just the cards
// it checks.
await import("@/components/dashboard/widgets/StorageCapacityWidget.tsx");
await import("@/components/dashboard/widgets/HubbleFlowsWidget.tsx");
await import("@/components/dashboard/widgets/CertsExpiringWidget.tsx");
await import("@/components/dashboard/widgets/MtlsCoverageWidget.tsx");
await import("@/components/dashboard/widgets/TopConsumersWidget.tsx");

const seeded = new Set<string>();

/** Writes one source's state the way a landed fetch would. */
function seed(key: string, data: unknown): void {
  seeded.add(key);
  const state: SourceState = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
  dashboardData.signalFor(key).value = state;
}

afterEach(() => {
  for (const key of seeded) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
  seeded.clear();
});

/** Renders a registered widget the way the host does. */
function renderWidget(id: string, params: Record<string, string> = {}) {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  return render(<>{def.render({ mode: "normal", params })}</>);
}

function vector(samples: Array<[Record<string, string>, string]>) {
  return {
    resultType: "vector",
    result: samples.map(([metric, v]) => ({ metric, value: [0, v] })),
  };
}

describe("storage-capacity", () => {
  const classes = [
    { name: "fast-ssd", provisioner: "csi.example/fast", isDefault: true },
    { name: "cold-hdd", provisioner: "csi.example/cold", isDefault: false },
  ];

  test("shows the classes, the default flag and the fullest volume", () => {
    seed("storage-classes", classes);
    seed(
      "volume-capacity",
      vector([
        [{ namespace: "db", persistentvolumeclaim: "data-0" }, "91.4"],
        [{ namespace: "db", persistentvolumeclaim: "logs-0" }, "12"],
      ]),
    );
    renderWidget("storage-capacity");
    expect(body().getByTestId("storage-capacity-summary").textContent).toBe(
      "2 classes",
    );
    expect(body().getByText("fast-ssd")).toBeTruthy();
    expect(body().getByText("default")).toBeTruthy();
    expect(body().getByText("91%")).toBeTruthy();
    expect(body().getByText("data-0")).toBeTruthy();
    expect(body().queryByTestId("storage-capacity-no-default")).toBeNull();
  });

  test("empty readable payloads say nothing is there", () => {
    seed("storage-classes", []);
    seed("volume-capacity", vector([]));
    renderWidget("storage-capacity");
    expect(body().getByTestId("storage-capacity-no-classes")).toBeTruthy();
    expect(body().getByTestId("storage-capacity-volumes-empty")).toBeTruthy();
  });

  test("a new payload on the same keys replaces the rows", () => {
    seed("storage-classes", classes);
    seed(
      "volume-capacity",
      vector([[{ namespace: "db", persistentvolumeclaim: "data-0" }, "91.4"]]),
    );
    renderWidget("storage-capacity");
    expect(body().getByText("data-0")).toBeTruthy();
    act(() => {
      seed("storage-classes", [
        { name: "only-one", provisioner: "csi.example/x", isDefault: false },
      ]);
      seed(
        "volume-capacity",
        vector([
          [{ namespace: "web", persistentvolumeclaim: "cache-9" }, "50"],
        ]),
      );
    });
    expect(body().queryByText("data-0")).toBeNull();
    expect(body().getByText("cache-9")).toBeTruthy();
    expect(body().getByText("only-one")).toBeTruthy();
    expect(body().getByText("50%")).toBeTruthy();
    expect(body().getByTestId("storage-capacity-summary").textContent).toBe(
      "1 class",
    );
  });
});

describe("hubble-flows", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod" };
  const key = sourceKeyFor("hubble-flows", params);
  const flow = (over: Record<string, unknown>) => ({
    time: "2026-11-01T10:00:00Z",
    verdict: "forwarded",
    srcPod: "client",
    srcNamespace: "prod",
    dstPod: "server",
    dstNamespace: "prod",
    protocol: "TCP",
    dstPort: 8080,
    ...over,
  });

  test("shows the blocked share, the namespace and the dropped flow", () => {
    seed(key, [
      flow({ verdict: "dropped", dropReason: "POLICY_DENIED", dstPod: "db" }),
      flow({}),
      flow({}),
    ]);
    renderWidget("hubble-flows", params);
    expect(body().getByTestId("hubble-flows-namespace").textContent).toBe(
      "prod",
    );
    expect(body().getByTestId("hubble-flows-caption").textContent).toBe(
      "1 of the last 3 flows did not get through (33%).",
    );
    expect(body().getByText("prod/client → prod/db")).toBeTruthy();
    expect(body().getByText(/POLICY_DENIED/)).toBeTruthy();
  });

  test("identical blocked flows each get a row", () => {
    const dup = flow({ verdict: "dropped" });
    seed(key, [dup, { ...dup }]);
    renderWidget("hubble-flows", params);
    expect(body().getByTestId("hubble-flows-list").children.length).toBe(2);
  });

  test("an empty batch says Hubble saw no traffic", () => {
    seed(key, []);
    renderWidget("hubble-flows", params);
    expect(body().getByTestId("hubble-flows-none")).toBeTruthy();
  });

  test("a new payload on the same key replaces the rows", () => {
    seed(key, [flow({ verdict: "dropped", dstPod: "db" })]);
    renderWidget("hubble-flows", params);
    expect(body().getByText("prod/client → prod/db")).toBeTruthy();
    act(() => {
      seed(key, [
        flow({ verdict: "error", dstPod: "cache" }),
        flow({ verdict: "dropped", dstPod: "cache" }),
      ]);
    });
    expect(body().queryByText("prod/client → prod/db")).toBeNull();
    expect(body().getByTestId("hubble-flows-caption").textContent).toBe(
      "2 of the last 2 flows did not get through (100%).",
    );
    expect(body().getAllByText("prod/client → prod/cache").length).toBe(2);
  });
});

describe("certs-expiring", () => {
  const cert = (over: Record<string, unknown>) => ({
    namespace: "prod",
    name: "api-tls",
    daysRemaining: 3,
    warningThresholdDays: 30,
    criticalThresholdDays: 7,
    ...over,
  });

  test("lists the expiring certificate with its days left", () => {
    seed("certificates-list", [
      cert({}),
      cert({ name: "fine", daysRemaining: 90 }),
    ]);
    renderWidget("certs-expiring");
    expect(body().getByTestId("certs-expiring-summary").textContent).toBe(
      "1 of 2 expiring",
    );
    expect(body().getByText("api-tls")).toBeTruthy();
    expect(body().getByText("3d left")).toBeTruthy();
    expect(body().queryByText("fine")).toBeNull();
  });

  test("an empty inventory says nothing is managed", () => {
    seed("certificates-list", []);
    renderWidget("certs-expiring");
    expect(body().getByTestId("certs-expiring-none")).toBeTruthy();
  });

  test("a new payload on the same key replaces the rows", () => {
    seed("certificates-list", [cert({})]);
    renderWidget("certs-expiring");
    expect(body().getByText("api-tls")).toBeTruthy();
    act(() => {
      seed("certificates-list", [
        cert({ name: "web-tls", daysRemaining: -1 }),
        cert({ name: "mail-tls", daysRemaining: 20 }),
      ]);
    });
    expect(body().queryByText("api-tls")).toBeNull();
    expect(body().getByText("web-tls")).toBeTruthy();
    expect(body().getByText("expired")).toBeTruthy();
    expect(body().getByText("20d left")).toBeTruthy();
    expect(body().getByTestId("certs-expiring-summary").textContent).toBe(
      "2 of 2 expiring",
    );
  });
});

describe("mtls-coverage", () => {
  const wl = (over: Record<string, unknown>) => ({
    namespace: "shop",
    workload: "web",
    state: "active",
    mesh: "istio",
    ...over,
  });

  test("shows the strict share and the workload that is not strict", () => {
    seed("mesh-mtls", {
      workloads: [wl({}), wl({ workload: "legacy-api", state: "inactive" })],
    });
    renderWidget("mtls-coverage");
    expect(body().getByTestId("mtls-coverage-percent").textContent).toBe("50%");
    expect(body().getByTestId("mtls-coverage-caption").textContent).toBe(
      "1 of 2 meshed workloads enforce strict mTLS.",
    );
    expect(body().getByText("legacy-api")).toBeTruthy();
  });

  test("an installed mesh with no workloads says so", () => {
    seed("mesh-mtls", { workloads: [] });
    renderWidget("mtls-coverage");
    expect(body().getByTestId("mtls-coverage-none")).toBeTruthy();
  });

  test("a new payload on the same key replaces the figures", () => {
    seed("mesh-mtls", { workloads: [wl({ state: "inactive" })] });
    renderWidget("mtls-coverage");
    expect(body().getByTestId("mtls-coverage-percent").textContent).toBe("0%");
    act(() => {
      seed("mesh-mtls", {
        workloads: [wl({}), wl({ workload: "pay" }), wl({ workload: "cart" })],
      });
    });
    expect(body().getByTestId("mtls-coverage-percent").textContent).toBe(
      "100%",
    );
    expect(body().getByTestId("mtls-coverage-clear")).toBeTruthy();
  });
});

describe("top-consumers", () => {
  const cpu = vector([
    [{ namespace: "prod", pod: "busy-1" }, "0.5"],
    [{ namespace: "prod", pod: "idle-1" }, "0.01"],
  ]);
  const memory = vector([[{ namespace: "prod", pod: "hog-1" }, "2048"]]);

  test("shows CPU rows by default", () => {
    seed("top-consumers-cpu", cpu);
    seed("top-consumers-memory", memory);
    renderWidget("top-consumers");
    expect(body().getByText("busy-1")).toBeTruthy();
    expect(body().getByText("500m")).toBeTruthy();
    expect(body().queryByText("hog-1")).toBeNull();
  });

  test("pressing Memory shows the memory source's rows", () => {
    seed("top-consumers-cpu", cpu);
    seed("top-consumers-memory", memory);
    renderWidget("top-consumers");
    fireEvent.click(body().getByRole("button", { name: "Memory" }));
    expect(body().getByText("hog-1")).toBeTruthy();
    expect(body().getByText("2.0 GiB")).toBeTruthy();
    expect(body().queryByText("busy-1")).toBeNull();
  });

  test("an empty vector says nothing was reported", () => {
    seed("top-consumers-cpu", vector([]));
    renderWidget("top-consumers");
    expect(body().getByTestId("top-consumers-empty")).toBeTruthy();
  });

  test("the memory tab waits while its source has no data", () => {
    seed("top-consumers-cpu", cpu);
    renderWidget("top-consumers");
    fireEvent.click(body().getByRole("button", { name: "Memory" }));
    expect(body().getByTestId("top-consumers-pending")).toBeTruthy();
  });

  test("a new payload on the shown key replaces the rows", () => {
    seed("top-consumers-cpu", cpu);
    renderWidget("top-consumers");
    expect(body().getByText("busy-1")).toBeTruthy();
    act(() => {
      seed(
        "top-consumers-cpu",
        vector([[{ namespace: "prod", pod: "new-1" }, "2"]]),
      );
    });
    expect(body().queryByText("busy-1")).toBeNull();
    expect(body().getByText("new-1")).toBeTruthy();
    expect(body().getByText("2.00")).toBeTruthy();
  });

  test("a memory payload landing while the Memory tab is open updates it", () => {
    seed("top-consumers-cpu", cpu);
    seed("top-consumers-memory", memory);
    renderWidget("top-consumers");
    fireEvent.click(body().getByRole("button", { name: "Memory" }));
    act(() => {
      seed(
        "top-consumers-memory",
        vector([[{ namespace: "prod", pod: "big-2" }, "300"]]),
      );
    });
    expect(body().getByText("big-2")).toBeTruthy();
    expect(body().getByText("300 MiB")).toBeTruthy();
  });
});
