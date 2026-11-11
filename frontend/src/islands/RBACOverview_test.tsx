import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  setBrowser,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The RBAC overview joins role bindings to roles: one row per subject of each
 * binding, with the number of rules its role carries. Clicking a row lists the
 * subject's effective permissions; system accounts are hidden until asked for.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: RBACOverview } = await import("./RBACOverview.tsx");

const roleBinding = {
  metadata: { name: "dev-binding", namespace: "default" },
  roleRef: { kind: "Role", name: "developer", apiGroup: "rbac" },
  subjects: [
    { kind: "User", name: "alice" },
    { kind: "Group", name: "devs" },
  ],
};
const clusterBinding = {
  metadata: { name: "admin-binding" },
  roleRef: { kind: "ClusterRole", name: "cluster-admin", apiGroup: "rbac" },
  subjects: [
    { kind: "User", name: "alice" },
    { kind: "User", name: "system:kube-scheduler" },
  ],
};
const roles = [
  {
    metadata: { name: "developer", namespace: "default" },
    rules: [
      { apiGroups: [""], resources: ["pods", "pods/log"], verbs: ["get"] },
      { apiGroups: ["apps"], resources: ["deployments"], verbs: ["list"] },
    ],
  },
];
const clusterRoles = [
  {
    metadata: { name: "cluster-admin" },
    rules: [{ apiGroups: ["*"], resources: ["*"], verbs: ["*"] }],
  },
];

let urls: string[] = [];

function stubApi(fail = false) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    if (fail) return json({}, 500);
    if (url.includes("/clusterrolebindings")) return json([clusterBinding]);
    if (url.includes("/rolebindings")) return json([roleBinding]);
    if (url.includes("/clusterroles")) return json(clusterRoles);
    if (url.includes("/roles")) return json(roles);
    return json([]);
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  urls = [];
  setAccessToken("t");
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

const rowOf = (name: string) =>
  body().getByRole("cell", { name }).closest("tr") as HTMLElement;

test("requests the four RBAC lists and shows one row per subject, system accounts hidden", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  expect(urls.sort()).toEqual([
    "/api/v1/resources/clusterrolebindings?limit=500",
    "/api/v1/resources/clusterroles?limit=500",
    "/api/v1/resources/rolebindings?limit=500",
    "/api/v1/resources/roles?limit=500",
  ]);
  expect(body().getByText("3 of 4 entries")).toBeTruthy();
  expect(body().queryByText("system:kube-scheduler")).toBeNull();
  const devs = rowOf("devs");
  expect(devs.textContent).toContain("Group");
  expect(devs.textContent).toContain("dev-binding");
  expect(devs.textContent).toContain("developer");
  expect(devs.textContent).toContain("default");
  expect(devs.textContent).toContain("2");
  const links = devs.querySelectorAll("a");
  expect(links[0].getAttribute("href")).toBe(
    "/rbac/rolebindings/default/dev-binding",
  );
  expect(links[1].getAttribute("href")).toBe("/rbac/roles/default/developer");
});

test("a cluster binding row reads (cluster) and links to cluster-scoped pages", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  const row = body()
    .getAllByRole("cell", { name: "alice" })
    .map((c: HTMLElement) => c.closest("tr") as HTMLElement)
    .find((r: HTMLElement) =>
      r.textContent?.includes("admin-binding"),
    ) as HTMLElement;
  expect(row.textContent).toContain("(cluster)");
  const links = row.querySelectorAll("a");
  expect(links[0].getAttribute("href")).toBe(
    "/rbac/clusterrolebindings/admin-binding",
  );
  expect(links[1].getAttribute("href")).toBe(
    "/rbac/clusterroles/cluster-admin",
  );
});

test("unticking hide system accounts shows them", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  fireEvent.click(body().getByLabelText("Hide system accounts"));
  expect(body().getByText("system:kube-scheduler")).toBeTruthy();
  expect(body().getByText("4 of 4 entries")).toBeTruthy();
});

test("the filter matches subject, role and binding names", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  const box = body().getByPlaceholderText(
    "Filter by subject, role, or binding...",
  );
  fireEvent.change(box, { target: { value: "DEVS" } });
  expect(body().getByText("1 of 4 entries")).toBeTruthy();
  fireEvent.change(box, { target: { value: "cluster-admin" } });
  expect(body().getByText("1 of 4 entries")).toBeTruthy();
  fireEvent.change(box, { target: { value: "dev-binding" } });
  expect(body().getByText("2 of 4 entries")).toBeTruthy();
});

test("clicking a subject lists its effective permissions, clicking again hides them", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  expect(body().queryByText(/Effective Permissions/)).toBeNull();
  fireEvent.click(rowOf("devs"));
  expect(body().getByText("Effective Permissions: devs")).toBeTruthy();
  expect(body().getByText("pods,pods/log")).toBeTruthy();
  expect(body().getByText("deployments")).toBeTruthy();
  expect(
    body().getAllByText("Role: developer (via dev-binding in default)").length,
  ).toBe(2);
  fireEvent.click(rowOf("devs"));
  expect(body().queryByText(/Effective Permissions/)).toBeNull();
});

test("clicking another subject switches the panel", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  fireEvent.click(rowOf("devs"));
  const aliceRow = body()
    .getAllByRole("cell", { name: "alice" })
    .map((c: HTMLElement) => c.closest("tr") as HTMLElement)[0];
  fireEvent.click(aliceRow);
  expect(body().getByText("Effective Permissions: alice")).toBeTruthy();
});

test("clicking a link in a row does not select the subject", async () => {
  stubApi();
  render(<RBACOverview />);
  await settle();
  const link = rowOf("devs").querySelector("a") as HTMLAnchorElement;
  link.addEventListener("click", (e) => e.preventDefault());
  fireEvent.click(link);
  expect(body().queryByText(/Effective Permissions/)).toBeNull();
});

test("a failed load shows the error and no table", async () => {
  stubApi(true);
  render(<RBACOverview />);
  await settle();
  expect(body().getByText("Failed to load RBAC data")).toBeTruthy();
  expect(body().queryByRole("table")).toBeNull();
});

test("renders nothing and fetches nothing on the server", async () => {
  stubApi();
  setBrowser(false);
  try {
    const { container } = render(<RBACOverview />);
    await act(async () => {});
    expect(container.innerHTML).toBe("");
    expect(urls).toEqual([]);
  } finally {
    setBrowser(true);
  }
});
