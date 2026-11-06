import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * A ResourceTable's `kind` goes into `/v1/resources/{kind}` and must be a
 * backend adapter's `Kind()`, which for some kinds is a short name ("hpas",
 * "pdbs", "pvcs", "pvs") rather than the API resource ("horizontalpodautoscalers").
 * A page that used the API resource name got "404: unknown resource kind".
 *
 * This reads every adapter's `Kind()` from the backend source, plus the kinds
 * routes.go serves with a handler of their own (CiliumNetworkPolicies), and
 * checks each literal kind a page or dashboard hands to ResourceTable:
 * `kind="…"` on the element, and the `kind: "…"` entries of a dashboard that
 * passes `kind={kind}` from its tab table.
 */
const ROOT = join(import.meta.dir, "..", "..");
const BACKEND = join(ROOT, "..", "backend", "internal");
const ADAPTERS = join(BACKEND, "k8s", "resources");
const ROUTES = join(BACKEND, "server", "routes.go");

function adapterKinds(): Set<string> {
  const kinds = new Set<string>();
  for (const name of readdirSync(ADAPTERS)) {
    if (!name.endsWith(".go") || name.endsWith("_test.go")) continue;
    const src = readFileSync(join(ADAPTERS, name), "utf8");
    for (const m of src.matchAll(
      /\)\s*Kind\(\)\s*string\s*\{\s*return\s+"([a-z]+)"/g,
    )) {
      kinds.add(m[1]);
    }
  }
  const routes = readFileSync(ROUTES, "utf8");
  for (const m of routes.matchAll(/\.Get\("\/resources\/([a-z]+)"/g)) {
    kinds.add(m[1]);
  }
  return kinds;
}

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) walk(p, out);
    else if (/\.(tsx|astro)$/.test(entry.name) && !/_test\./.test(entry.name))
      out.push(p);
  }
  return out;
}

/** Every literal kind each file hands to ResourceTable, with its file. */
function tableKinds(): { file: string; kind: string }[] {
  const found: { file: string; kind: string }[] = [];
  for (const file of [
    ...walk(join(ROOT, "src")),
    ...walk(join(ROOT, "components")),
  ]) {
    const src = readFileSync(file, "utf8");
    if (!src.includes("<ResourceTable")) continue;
    const rel = file.slice(ROOT.length + 1).replaceAll("\\", "/");
    for (const m of src.matchAll(/<ResourceTable\b[^>]*?\bkind="([^"]+)"/gs)) {
      found.push({ file: rel, kind: m[1] });
    }
    if (/<ResourceTable\b[^>]*?\bkind=\{kind\}/s.test(src)) {
      for (const m of src.matchAll(/\bkind:\s*"([^"]+)"/g)) {
        found.push({ file: rel, kind: m[1] });
      }
    }
  }
  return found;
}

test("the backend adapters were found", () => {
  const kinds = adapterKinds();
  expect(kinds.has("pods")).toBe(true);
  expect(kinds.has("hpas")).toBe(true);
  expect(kinds.has("ciliumnetworkpolicies")).toBe(true);
  expect(kinds.has("horizontalpodautoscalers")).toBe(false);
});

test("every ResourceTable kind is a backend adapter kind", () => {
  const kinds = adapterKinds();
  const found = tableKinds();
  expect(found.length).toBeGreaterThan(10);
  const unknown = found
    .filter((f) => !kinds.has(f.kind))
    .map((f) => `${f.file}: "${f.kind}"`);
  expect(unknown).toEqual([]);
});
