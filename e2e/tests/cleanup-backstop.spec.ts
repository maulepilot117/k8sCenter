import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { CleanupBackstop, deleteAccount } from "../helpers.ts";

/**
 * The CleanupBackstop contract (e2e/helpers.ts), exercised without a browser
 * or cluster: every test here uses fakes and takes no `page` fixture, so it
 * runs in the same Playwright project as the rest of the suite for free.
 */

/** A stand-in for the admin page: just enough for getAuthHeaders. */
const fakePage = {
  evaluate: async () => "token",
  request: {},
} as unknown as Page;

/** Stands in for playwright.request.newContext. */
const fakePlaywright = {
  request: { newContext: async () => ({ dispose: async () => {} }) },
} as never;

test.describe("CleanupBackstop", () => {
  test("a completed action marks the entry done and drain skips it", async () => {
    const backstop = new CleanupBackstop();
    let runs = 0;
    const cleanup = backstop.track(() => {
      runs++;
    }, "ok");
    await backstop.adopt(fakePage);
    await cleanup();
    expect(backstop.pending).toEqual([]);
    await backstop.drain(fakePlaywright, "http://x");
    expect(runs).toBe(1);
  });

  test("a thrown action leaves the entry pending and drain retries it", async () => {
    const backstop = new CleanupBackstop();
    let calls = 0;
    const cleanup = backstop.track(() => {
      calls++;
      // First call is the in-test run on a dead page; the retry succeeds.
      if (calls === 1) throw new Error("page closed");
    }, "dead page");
    await backstop.adopt(fakePage);
    await expect(cleanup()).rejects.toThrow("page closed");
    expect(backstop.pending).toEqual(["dead page"]);
    await backstop.drain(fakePlaywright, "http://x");
    expect(calls).toBe(2);
    expect(backstop.pending).toEqual([]);
  });

  test("a cleanup that never ran is run by drain", async () => {
    const backstop = new CleanupBackstop();
    let ran = false;
    backstop.track(() => {
      ran = true;
    }, "timed out");
    await backstop.adopt(fakePage);
    await backstop.drain(fakePlaywright, "http://x");
    expect(ran).toBe(true);
  });

  test("a 404 counts as done in deleteAccount", async () => {
    const api = {
      headers: {},
      request: {
        delete: async () => ({ ok: () => false, status: () => 404 }),
      },
    } as never;
    await deleteAccount(api, "gone", "ghost");
    const failing = {
      headers: {},
      request: {
        delete: async () => ({ ok: () => false, status: () => 500 }),
      },
    } as never;
    await expect(deleteAccount(failing, "id", "user")).rejects.toThrow(
      "could not delete user (id): 500",
    );
  });

  test("drain throws listing every leaked label", async () => {
    const backstop = new CleanupBackstop();
    backstop.track(() => {
      throw new Error("401");
    }, "incident");
    backstop.track(() => {}, "fine");
    backstop.track(() => {
      throw new Error("401");
    }, "canary pod");
    await backstop.adopt(fakePage);
    await expect(backstop.drain(fakePlaywright, "http://x")).rejects.toThrow(
      /leaked: incident \(.*401.*\); canary pod/,
    );
    expect(backstop.pending).toEqual(["incident", "canary pod"]);
  });

  test("drain with work but no captured identity fails naming the entries", async () => {
    const backstop = new CleanupBackstop();
    backstop.track(() => {}, "orphan");
    await expect(backstop.drain(fakePlaywright, "http://x")).rejects.toThrow(
      /no admin identity captured; leaked: orphan/,
    );
  });

  test("the in-test cleanup without adopt() throws instead of reporting success", async () => {
    const backstop = new CleanupBackstop();
    const cleanup = backstop.track(() => {}, "x");
    await expect(cleanup()).rejects.toThrow("adopt(page) was never called");
    expect(backstop.pending).toEqual(["x"]);
  });
});
