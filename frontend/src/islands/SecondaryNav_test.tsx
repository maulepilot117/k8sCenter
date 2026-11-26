import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import type { RubrikStatus } from "@/lib/rubrik-types.ts";
import { hydrate } from "@/src/test-utils/hydrate.tsx";
import { body, render, settle, setupDom } from "@/src/test-utils/render.tsx";

/**
 * Nav gating for provider entries (`requires: "rubrik"`): hidden until the
 * Rubrik status store says the provider is on, hidden on the server and the
 * first client render, and left out of `flattenGroups`. fetch is routed by
 * URL; the status reply is set per test.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: SecondaryNav } = await import("./SecondaryNav.tsx");
const { DOMAIN_SECTIONS, flattenGroups } = await import("@/lib/constants.ts");
const {
  loadRubrikStatus,
  rubrikStatus,
  reloadRubrikStatus,
  resetRubrikStatusForTest,
} = await import("@/src/lib/rubrik-status.ts");
const { selectedCluster, switchCluster } = await import("@/src/lib/cluster.ts");

const STATUS = "/api/v1/rubrik/status";

const backup = DOMAIN_SECTIONS.find((s) => s.id === "backup");
if (!backup?.groups?.[0]) throw new Error("backup section has no group");
const backupGroup = backup.groups[0];
// The Backup group's entries that always show; its Rubrik entry is gated.
const plainBackupHrefs = backupGroup.items
  .filter((it) => !it.requires)
  .map((it) => it.href);
if (
  !backupGroup.items.some(
    (it) => it.href === "/backup/rubrik" && it.requires === "rubrik",
  )
) {
  throw new Error("the Backup group has no gated Rubrik entry");
}

function status(): RubrikStatus {
  return {
    enabled: true,
    preview: true,
    writesEnabled: false,
    cluster: { id: "local", state: "not_set_up" },
  };
}

const reply = (code: number, payload: unknown): Response =>
  new Response(JSON.stringify(payload), {
    status: code,
    headers: { "Content-Type": "application/json" },
  });

let statusReply: () => Response = () => reply(200, { data: status() });
let statusCalls: { cluster: string | null }[] = [];

beforeEach(() => {
  resetRubrikStatusForTest();
  statusCalls = [];
  statusReply = () => reply(200, { data: status() });
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    if (url === STATUS) {
      const headers = new Headers(init?.headers);
      statusCalls.push({ cluster: headers.get("X-Cluster-ID") });
      return statusReply();
    }
    // Pins and anything else the chrome asks for: an empty list.
    return reply(200, { data: [] });
  }) as unknown as typeof globalThis.fetch;
});

afterEach(() => {
  resetRubrikStatusForTest();
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

const rubrikLink = () =>
  document.body.querySelector('a[href="/backup/rubrik"]');
const hrefs = () =>
  body()
    .queryAllByRole("link")
    .map((a: HTMLElement) => a.getAttribute("href"));

test("a 404 status hides requires entries and reports disabled", async () => {
  statusReply = () =>
    reply(404, { error: { code: 404, message: "404 page not found" } });
  const { unmount } = render(<SecondaryNav currentPath="/backup/backups" />);
  await settle();
  expect(statusCalls.length).toBe(1);
  expect(rubrikStatus.value.kind).toBe("disabled");
  expect(rubrikLink()).toBeNull();
  // Entries without `requires` render as before, and the count leaves the
  // hidden entry out.
  expect(hrefs()).toEqual(plainBackupHrefs);
  body().getByText(`${plainBackupHrefs.length} items`);
  unmount();
});

test("an enabled status shows requires entries after the read resolves", async () => {
  const { unmount } = render(<SecondaryNav currentPath="/backup/backups" />);
  expect(rubrikLink()).toBeNull();
  await settle();
  expect(rubrikStatus.value.kind).toBe("enabled");
  expect(rubrikLink()).not.toBeNull();
  expect(hrefs()).toEqual([...plainBackupHrefs, "/backup/rubrik"]);
  body().getByText(`${plainBackupHrefs.length + 1} items`);
  unmount();
});

test("the server render matches the first client render while enabled", async () => {
  // The store already says enabled before the island hydrates, as when
  // another island read the status first: only the client gate keeps the
  // entry out of the server HTML and the hydrating render.
  await loadRubrikStatus(selectedCluster.value);
  expect(rubrikStatus.value.kind).toBe("enabled");
  const { html, container, errors, done } = await hydrate(() => (
    <SecondaryNav currentPath="/backup/backups" />
  ));
  expect(html).not.toContain('href="/backup/rubrik"');
  expect(errors).toEqual([]);
  await settle();
  expect(container.querySelector('a[href="/backup/rubrik"]')).not.toBeNull();
  done();
});

test("a 500 status keeps requires entries hidden and reports an error", async () => {
  statusReply = () => reply(500, { error: { code: 500, message: "boom" } });
  const { unmount } = render(<SecondaryNav currentPath="/backup/backups" />);
  await settle();
  expect(rubrikStatus.value).toEqual({ kind: "error", httpStatus: 500 });
  expect(rubrikLink()).toBeNull();
  expect(hrefs()).toEqual(plainBackupHrefs);
  unmount();
});

test("a failed read after an enabled one keeps the nav as it was", async () => {
  const { unmount } = render(<SecondaryNav currentPath="/backup/backups" />);
  await settle();
  expect(rubrikLink()).not.toBeNull();
  statusReply = () => reply(502, { error: { code: 502, message: "boom" } });
  await reloadRubrikStatus();
  await settle();
  expect(rubrikStatus.value).toEqual({ kind: "error", httpStatus: 502 });
  expect(rubrikLink()).not.toBeNull();
  unmount();
});

test("a slow older read does not overwrite a newer answer", async () => {
  let releaseOld!: (r: Response) => void;
  const old = new Promise<Response>((r) => {
    releaseOld = r;
  });
  const queue: (() => Response | Promise<Response>)[] = [
    () => old,
    () => reply(404, { error: { code: 404, message: "404 page not found" } }),
  ];
  statusReply = (() => queue.shift()?.()) as unknown as () => Response;
  const first = loadRubrikStatus("local");
  await loadRubrikStatus("local");
  expect(rubrikStatus.value.kind).toBe("disabled");
  releaseOld(reply(200, { data: status() }));
  await first;
  expect(rubrikStatus.value.kind).toBe("disabled");
});

test("a domain with no requires entry never asks for the status", async () => {
  const { unmount } = render(
    <SecondaryNav currentPath="/workloads/deployments" />,
  );
  await settle();
  expect(statusCalls.length).toBe(0);
  expect(rubrikStatus.value.kind).toBe("loading");
  unmount();
});

test("a cluster switch reads the status again for the new cluster", async () => {
  const start = selectedCluster.value;
  const { unmount } = render(<SecondaryNav currentPath="/backup/backups" />);
  await settle();
  expect(statusCalls.map((c) => c.cluster)).toEqual([start]);
  try {
    switchCluster("c-two", "gen-1");
    await settle();
    expect(statusCalls.map((c) => c.cluster)).toEqual([start, "c-two"]);
  } finally {
    switchCluster(start, "local");
    await settle();
    unmount();
  }
});

test("flattenGroups leaves out requires entries and is otherwise unchanged", () => {
  if (!backup) throw new Error("no backup section");
  expect(flattenGroups(backup).map((it) => it.href)).toEqual(plainBackupHrefs);
  for (const section of DOMAIN_SECTIONS) {
    const all = (section.groups ?? []).flatMap((g) => g.items);
    expect(flattenGroups(section)).toEqual(all.filter((it) => !it.requires));
  }
});
