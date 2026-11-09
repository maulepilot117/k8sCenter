/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * usePoll keeps the status and reason of a failed fetch, and treats a 403 as
 * final: it stops polling and does not resume when the tab becomes visible
 * again (#608 R5). Other failures keep today's pause-and-resume behaviour.
 */
setupDom({ browser: true });

const { usePoll } = await import("./use-poll.ts");
const { setAccessToken } = await import("@/lib/api.ts");

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
});

function errorResponse(status: number, reason: string): Response {
  return new Response(
    JSON.stringify({ error: { code: status, message: "nope", reason } }),
    { status, headers: { "Content-Type": "application/json" } },
  );
}

let latest: ReturnType<typeof usePoll<{ ok: boolean }>> | null = null;

function Probe({
  url = "/v1/networking/cilium/bgp",
  stopWhenNotOk = false,
}: {
  url?: string;
  stopWhenNotOk?: boolean;
}) {
  latest = usePoll<{ ok: boolean }>(url, {
    interval: INTERVAL,
    // A fresh function every render: only its latest value may matter.
    shouldContinuePolling: stopWhenNotOk ? (d) => d.ok : undefined,
  });
  return null;
}

const okResponse = (ok = true) =>
  new Response(JSON.stringify({ data: { ok } }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

async function mount() {
  await act(async () => {
    render(<Probe />);
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
  expect(latest?.failure).toEqual({ status: 403, reason: "forbidden" });
  const afterFirst = calls;
  expect(afterFirst).toBe(1);

  await act(() => wait(INTERVAL * 4));
  await becomeVisible();
  expect(calls).toBe(afterFirst);
});

test("other failures keep polling and resume on visibility", async () => {
  respond = () => errorResponse(502, "unreachable");
  await mount();
  expect(latest?.failure).toEqual({ status: 502, reason: "unreachable" });

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
  expect(latest?.failure?.status).toBe(502);

  fail = false;
  await act(() => wait(INTERVAL * 2));
  expect(latest?.failure).toBeNull();
  expect(latest?.data).toEqual({ ok: true });
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

test("polling stops when shouldContinuePolling returns false", async () => {
  respond = () => okResponse(false);
  await act(async () => {
    render(<Probe stopWhenNotOk />);
  });
  await act(() => wait(10));
  expect(calls).toBe(1);
  expect(latest?.data).toEqual({ ok: false });

  await act(() => wait(INTERVAL * 5));
  await becomeVisible();
  expect(calls).toBe(1);
});

test("ticks are skipped while the tab is hidden", async () => {
  respond = () => okResponse();
  const hidden = Object.getOwnPropertyDescriptor(document, "hidden");
  Object.defineProperty(document, "hidden", {
    configurable: true,
    get: () => true,
  });
  try {
    await mount();
    expect(calls).toBe(1);
    await act(() => wait(INTERVAL * 5));
    expect(calls).toBe(1);
  } finally {
    if (hidden) Object.defineProperty(document, "hidden", hidden);
    else Reflect.deleteProperty(document, "hidden");
  }
  await act(() => wait(INTERVAL * 3));
  expect(calls).toBeGreaterThan(1);
});

test("a new url starts from a clean slate after a 403", async () => {
  respond = () => errorResponse(403, "forbidden");
  let view: ReturnType<typeof render> | undefined;
  await act(async () => {
    view = render(<Probe url="/v1/a" />);
  });
  await act(() => wait(INTERVAL * 4));
  expect(calls).toBe(1);

  respond = () => okResponse();
  await act(async () => {
    view?.rerender(<Probe url="/v1/b" />);
  });
  await act(() => wait(INTERVAL * 4));
  expect(latest?.data).toEqual({ ok: true });
  expect(calls).toBeGreaterThan(2);
});

test("the interval does not restart when only shouldContinuePolling changes", async () => {
  respond = () => okResponse();
  let view: ReturnType<typeof render> | undefined;
  await act(async () => {
    view = render(<Probe stopWhenNotOk key="same" />);
  });
  expect(calls).toBe(1);
  // Re-render with a new shouldContinuePolling, back to back. A restart
  // fetches at once on every render, so eight renders would make at least
  // nine fetches; without one, only an interval tick can add a fetch.
  for (let i = 0; i < 8; i++) {
    await act(async () => {
      view?.rerender(<Probe stopWhenNotOk key="same" />);
    });
  }
  expect(calls).toBeLessThanOrEqual(3);
});
