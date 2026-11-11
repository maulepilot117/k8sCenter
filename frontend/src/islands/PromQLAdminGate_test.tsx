import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { fetchCurrentUser, logout } from "@/lib/auth.ts";
import { act, json, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The raw PromQL editor is for administrators. While the user is unknown the
 * gate shows a loading shell; a non-admin gets an explanation instead of the
 * editor; an admin gets the wrapped editor.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: PromQLAdminGate } = await import("./PromQLAdminGate.tsx");

afterEach(async () => {
  globalThis.fetch = (async () =>
    json({})) as unknown as typeof globalThis.fetch;
  await act(async () => {
    await logout();
  });
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

async function signIn(roles: string[]) {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    json({
      user: {
        id: "u1",
        username: "u",
        provider: "local",
        kubernetesUsername: "u",
        kubernetesGroups: [],
        roles,
      },
      rbac: {},
    })) as unknown as typeof globalThis.fetch;
  await act(async () => {
    await fetchCurrentUser();
  });
}

const child = <div>query editor</div>;

test("shows a loading shell and hides the editor while the user is unknown", () => {
  const { container } = render(<PromQLAdminGate>{child}</PromQLAdminGate>);
  expect(container.textContent).toContain("Loading");
  expect(container.textContent).not.toContain("query editor");
});

test("hides the editor from a non-admin and explains why", async () => {
  await signIn(["viewer"]);
  const { container } = render(<PromQLAdminGate>{child}</PromQLAdminGate>);
  expect(container.textContent).toContain("Admin access required");
  expect(container.textContent).not.toContain("query editor");
  expect(container.querySelector('a[href="/"]')).not.toBeNull();
});

test("renders the editor for an admin", async () => {
  await signIn(["admin"]);
  const { container } = render(<PromQLAdminGate>{child}</PromQLAdminGate>);
  expect(container.textContent).toContain("query editor");
  expect(container.textContent).not.toContain("Admin access required");
});

test("swaps the loading shell for the editor when an admin loads after mount", async () => {
  const { container } = render(<PromQLAdminGate>{child}</PromQLAdminGate>);
  expect(container.textContent).toContain("Loading");
  await signIn(["admin"]);
  expect(container.textContent).toContain("query editor");
});
