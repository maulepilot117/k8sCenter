import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  REMOTE_COUNT_CAP,
  resetCountsForTest,
  resourceCounts,
  resourceCountsTruncated,
} from "@/src/lib/resource-counts.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The workloads, config, HPA and PDB list pages share one shape: a heading, a
 * subtitle taken from the shared counts store, a "New ..." button that opens
 * the kind's wizard (or links to a create page when the kind has none), the
 * resource table, and a ?action=create deep link that opens the wizard once
 * and cleans the URL.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: WorkloadsDashboard } = await import(
  "./WorkloadsDashboard.tsx"
);
const { default: ConfigDashboard } = await import("./ConfigDashboard.tsx");
const { default: HPAsDashboard } = await import("./HPAsDashboard.tsx");
const { default: PDBsDashboard } = await import("./PDBsDashboard.tsx");

/** The live-update socket is not under test; it only has to stay quiet. */
class QuietWebSocket {
  static readonly OPEN = 1;
  readyState = 0;
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

let urls: string[] = [];

/** The paragraph under the page heading; the table repeats the same words. */
function subtitleOf(heading: string): string | null {
  const h1 = body().getByRole("heading", { name: heading, level: 1 });
  return h1.parentElement?.querySelector("p")?.textContent ?? null;
}

beforeEach(() => {
  urls = [];
  // The counts store is shared by every test file in the process; the
  // subtitles read "Loading…" only while it is empty.
  resetCountsForTest();
  setAccessToken("t");
  globalThis.WebSocket = QuietWebSocket as unknown as typeof WebSocket;
  globalThis.fetch = (async (input: string | URL | Request) => {
    urls.push(String(input));
    return json([]);
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  // Drop the quiet socket from the shared ws store; left in CONNECTING, it
  // stops a later test file's subscription from opening its own socket.
  disconnectWS();
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  setAccessToken(null);
  resetCountsForTest();
  selectedNamespace.value = "all";
  window.history.replaceState({}, "", "/");
});

// ---- Workloads

test("workloads: heading and subtitle follow the path and the counts store", async () => {
  render(<WorkloadsDashboard currentPath="/workloads/statefulsets" />);
  await settle();
  expect(body().getByRole("heading", { name: "StatefulSets" })).toBeTruthy();
  expect(body().getByText("Loading statefulsets…")).toBeTruthy();
  await act(async () => {
    resourceCounts.value = { statefulsets: 3 };
  });
  expect(subtitleOf("StatefulSets")).toBe("3 statefulsets");
  await act(async () => {
    resourceCounts.value = {};
  });
  expect(subtitleOf("StatefulSets")).toBe("0 statefulsets");
});

test("workloads: an unknown path falls back to deployments, a nested path matches its tab", async () => {
  const view = render(
    <WorkloadsDashboard currentPath="/workloads/elsewhere" />,
  );
  await settle();
  expect(body().getByRole("heading", { name: "Deployments" })).toBeTruthy();
  view.rerender(<WorkloadsDashboard currentPath="/workloads/jobs/x" />);
  await settle();
  expect(body().getByRole("heading", { name: "Jobs" })).toBeTruthy();
});

test("workloads: a count at the remote cap reads as a lower bound", async () => {
  render(<WorkloadsDashboard currentPath="/workloads/deployments" />);
  await act(async () => {
    resourceCountsTruncated.value = true;
    resourceCounts.value = { deployments: REMOTE_COUNT_CAP };
  });
  expect(body().getByText(`${REMOTE_COUNT_CAP}+ deployments`)).toBeTruthy();
});

test("workloads: New opens the wizard and closing it hides it", async () => {
  render(<WorkloadsDashboard currentPath="/workloads/deployments" />);
  await settle();
  expect(body().queryByText(/^Create Deployment/)).toBeNull();
  fireEvent.click(body().getByRole("button", { name: /New Deployment/ }));
  await settle();
  expect(body().getByText(/^Create Deployment/)).toBeTruthy();
  fireEvent.click(body().getByRole("button", { name: "Close" }));
  await settle();
  expect(body().queryByText(/^Create Deployment/)).toBeNull();
});

test("workloads: a kind without a wizard links to its create page", async () => {
  render(<WorkloadsDashboard currentPath="/workloads/pods" />);
  await settle();
  expect(body().queryByRole("button", { name: /New Pod/ })).toBeNull();
  const link = body().getByRole("link", { name: /New Pod/ });
  expect(link.getAttribute("href")).toBe("/workloads/pods/new");
});

test("workloads: ?action=create opens the wizard once and removes the parameter", async () => {
  window.history.replaceState(
    {},
    "",
    "/workloads/deployments?action=create&x=1",
  );
  render(<WorkloadsDashboard currentPath="/workloads/deployments" />);
  await settle();
  expect(body().getByText(/^Create Deployment/)).toBeTruthy();
  expect(window.location.search).toBe("?x=1");
});

test("workloads: ?action=create is ignored for a kind without a wizard", async () => {
  window.history.replaceState({}, "", "/workloads/pods?action=create");
  render(<WorkloadsDashboard currentPath="/workloads/pods" />);
  await settle();
  expect(window.location.search).toBe("?action=create");
});

test("workloads: the table lists the kind the page is for", async () => {
  render(<WorkloadsDashboard currentPath="/workloads/daemonsets" />);
  await settle();
  expect(urls.some((u) => u.includes("/resources/daemonsets"))).toBe(true);
});

// ---- Config

test("config: heading and subtitle follow the path; secrets have no subtitle", async () => {
  const view = render(<ConfigDashboard currentPath="/config/limitranges/" />);
  await settle();
  expect(body().getByRole("heading", { name: "Limit Ranges" })).toBeTruthy();
  expect(body().getByText("Loading limit ranges…")).toBeTruthy();
  await act(async () => {
    resourceCounts.value = { limitranges: 2, configmaps: 5 };
  });
  expect(body().getByText("2 limit ranges")).toBeTruthy();

  view.rerender(<ConfigDashboard currentPath="/config/secrets" />);
  await settle();
  expect(body().getByRole("heading", { name: "Secrets" })).toBeTruthy();
  expect(subtitleOf("Secrets")).toBeNull();

  view.rerender(<ConfigDashboard currentPath="/config" />);
  await settle();
  expect(body().getByRole("heading", { name: "ConfigMaps" })).toBeTruthy();
  expect(body().getByText("5 configmaps")).toBeTruthy();
});

test("config: only ConfigMaps and Secrets get a New button, which opens the wizard", async () => {
  const view = render(<ConfigDashboard currentPath="/config/configmaps" />);
  await settle();
  fireEvent.click(body().getByRole("button", { name: /New ConfigMap/ }));
  await settle();
  expect(body().getByText("Create ConfigMap")).toBeTruthy();
  fireEvent.click(body().getByRole("button", { name: "Close" }));
  await settle();
  expect(body().queryByText("Create ConfigMap")).toBeNull();

  view.rerender(<ConfigDashboard currentPath="/config/serviceaccounts" />);
  await settle();
  expect(
    body().getByRole("heading", { name: "Service Accounts" }),
  ).toBeTruthy();
  expect(body().queryByRole("button", { name: /^New/ })).toBeNull();
});

test("config: ?action=create opens the wizard once and removes the parameter", async () => {
  window.history.replaceState({}, "", "/config/secrets?action=create");
  render(<ConfigDashboard currentPath="/config/secrets" />);
  await settle();
  expect(body().getByText(/^Create Secret/)).toBeTruthy();
  expect(window.location.search).toBe("");
});

// ---- HPAs and PDBs

test("hpas: heading, subtitle, New HPA wizard and table", async () => {
  render(<HPAsDashboard />);
  await settle();
  expect(
    body().getByRole("heading", { name: "HorizontalPodAutoscalers" }),
  ).toBeTruthy();
  expect(body().getByText("Loading horizontal pod autoscalers…")).toBeTruthy();
  await act(async () => {
    resourceCounts.value = { horizontalpodautoscalers: 6 };
  });
  expect(body().getByText("6 horizontal pod autoscalers")).toBeTruthy();
  expect(urls.some((u) => u.includes("/resources/hpas"))).toBe(true);

  fireEvent.click(body().getByRole("button", { name: /New HPA/ }));
  await settle();
  expect(body().getByText("Create HorizontalPodAutoscaler")).toBeTruthy();
  fireEvent.click(body().getByRole("button", { name: "Close" }));
  await settle();
  expect(body().queryByText("Create HorizontalPodAutoscaler")).toBeNull();
});

test("hpas: ?action=create opens the wizard once and removes the parameter", async () => {
  window.history.replaceState({}, "", "/scaling/hpas?action=create");
  render(<HPAsDashboard />);
  await settle();
  expect(body().getByText("Create HorizontalPodAutoscaler")).toBeTruthy();
  expect(window.location.search).toBe("");
});

test("pdbs: heading, subtitle, New PDB wizard and table", async () => {
  render(<PDBsDashboard />);
  await settle();
  expect(
    body().getByRole("heading", { name: "PodDisruptionBudgets" }),
  ).toBeTruthy();
  expect(body().getByText("Loading pod disruption budgets…")).toBeTruthy();
  await act(async () => {
    resourceCounts.value = { poddisruptionbudgets: 1 };
  });
  expect(body().getByText("1 pod disruption budgets")).toBeTruthy();
  expect(urls.some((u) => u.includes("/resources/pdbs"))).toBe(true);

  fireEvent.click(body().getByRole("button", { name: /New PDB/ }));
  await settle();
  expect(body().getByText("Create PodDisruptionBudget")).toBeTruthy();
  fireEvent.click(body().getByRole("button", { name: "Close" }));
  await settle();
  expect(body().queryByText("Create PodDisruptionBudget")).toBeNull();
});

test("pdbs: ?action=create opens the wizard once and removes the parameter", async () => {
  window.history.replaceState({}, "", "/scaling/pdbs?action=create");
  render(<PDBsDashboard />);
  await settle();
  expect(body().getByText("Create PodDisruptionBudget")).toBeTruthy();
  expect(window.location.search).toBe("");
});

test("a namespace change re-renders the subtitle from the counts store", async () => {
  render(<HPAsDashboard />);
  await settle();
  await act(async () => {
    resourceCounts.value = { horizontalpodautoscalers: 1 };
  });
  expect(body().getByText("1 horizontal pod autoscalers")).toBeTruthy();
  await act(async () => {
    selectedNamespace.value = "team";
    resourceCounts.value = { horizontalpodautoscalers: 9 };
  });
  expect(body().getByText("9 horizontal pod autoscalers")).toBeTruthy();
});
