import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The rules list shows each rule's filters, channel and enabled state; the
 * modal creates and edits rules; the On/Off pill toggles one rule and ignores
 * a second press while the first is in flight; and deleting asks to confirm
 * in the row first.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: NotificationRules } = await import("./NotificationRules.tsx");

interface Call {
  method: string;
  url: string;
  body: unknown;
}
let calls: Call[] = [];
let rules: unknown[] = [];
let channels: unknown[] = [];
let rulesFail = false;
let handler: (call: Call) => Promise<Response> | null = () => null;

const rule = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  name: `rule-${id}`,
  sourceFilter: [],
  severityFilter: ["critical"],
  channelId: "c1",
  channelName: "ops-slack",
  enabled: true,
  createdBy: "admin",
  createdAt: "2026-11-01T00:00:00Z",
  ...over,
});
const channel = (id: string, name: string) => ({
  id,
  name,
  type: "slack",
  config: {},
  createdBy: "admin",
  createdAt: "2026-11-01T00:00:00Z",
});

const reply = (data: unknown, status = 200) =>
  new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function stubApi() {
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
    const held = handler(call);
    if (held) return held;
    if (call.method === "GET" && call.url.includes("/notifications/rules")) {
      return rulesFail ? reply(null, 500) : reply(rules);
    }
    if (call.method === "GET" && call.url.includes("/notifications/channels")) {
      return reply(channels);
    }
    return reply({ id: "new" });
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  rules = [];
  channels = [channel("c1", "ops-slack"), channel("c2", "pager")];
  rulesFail = false;
  handler = () => null;
  setAccessToken("t");
  stubApi();
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

async function mount() {
  const view = render(<NotificationRules />);
  await settle();
  return view.container as HTMLElement;
}

const click = async (el: Element) => {
  await act(async () => {
    fireEvent.click(el);
  });
};
const type = async (el: Element, value: string) => {
  await act(async () => {
    fireEvent.change(el, { target: { value } });
  });
};
const writes = () => calls.filter((c) => c.method !== "GET");
const gets = (part: string) =>
  calls.filter((c) => c.method === "GET" && c.url.includes(part));

test("lists each rule with its filters, channel and state", async () => {
  rules = [
    rule("a", {
      sourceFilter: ["alert"],
      severityFilter: ["critical", "info"],
    }),
    rule("b", { sourceFilter: [], severityFilter: [], enabled: false }),
  ];
  const root = await mount();
  const rows = [...root.querySelectorAll("tbody tr")];
  expect(rows).toHaveLength(2);
  expect(rows[0].textContent).toContain("rule-a");
  expect(rows[0].textContent).toContain("Critical");
  expect(rows[0].textContent).toContain("Info");
  expect(rows[0].textContent).toContain("ops-slack");
  expect(rows[0].textContent).toContain("On");
  expect(rows[1].textContent).toContain("All");
  expect(rows[1].textContent).toContain("Off");
});

test("an empty list says notifications stay in-app", async () => {
  const root = await mount();
  expect(root.textContent).toContain("No rules configured");
});

test("a failed load shows the error banner and the empty state", async () => {
  rulesFail = true;
  const root = await mount();
  expect(root.textContent).toContain("Failed to load rules");
  expect(root.textContent).toContain("Routing Rules");
});

test("Create Rule opens the modal with the first channel selected and Create disabled until named", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  const select = body().getByDisplayValue("ops-slack (slack)");
  expect((select as HTMLSelectElement).value).toBe("c1");
  const create = body().getByRole("button", {
    name: "Create",
  }) as HTMLButtonElement;
  expect(create.disabled).toBe(true);
  await type(
    body().getByPlaceholderText("e.g. Critical alerts to Slack"),
    "my rule",
  );
  expect(create.disabled).toBe(false);
});

test("creating a rule posts the name, ticked filters and channel, then reloads", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  await type(
    body().getByPlaceholderText("e.g. Critical alerts to Slack"),
    "  paging  ",
  );
  await click(body().getByLabelText("Critical"));
  await click(body().getByLabelText("Warning"));
  await click(body().getByLabelText("Warning"));
  await click(body().getByLabelText("Enabled"));
  await type(body().getByDisplayValue("ops-slack (slack)"), "c2");
  await click(body().getByRole("button", { name: "Create" }));
  await settle();
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("POST");
  expect(writes()[0].body).toEqual({
    name: "paging",
    sourceFilter: [],
    severityFilter: ["critical"],
    channelId: "c2",
    enabled: false,
  });
  expect(gets("/notifications/rules")).toHaveLength(2);
  expect(body().queryByText("Create Rule", { selector: "h3" })).toBeNull();
});

test("editing prefills the form and saves with a PUT", async () => {
  rules = [rule("a", { sourceFilter: ["alert"] })];
  await mount();
  await click(body().getByRole("button", { name: "Edit" }));
  expect(body().getByText("Edit Rule")).toBeTruthy();
  const name = body().getByPlaceholderText(
    "e.g. Critical alerts to Slack",
  ) as HTMLInputElement;
  expect(name.value).toBe("rule-a");
  expect((body().getByLabelText("Critical") as HTMLInputElement).checked).toBe(
    true,
  );
  await type(name, "renamed");
  await click(body().getByRole("button", { name: "Update" }));
  await settle();
  expect(writes()[0].method).toBe("PUT");
  expect(writes()[0].url).toContain("/notifications/rules/a");
  expect(writes()[0].body).toMatchObject({
    name: "renamed",
    sourceFilter: ["alert"],
    severityFilter: ["critical"],
    channelId: "c1",
    enabled: true,
  });
});

test("a failed save keeps the modal open and shows the error", async () => {
  await mount();
  handler = (c) =>
    c.method === "POST" ? Promise.resolve(reply(null, 500)) : null;
  await click(body().getByRole("button", { name: "Create Rule" }));
  await type(body().getByPlaceholderText("e.g. Critical alerts to Slack"), "x");
  await click(body().getByRole("button", { name: "Create" }));
  await settle();
  expect(document.body.textContent).toContain("Failed to save rule");
  expect(body().getByText("Create Rule", { selector: "h3" })).toBeTruthy();
});

test("clicking the backdrop closes the modal, Cancel too", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Rule" }));
  await click(body().getByRole("button", { name: "Cancel" }));
  expect(body().queryByText("Create Rule", { selector: "h3" })).toBeNull();
  await click(body().getByRole("button", { name: "Create Rule" }));
  const backdrop = document.querySelector(".glass-scrim") as HTMLElement;
  await click(backdrop);
  expect(body().queryByText("Create Rule", { selector: "h3" })).toBeNull();
});

test("the On/Off pill flips one rule, and a second press while it is in flight is ignored", async () => {
  rules = [rule("a"), rule("b")];
  const release: Array<(r: Response) => void> = [];
  handler = (c) =>
    c.method === "PUT"
      ? new Promise<Response>((resolve) => {
          release.push(resolve);
        })
      : null;
  await mount();
  const [firstPill, secondPill] = body().getAllByRole("button", { name: "On" });
  await act(async () => {
    fireEvent.click(firstPill);
    fireEvent.click(firstPill);
  });
  expect(writes()).toHaveLength(1);
  expect(writes()[0].url).toContain("/notifications/rules/a");
  expect(writes()[0].body).toMatchObject({ name: "rule-a", enabled: false });

  // A different rule is not blocked by the first one's guard.
  await click(secondPill);
  expect(writes()).toHaveLength(2);
  expect(writes()[1].url).toContain("/notifications/rules/b");

  await act(async () => {
    for (const done of release) done(reply(null));
    await new Promise((r) => setTimeout(r, 0));
  });
  await settle();
  // Once finished, the first rule can be toggled again.
  handler = () => null;
  await click(body().getAllByRole("button", { name: "On" })[0]);
  await settle();
  expect(writes().filter((w) => w.url.endsWith("/rules/a"))).toHaveLength(2);
});

test("Delete asks to confirm in the row, Cancel backs out, Confirm sends one DELETE and reloads", async () => {
  rules = [rule("a"), rule("b")];
  await mount();
  const deletes = body().getAllByRole("button", { name: "Delete" });
  await click(deletes[0]);
  expect(body().getAllByRole("button", { name: "Confirm" })).toHaveLength(1);
  await click(body().getByRole("button", { name: "Cancel" }));
  expect(body().queryByRole("button", { name: "Confirm" })).toBeNull();
  expect(writes()).toHaveLength(0);

  await click(body().getAllByRole("button", { name: "Delete" })[1]);
  await click(body().getByRole("button", { name: "Confirm" }));
  await settle();
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("DELETE");
  expect(writes()[0].url).toContain("/notifications/rules/b");
  expect(gets("/notifications/rules")).toHaveLength(2);
  expect(body().queryByRole("button", { name: "Confirm" })).toBeNull();
});
