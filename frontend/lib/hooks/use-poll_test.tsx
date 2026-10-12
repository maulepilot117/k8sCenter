/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * usePoll keeps the status and reason of a failed fetch, and treats a 403 as
 * final: it stops polling and does not resume when the tab becomes visible
 * again (#608 R5). Other failures keep today's pause-and-resume behaviour.
 */
GlobalRegistrator.register({ url: "http://localhost/" });

const { usePoll } = await import("./use-poll.ts");
const { setAccessToken } = await import("@/lib/api.ts");

afterAll(() => {
  GlobalRegistrator.unregister();
});

const INTERVAL = 20;
let calls = 0;
let respond: () => Response;
const realFetch = globalThis.fetch;

beforeEach(() => {
  calls = 0;
  setAccessToken("test-token");
  globalThis.fetch = (async () => {
    calls++;
    return respond();
  }) as unknown as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  render(null, document.body);
});

function errorResponse(status: number, reason: string): Response {
  return new Response(
    JSON.stringify({ error: { code: status, message: "nope", reason } }),
    { status, headers: { "Content-Type": "application/json" } },
  );
}

let latest: ReturnType<typeof usePoll<{ ok: boolean }>> | null = null;

function Probe() {
  latest = usePoll<{ ok: boolean }>("/v1/networking/cilium/bgp", {
    interval: INTERVAL,
  });
  return null;
}

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

async function mount() {
  await act(async () => {
    render(<Probe />, document.body);
  });
  await act(() => wait(10));
}

async function becomeVisible() {
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
    await wait(10);
  });
}

test("a 403 records its reason and stops polling for good", async () => {
  respond = () => errorResponse(403, "forbidden");
  await mount();
  expect(latest?.failure.value).toEqual({ status: 403, reason: "forbidden" });
  const afterFirst = calls;
  expect(afterFirst).toBe(1);

  await act(() => wait(INTERVAL * 4));
  await becomeVisible();
  expect(calls).toBe(afterFirst);
});

test("other failures keep polling and resume on visibility", async () => {
  respond = () => errorResponse(502, "unreachable");
  await mount();
  expect(latest?.failure.value).toEqual({ status: 502, reason: "unreachable" });

  await act(() => wait(INTERVAL * 5));
  const paused = calls;
  expect(paused).toBeGreaterThanOrEqual(3);

  await becomeVisible();
  expect(calls).toBeGreaterThan(paused);
});

test("a success clears the failure", async () => {
  let fail = true;
  respond = () =>
    fail
      ? errorResponse(502, "unreachable")
      : new Response(JSON.stringify({ data: { ok: true } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
  await mount();
  expect(latest?.failure.value?.status).toBe(502);

  fail = false;
  await act(() => wait(INTERVAL * 2));
  expect(latest?.failure.value).toBeNull();
  expect(latest?.data.value).toEqual({ ok: true });
});

test("a manual refetch that succeeds restarts paused polling", async () => {
  let fail = true;
  respond = () =>
    fail
      ? errorResponse(502, "unreachable")
      : new Response(JSON.stringify({ data: { ok: true } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
  await mount();
  await act(() => wait(INTERVAL * 5));
  const paused = calls;
  await act(() => wait(INTERVAL * 3));
  expect(calls).toBe(paused);

  fail = false;
  await act(async () => {
    await latest?.refetch();
  });
  const afterRefetch = calls;
  await act(() => wait(INTERVAL * 4));
  expect(calls).toBeGreaterThan(afterRefetch);
});
