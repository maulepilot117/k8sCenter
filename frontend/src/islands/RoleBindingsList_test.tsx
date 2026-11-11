import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  json,
  render,
  setBrowser,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * On a Role or ClusterRole detail page, lists the bindings that reference it.
 * A Role is looked up in its own namespace's bindings; a ClusterRole in both
 * binding lists. A failed load shows the empty-state message.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: RoleBindingsList } = await import("./RoleBindingsList.tsx");

let urls: string[] = [];
let respond: (url: string) => Response = () => json([]);

beforeEach(() => {
  urls = [];
  respond = () => json([]);
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    urls.push(String(input));
    return respond(String(input));
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

test("a Role lists same-namespace bindings that reference it", async () => {
  respond = () =>
    json([
      {
        metadata: { name: "dev-binding", namespace: "team" },
        roleRef: { kind: "Role", name: "developer" },
        subjects: [
          { kind: "User", name: "alice" },
          { kind: "Group", name: "devs" },
        ],
      },
      {
        metadata: { name: "other", namespace: "team" },
        roleRef: { kind: "Role", name: "viewer" },
      },
      {
        metadata: { name: "wrong-kind", namespace: "team" },
        roleRef: { kind: "ClusterRole", name: "developer" },
      },
    ]);
  render(
    <RoleBindingsList roleName="developer" roleKind="Role" namespace="team" />,
  );
  await settle();
  expect(urls).toEqual(["/api/v1/resources/rolebindings/team?limit=500"]);
  expect(body().getByText("Bindings Using This Role (1)")).toBeTruthy();
  const link = body().getByRole("link", { name: "dev-binding" });
  expect(link.getAttribute("href")).toBe("/rbac/rolebindings/team/dev-binding");
  const row = link.closest("tr") as HTMLElement;
  expect(row.textContent).toContain("(team)");
  expect(row.textContent).toContain("RoleBinding");
  expect(row.textContent).toContain("User:alice,Group:devs");
  expect(body().queryByText("other")).toBeNull();
});

test("a ClusterRole reads role and cluster bindings and links each by scope", async () => {
  respond = (url) =>
    url.includes("/clusterrolebindings")
      ? json([
          {
            metadata: { name: "crb" },
            roleRef: { kind: "ClusterRole", name: "admin" },
          },
        ])
      : json([
          {
            metadata: { name: "rb", namespace: "ns1" },
            roleRef: { kind: "ClusterRole", name: "admin" },
            subjects: [{ kind: "User", name: "bob" }],
          },
        ]);
  render(<RoleBindingsList roleName="admin" roleKind="ClusterRole" />);
  await settle();
  expect([...urls].sort()).toEqual([
    "/api/v1/resources/clusterrolebindings?limit=500",
    "/api/v1/resources/rolebindings?limit=500",
  ]);
  expect(body().getByText("Bindings Using This ClusterRole (2)")).toBeTruthy();
  expect(body().getByRole("link", { name: "crb" }).getAttribute("href")).toBe(
    "/rbac/clusterrolebindings/crb",
  );
  const crbRow = body().getByRole("link", { name: "crb" }).closest("tr");
  expect(crbRow?.textContent).toContain("ClusterRoleBinding");
  expect(crbRow?.textContent).toContain("-");
  expect(body().getByRole("link", { name: "rb" }).getAttribute("href")).toBe(
    "/rbac/rolebindings/ns1/rb",
  );
});

test("says so when no binding references the role", async () => {
  render(<RoleBindingsList roleName="x" roleKind="ClusterRole" />);
  await settle();
  expect(
    body().getByText("No bindings reference this clusterrole."),
  ).toBeTruthy();
  expect(body().getByText("Bindings Using This ClusterRole")).toBeTruthy();
});

test("shows nothing while loading", () => {
  globalThis.fetch = (() =>
    new Promise<Response>(() => {})) as unknown as typeof globalThis.fetch;
  const { container } = render(
    <RoleBindingsList roleName="x" roleKind="Role" namespace="n" />,
  );
  expect(container.innerHTML).toBe("");
});

test("a failed load shows the empty-state message", async () => {
  respond = () => json({}, 500);
  render(<RoleBindingsList roleName="x" roleKind="Role" namespace="n" />);
  await settle();
  expect(body().getByText("No bindings reference this role.")).toBeTruthy();
});

test("refetches when the role changes", async () => {
  const view = render(
    <RoleBindingsList roleName="a" roleKind="Role" namespace="n1" />,
  );
  await settle();
  view.rerender(
    <RoleBindingsList roleName="a" roleKind="Role" namespace="n2" />,
  );
  await settle();
  expect(urls).toEqual([
    "/api/v1/resources/rolebindings/n1?limit=500",
    "/api/v1/resources/rolebindings/n2?limit=500",
  ]);
});

test("renders nothing and fetches nothing on the server", async () => {
  setBrowser(false);
  try {
    const { container } = render(
      <RoleBindingsList roleName="x" roleKind="Role" namespace="n" />,
    );
    await act(async () => {});
    expect(container.innerHTML).toBe("");
    expect(urls).toEqual([]);
  } finally {
    setBrowser(true);
  }
});
