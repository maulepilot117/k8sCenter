import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { toasts } from "@/src/lib/toast-store.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The channel list shows each channel's type, status and config; the modal
 * creates and edits slack, email and webhook channels with client-side URL
 * checks; Test posts to the channel and shows a tick or a cross per row; and
 * deleting needs the channel's name typed first.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: NotificationChannels } = await import(
  "./NotificationChannels.tsx"
);

interface Call {
  method: string;
  url: string;
  body: unknown;
}
let calls: Call[] = [];
let channels: unknown[] = [];
let listFails = false;
/** Per-request hook so a test can hold or fail a request. */
let handler: (call: Call) => Promise<Response> | null = () => null;

const channel = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  name: `chan-${id}`,
  type: "slack",
  config: { webhookUrl: "https://hooks.slack.com/x" },
  createdBy: "admin",
  createdAt: "2026-11-01T00:00:00Z",
  ...over,
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
    if (call.method === "GET" && call.url.includes("/notifications/channels")) {
      return listFails ? reply(null, 500) : reply(channels);
    }
    return reply({ id: "new" });
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  channels = [];
  listFails = false;
  handler = () => null;
  toasts.value = [];
  setAccessToken("t");
  stubApi();
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  toasts.value = [];
});

async function mount() {
  const view = render(<NotificationChannels />);
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

test("lists each channel with its type, status and config", async () => {
  channels = [
    channel("a"),
    channel("b", {
      name: "ops-hook",
      type: "webhook",
      config: { url: "https://example.com/h" },
      lastError: "connection refused",
    }),
  ];
  const root = await mount();
  expect(root.textContent).toContain("chan-a");
  expect(root.textContent).toContain("Slack");
  expect(root.textContent).toContain("webhookUrl: https://hooks.slack.com/x");
  expect(root.textContent).toContain("OK");
  expect(root.textContent).toContain("ops-hook");
  expect(root.textContent).toContain("Webhook");
  expect(root.textContent).toContain("connection refused");
});

test("an empty list says no channels are configured", async () => {
  const root = await mount();
  expect(root.textContent).toContain("No channels configured yet.");
});

test("a failed load shows the error instead of the list", async () => {
  listFails = true;
  const root = await mount();
  expect(root.textContent).not.toContain("Notification Channels");
  expect(root.textContent?.length).toBeGreaterThan(0);
});

test("creating a slack channel posts its name and webhook URL, then reloads", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Channel" }));
  await type(body().getByPlaceholderText("my-slack-alerts"), "  alerts  ");
  await type(
    body().getByPlaceholderText("https://hooks.slack.com/services/..."),
    " https://hooks.slack.com/services/T/B/C ",
  );
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("POST");
  expect(writes()[0].url).toContain("/notifications/channels");
  expect(writes()[0].body).toEqual({
    name: "alerts",
    type: "slack",
    config: { webhookUrl: "https://hooks.slack.com/services/T/B/C" },
  });
  expect(toasts.value.map((t) => t.message)).toContain("Channel created");
  expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
  expect(body().queryByPlaceholderText("my-slack-alerts")).toBeNull();
});

test("a name and a valid URL are required before anything is sent", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Channel" }));
  await click(body().getByRole("button", { name: "Save" }));
  expect(toasts.value.at(-1)?.message).toBe("Name is required");

  await type(body().getByPlaceholderText("my-slack-alerts"), "x");
  await type(
    body().getByPlaceholderText("https://hooks.slack.com/services/..."),
    "ftp://nope",
  );
  await click(body().getByRole("button", { name: "Save" }));
  expect(toasts.value.at(-1)?.message).toBe(
    "Valid Slack webhook URL is required",
  );
  expect(writes()).toHaveLength(0);
});

test("an email channel sends the split recipients and the schedule", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Channel" }));
  await type(body().getByPlaceholderText("my-slack-alerts"), "digest");
  await click(body().getByLabelText(/Email/));
  await type(
    body().getByPlaceholderText("admin@example.com, ops@example.com"),
    "a@x.io, , b@x.io",
  );
  await type(body().getByDisplayValue("Daily digest"), "weekly");
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(writes()[0].body).toEqual({
    name: "digest",
    type: "email",
    config: { recipients: ["a@x.io", "b@x.io"], schedule: "weekly" },
  });
});

test("a webhook channel sends the secret only when one is typed", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Channel" }));
  await type(body().getByPlaceholderText("my-slack-alerts"), "hook");
  await click(body().getByLabelText(/Webhook/));
  await type(
    body().getByPlaceholderText("https://example.com/webhook"),
    "https://example.com/h",
  );
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(writes()[0].body).toEqual({
    name: "hook",
    type: "webhook",
    config: { url: "https://example.com/h" },
  });

  await click(body().getByRole("button", { name: "Create Channel" }));
  await type(body().getByPlaceholderText("my-slack-alerts"), "hook2");
  await click(body().getByLabelText(/Webhook/));
  await type(
    body().getByPlaceholderText("https://example.com/webhook"),
    "https://example.com/h",
  );
  await type(body().getByPlaceholderText("HMAC signing secret"), "s3cret");
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(writes()[1].body).toEqual({
    name: "hook2",
    type: "webhook",
    config: { url: "https://example.com/h", secret: "s3cret" },
  });
});

test("editing prefills the form and saves with a PUT", async () => {
  channels = [channel("a")];
  await mount();
  await click(body().getByRole("button", { name: "Edit" }));
  expect(body().getByText("Edit Channel")).toBeTruthy();
  const name = body().getByPlaceholderText(
    "my-slack-alerts",
  ) as HTMLInputElement;
  expect(name.value).toBe("chan-a");
  expect(
    (
      body().getByPlaceholderText(
        "https://hooks.slack.com/services/...",
      ) as HTMLInputElement
    ).value,
  ).toBe("https://hooks.slack.com/x");
  await type(name, "renamed");
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(writes()[0].method).toBe("PUT");
  expect(writes()[0].url).toContain("/notifications/channels/a");
  expect((writes()[0].body as { name: string }).name).toBe("renamed");
  expect(toasts.value.map((t) => t.message)).toContain("Channel updated");
});

test("Cancel closes the modal without sending anything", async () => {
  await mount();
  await click(body().getByRole("button", { name: "Create Channel" }));
  await click(body().getByRole("button", { name: "Cancel" }));
  expect(body().queryByPlaceholderText("my-slack-alerts")).toBeNull();
  expect(writes()).toHaveLength(0);
});

test("a failed save keeps the modal open and toasts the error", async () => {
  await mount();
  handler = (c) =>
    c.method === "POST"
      ? Promise.resolve(
          new Response(JSON.stringify({ error: { message: "nope" } }), {
            status: 500,
          }),
        )
      : null;
  await click(body().getByRole("button", { name: "Create Channel" }));
  await type(body().getByPlaceholderText("my-slack-alerts"), "x");
  await type(
    body().getByPlaceholderText("https://hooks.slack.com/services/..."),
    "https://hooks.slack.com/a",
  );
  await click(body().getByRole("button", { name: "Save" }));
  await settle();
  expect(toasts.value.at(-1)?.type).toBe("error");
  expect(body().getByPlaceholderText("my-slack-alerts")).toBeTruthy();
});

test("Test shows a tick for one channel and a cross for another, kept apart", async () => {
  channels = [channel("a"), channel("b")];
  const release: Record<string, (r: Response) => void> = {};
  handler = (c) =>
    c.method === "POST" && c.url.endsWith("/test")
      ? new Promise<Response>((resolve) => {
          release[c.url.split("/").at(-2) as string] = resolve;
        })
      : null;
  const root = await mount();
  const tests = body().getAllByRole("button", { name: "Test" });
  await click(tests[0]);
  await click(tests[1]);
  await act(async () => {
    release.b(reply({ status: "ok" }));
    await new Promise((r) => setTimeout(r, 0));
  });
  await act(async () => {
    release.a(reply(null, 500));
    await new Promise((r) => setTimeout(r, 0));
  });
  const rows = [...root.querySelectorAll("tbody tr")];
  expect(rows[0].textContent).toContain("✗");
  expect(rows[0].textContent).not.toContain("✓");
  expect(rows[1].textContent).toContain("✓");
});

test("deleting needs the channel's name typed, then sends one DELETE and reloads", async () => {
  channels = [channel("a")];
  await mount();
  await click(body().getByRole("button", { name: "Delete" }));
  const confirm = body()
    .getAllByRole("button", { name: "Delete" })
    .pop() as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  await type(body().getByPlaceholderText("chan-a"), "chan-a");
  expect(confirm.disabled).toBe(false);
  await click(confirm);
  await settle();
  const deletes = writes();
  expect(deletes).toHaveLength(1);
  expect(deletes[0].method).toBe("DELETE");
  expect(deletes[0].url).toContain("/notifications/channels/a");
  expect(toasts.value.map((t) => t.message)).toContain('Deleted "chan-a"');
  expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
});
