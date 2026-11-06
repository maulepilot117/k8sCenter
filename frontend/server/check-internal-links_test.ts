// biome-ignore-all lint/suspicious/noTemplateCurlyInString: the fixtures are source text, where "${x}" is the literal the guard reads, not an interpolation.
import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import {
  checkInternalLinks,
  extractLinks,
  linkSegments,
  resolves,
  routeFromPagePath,
} from "./check-internal-links.ts";

/** A throwaway frontend root holding `files` (path -> contents). */
function withFixture(
  files: Record<string, string>,
  fn: (root: string) => void,
): void {
  const root = mkdtempSync(join(tmpdir(), "k8sc-links-"));
  try {
    for (const [path, contents] of Object.entries(files)) {
      const full = join(root, path);
      mkdirSync(dirname(full), { recursive: true });
      writeFileSync(full, contents);
    }
    fn(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test("page paths become routes, index serving its directory", () => {
  expect(routeFromPagePath("index.astro")).toEqual([]);
  expect(routeFromPagePath("config/secrets.astro")).toEqual([
    "config",
    "secrets",
  ]);
  expect(routeFromPagePath("config/secrets/[namespace]/[name].astro")).toEqual([
    "config",
    "secrets",
    "[namespace]",
    "[name]",
  ]);
  expect(routeFromPagePath("backup/index.astro")).toEqual(["backup"]);
});

test("links that cannot be checked statically are skipped", () => {
  expect(linkSegments("https://example.com/x")).toBeNull();
  expect(linkSegments("//cdn.example.com/x")).toBeNull();
  expect(linkSegments("/api/v1/resources")).toBeNull();
  expect(linkSegments("/ws/resources")).toBeNull();
  expect(linkSegments("/favicon.svg")).toBeNull();
  expect(linkSegments("/${base}/x")).toBeNull();
});

test("query strings, fragments and interpolations are handled", () => {
  expect(linkSegments("/security/violations?namespace=x")).toEqual([
    "security",
    "violations",
  ]);
  expect(linkSegments("/workloads/pods/${ns}/${name}#logs")?.length).toBe(4);
  // A "?" inside an interpolation does not end the path.
  expect(linkSegments("/a/${x ? y : z}/b")?.length).toBe(3);
});

test("a link resolves only to a route of the same shape", () => {
  const routes = [
    ["config", "secrets", "[namespace]", "[name]"],
    ["backup", "backups"],
    ["backup", "backups", "new"],
    [],
  ];
  const seg = (h: string) => linkSegments(h) as string[];
  expect(resolves(seg("/config/secrets/${ns}/${n}"), routes)).toBe(true);
  expect(resolves(seg("/workloads/secrets/${ns}/${n}"), routes)).toBe(false);
  expect(resolves(seg("/backup/backups/${ns}/${n}"), routes)).toBe(false);
  expect(resolves(seg("/backup/backups"), routes)).toBe(true);
  // An interpolated segment may produce a literal one.
  expect(resolves(seg("/backup/backups/${action}"), routes)).toBe(true);
  expect(resolves(seg("/"), routes)).toBe(true);
});

test("links are read from attributes, objects and location writes", () => {
  const source = [
    '<a href="/a">a</a>',
    "<a href={`/b/${x}`}>b</a>",
    'const nav = { href: "/c" };',
    'globalThis.location.href = "/d";',
    "const notALink = `/e`;",
  ].join("\n");
  expect(extractLinks(source)).toEqual([
    { line: 1, href: "/a" },
    { line: 2, href: "/b/${x}" },
    { line: 3, href: "/c" },
    { line: 4, href: "/d" },
  ]);
});

test("the check reports each dead link with its file and line", () => {
  withFixture(
    {
      "src/pages/index.astro": "",
      "src/pages/config/secrets/[namespace]/[name].astro": "",
      "src/islands/Cert.tsx":
        "const ok = <a href={`/config/secrets/${ns}/${n}`} />;\n" +
        "const bad = <a href={`/workloads/secrets/${ns}/${n}`} />;\n",
      "src/islands/Cert_test.tsx": '<a href="/nowhere" />',
      "components/Back.tsx": '<a href="/cluster/dashboard">Back</a>',
    },
    (root) => {
      const result = checkInternalLinks(root);
      expect(result.routes).toBe(2);
      expect(result.offenders).toEqual([
        {
          file: "components/Back.tsx",
          line: 1,
          href: "/cluster/dashboard",
        },
        {
          file: "src/islands/Cert.tsx",
          line: 2,
          href: "/workloads/secrets/${ns}/${n}",
        },
      ]);
    },
  );
});
