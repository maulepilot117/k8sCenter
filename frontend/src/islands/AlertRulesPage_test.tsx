/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * Under a remote selection the rule list is the remote cluster's
 * PrometheusRule objects, but the firing and history feeds stay on the local
 * Alertmanager (R-8 KTD13, R14). The page must say so, or an operator reads
 * a quiet local feed as proof the remote rules are not firing.
 *
 * The page also lists the rules, opens an editor to create or edit one (the
 * JSON is parsed before it is sent), and deletes after a confirm prompt.
 */
const bunFetch = globalThis.fetch;
const bunConfirm = globalThis.confirm;
setupDom({ browser: true });

const { default: AlertRulesPage } = await import("./AlertRulesPage.tsx");

interface Call {
  method: string;
  url: string;
  body: unknown;
}
let calls: Call[] = [];
let rules: unknown[] = [];
let failWrites = false;
let answerConfirm = true;
let asked: string[] = [];

const reply = (data: unknown, status = 200) =>
  new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const rule = (name: string, namespace = "monitoring") => ({
  name,
  namespace,
  rulesCount: 3,
  createdAt: "2026-11-01T00:00:00Z",
  managedBy: "kubecenter",
});

beforeEach(() => {
  calls = [];
  rules = [];
  failWrites = false;
  answerConfirm = true;
  asked = [];
  globalThis.confirm = ((message: string) => {
    asked.push(message);
    return answerConfirm;
  }) as typeof globalThis.confirm;
  setAccessToken("test-token");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const call: Call = {
      method: init?.method ?? "GET",
      url: String(input),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    };
    calls.push(call);
    if (call.method !== "GET") {
      return failWrites
        ? new Response(JSON.stringify({ error: { message: "denied" } }), {
            status: 500,
          })
        : reply({});
    }
    if (call.url.endsWith("/v1/alerts/rules")) return reply(rules);
    return reply({ kind: "PrometheusRule", metadata: { name: "one" } });
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  globalThis.confirm = bunConfirm;
  setAccessToken(null);
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

async function mount() {
  const view = render(<AlertRulesPage />);
  await settle();
  return view.container as HTMLElement;
}

const click = async (el: Element) => {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
};
const writes = () => calls.filter((c) => c.method !== "GET");
const editor = () => document.querySelector("textarea") as HTMLTextAreaElement;

const NOTICE = "local cluster's Alertmanager";

test("a remote selection says firing alerts come from the local Alertmanager", async () => {
  switchCluster("remote-1", "gen-1");
  const root = await mount();
  expect(root.textContent).toContain(NOTICE);
});

test("the local cluster shows no remote notice", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  const root = await mount();
  expect(root.textContent).not.toContain(NOTICE);
});

test("no rules shows the empty state", async () => {
  const root = await mount();
  expect(root.textContent).toContain("No alert rules");
});

test("lists each rule with its namespace and rule count", async () => {
  rules = [rule("cpu"), rule("mem", "other")];
  const root = await mount();
  const rows = [...root.querySelectorAll("tbody tr")];
  expect(rows).toHaveLength(2);
  expect(rows[0].textContent).toContain("cpu");
  expect(rows[0].textContent).toContain("monitoring");
  expect(rows[0].textContent).toContain("3");
  expect(rows[1].textContent).toContain("other");
});

test("Create Rule opens the editor on the example rule, and Cancel returns to the list", async () => {
  rules = [rule("cpu")];
  const root = await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  expect(root.textContent).toContain("Create Alert Rule");
  expect(editor().value).toContain("kubecenter-example");
  expect(body().getByDisplayValue("default")).toBeTruthy();
  await click(body().getAllByRole("button", { name: "Cancel" })[0]);
  expect(root.textContent).toContain("cpu");
  expect(root.textContent).not.toContain("Create Alert Rule");
});

test("saving a new rule posts the namespace and the parsed content, then returns to the list", async () => {
  const root = await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  await act(async () => {
    fireEvent.change(body().getByDisplayValue("default"), {
      target: { value: "team-a" },
    });
    fireEvent.change(editor(), {
      target: { value: '{"kind":"PrometheusRule"}' },
    });
  });
  await click(body().getByRole("button", { name: "Save" }));
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("POST");
  expect(writes()[0].url).toContain("/v1/alerts/rules");
  expect(writes()[0].body).toEqual({
    namespace: "team-a",
    content: { kind: "PrometheusRule" },
  });
  expect(root.textContent).not.toContain("Create Alert Rule");
  expect(
    calls.filter((c) => c.method === "GET" && c.url.endsWith("/alerts/rules")),
  ).toHaveLength(2);
});

test("invalid JSON shows an error and sends nothing", async () => {
  const root = await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  await act(async () => {
    fireEvent.change(editor(), { target: { value: "{nope" } });
  });
  await click(body().getByRole("button", { name: "Save" }));
  expect(root.textContent).toContain("Invalid JSON content");
  expect(writes()).toHaveLength(0);
  expect(
    (body().getByRole("button", { name: "Save" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
});

test("a failed save keeps the editor open with the error", async () => {
  failWrites = true;
  const root = await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  await click(body().getByRole("button", { name: "Save" }));
  expect(root.textContent).toContain("Create Alert Rule");
  expect(root.textContent).toContain("denied");
});

test("Edit loads the rule into the editor and Save puts it back", async () => {
  rules = [rule("cpu")];
  const root = await mount();
  await click(body().getByRole("button", { name: "Edit" }));
  expect(
    calls.some((c) => c.url.endsWith("/v1/alerts/rules/monitoring/cpu")),
  ).toBe(true);
  expect(root.textContent).toContain("Edit monitoring/cpu");
  expect(editor().value).toContain('"name": "one"');
  expect(body().queryByDisplayValue("default")).toBeNull();
  await click(body().getByRole("button", { name: "Save" }));
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("PUT");
  expect(writes()[0].url).toContain("/v1/alerts/rules/monitoring/cpu");
  expect(writes()[0].body).toEqual({
    kind: "PrometheusRule",
    metadata: { name: "one" },
  });
});

test("Delete asks first; declining sends nothing, accepting deletes and reloads", async () => {
  rules = [rule("cpu")];
  await mount();
  answerConfirm = false;
  await click(body().getByRole("button", { name: "Delete" }));
  expect(asked).toEqual(["Delete PrometheusRule monitoring/cpu?"]);
  expect(writes()).toHaveLength(0);

  answerConfirm = true;
  await click(body().getByRole("button", { name: "Delete" }));
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("DELETE");
  expect(writes()[0].url).toContain("/v1/alerts/rules/monitoring/cpu");
  expect(
    calls.filter((c) => c.method === "GET" && c.url.endsWith("/alerts/rules")),
  ).toHaveLength(2);
});
