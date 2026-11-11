import { afterEach, expect, spyOn, test } from "bun:test";
import { ApiError } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import type { BulkRefreshJob, BulkScopeResponse } from "@/lib/eso-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The bulk-refresh dialog: it resolves the scope, asks for confirmation, runs
 * the job and polls it every two seconds until it completes. `esoApi` is
 * replaced so the test controls what each call returns, and the poll timer is
 * captured so the test fires it by hand.
 */
setupDom({ browser: true });
const { default: ESOBulkRefreshDialog } = await import(
  "./ESOBulkRefreshDialog.tsx"
);

const restores: Array<() => void> = [];
afterEach(() => {
  for (const r of restores.splice(0)) r();
});

function stub<K extends keyof typeof esoApi>(
  method: K,
  impl: (...args: never[]) => unknown,
) {
  const spy = spyOn(esoApi, method).mockImplementation(impl as never);
  restores.push(() => spy.mockRestore());
  return spy;
}

function scopeOf(visibleCount: number): { data: BulkScopeResponse } {
  return {
    data: {
      action: "refresh_store",
      scopeTarget: "apps/vault",
      totalCount: visibleCount,
      totalNamespaces: 1,
      visibleCount,
      restricted: false,
      targets: Array.from({ length: visibleCount }, (_, i) => ({
        namespace: "apps",
        name: `es-${i}`,
        uid: `uid-${i}`,
      })),
      byNamespace: [{ namespace: "apps", count: visibleCount }],
    },
  };
}

function jobOf(done: boolean, succeeded: string[] = []): BulkRefreshJob {
  return {
    jobId: "job-1",
    clusterId: "local",
    requestedBy: "u",
    action: "refresh_store",
    scopeTarget: "apps/vault",
    targetCount: 2,
    createdAt: "2026-01-01T00:00:00Z",
    completedAt: done ? "2026-01-01T00:00:05Z" : undefined,
    succeeded,
    failed: [],
    skipped: [],
  };
}

/** Captures the two-second poll timers so a test can fire them. */
function capturePollTimers() {
  const real = globalThis.setTimeout;
  const pending: Array<() => void> = [];
  globalThis.setTimeout = ((fn: () => void, ms?: number) => {
    if (ms === 2000) {
      pending.push(fn);
      return 0 as never;
    }
    return real(fn, ms);
  }) as unknown as typeof globalThis.setTimeout;
  restores.push(() => {
    globalThis.setTimeout = real;
  });
  return pending;
}

/** Lets responses land and effects run, then the work those effects start. */
const settle = async () => {
  for (let round = 0; round < 2; round++) {
    await act(async () => {
      for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
    });
  }
};

const target = { namespace: "apps", name: "vault" };

async function mount(onClose = () => {}) {
  const view = render(
    <ESOBulkRefreshDialog
      action="refresh_store"
      target={target}
      onClose={onClose}
    />,
  );
  await settle();
  return view;
}

test("a double click on Refresh starts one job, then progress polls until it completes", async () => {
  const pending = capturePollTimers();
  stub("resolveStoreScope", async () => scopeOf(2));
  const started = stub("bulkRefresh", async () => ({
    data: { jobId: "job-1" },
  }));
  let polls = 0;
  stub("getBulkRefreshJob", async () => {
    polls++;
    return { data: polls === 1 ? jobOf(false) : jobOf(true, ["a", "b"]) };
  });

  await mount();
  expect(body().getByText("will receive a force-sync.", { exact: false }));
  const button = body().getByRole("button", { name: "Refresh 2" });
  // Both clicks land before the render that follows the first one.
  act(() => {
    fireEvent.click(button);
    fireEvent.click(button);
  });
  await settle();

  expect(started.mock.calls.length).toBe(1);
  expect(started.mock.calls[0][2]).toEqual(["uid-0", "uid-1"]);
  expect(body().getByText("Refreshing…"));
  expect(body().getByRole("button", { name: "Run in background" }));
  expect(pending.length).toBe(1);

  await act(async () => pending.shift()?.());
  await settle();
  expect(body().getByText("Refresh complete"));
  expect(
    body().queryByRole("button", { name: "Run in background" }),
  ).toBeNull();
  expect(polls).toBe(2);
  expect(pending.length).toBe(0); // a completed job is not polled again
});

test("Escape closes the dialog", async () => {
  stub("resolveStoreScope", async () => scopeOf(1));
  let closed = 0;
  await mount(() => {
    closed++;
  });
  fireEvent.keyDown(document, { key: "Escape" });
  expect(closed).toBe(1);
});

test("a 409 active_job_exists attaches to the running job", async () => {
  const pending = capturePollTimers();
  stub("resolveStoreScope", async () => scopeOf(1));
  stub("bulkRefresh", async () => {
    throw new ApiError(409, 409, "busy", {
      error: {
        code: 409,
        reason: "active_job_exists",
        extra: { jobId: "job-existing" },
      },
    });
  });
  const polled: string[] = [];
  stub("getBulkRefreshJob", async (id: string) => {
    polled.push(id);
    return { data: jobOf(false) };
  });

  await mount();
  fireEvent.click(body().getByRole("button", { name: "Refresh 1" }));
  await settle();

  expect(polled).toEqual(["job-existing"]);
  expect(body().getByText("Refreshing…"));
  expect(pending.length).toBe(1);
});

test("a 409 scope_changed re-resolves the scope and asks again", async () => {
  let resolved = 0;
  stub("resolveStoreScope", async () => scopeOf(++resolved === 1 ? 2 : 3));
  stub("bulkRefresh", async () => {
    throw new ApiError(409, 409, "changed", {
      error: { code: 409, reason: "scope_changed" },
    });
  });

  await mount();
  fireEvent.click(body().getByRole("button", { name: "Refresh 2" }));
  await settle();

  expect(resolved).toBe(2);
  expect(body().getByRole("button", { name: "Refresh 3" }));
});

test("closing the dialog while a poll is in flight schedules no further poll", async () => {
  const pending = capturePollTimers();
  stub("resolveStoreScope", async () => scopeOf(1));
  stub("bulkRefresh", async () => ({ data: { jobId: "job-1" } }));
  let release: (v: { data: BulkRefreshJob }) => void = () => {};
  let calls = 0;
  stub("getBulkRefreshJob", () => {
    calls++;
    return new Promise((res) => {
      release = res;
    });
  });

  const view = await mount();
  fireEvent.click(body().getByRole("button", { name: "Refresh 1" }));
  await settle();
  expect(calls).toBe(1);

  act(() => view.unmount());
  await act(async () => release({ data: jobOf(false) }));
  await settle();
  expect(pending.length).toBe(0);
});
