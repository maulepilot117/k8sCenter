import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  extractAstroTemplate,
  findIslandImports,
  findMissingHydrationDirectives,
  findTagCallSites,
} from "./check-island-hydration-directives.ts";

// --- pure-function unit coverage ---

test("extractAstroTemplate returns only the markup below the frontmatter fence", () => {
  const source = `---
import Foo from "../islands/Foo.tsx";
---
<Foo client:load />
`;
  const template = extractAstroTemplate(source);
  expect(template).toContain("<Foo client:load />");
  expect(template).not.toContain("import Foo");
});

test("extractAstroTemplate returns the whole source when there is no frontmatter fence", () => {
  expect(extractAstroTemplate("<div>just markup</div>")).toBe(
    "<div>just markup</div>",
  );
});

test("findTagCallSites finds a self-closing tag and a multi-line tag with brace-nested attributes", () => {
  const template = `
    <Foo client:load />
    <Bar
      client:load
      style={{ color: "red" }}
      title="a > b"
    >
      child
    </Bar>
  `;
  const fooTags = findTagCallSites(template, "Foo");
  expect(fooTags).toHaveLength(1);
  expect(fooTags[0]).toBe("<Foo client:load />");

  const barTags = findTagCallSites(template, "Bar");
  expect(barTags).toHaveLength(1);
  // The `>` inside style={{...}} and the string "a > b" must not end the
  // tag early — the captured tag should reach the real closing `>`.
  expect(barTags[0]).toContain('title="a > b"');
  expect(barTags[0].trim().endsWith(">")).toBe(true);
});

test("findTagCallSites does not match a longer component name sharing a prefix", () => {
  const template = `<FooBar client:load />`;
  expect(findTagCallSites(template, "Foo")).toHaveLength(0);
});

// --- fixture-backed integration coverage ---

function makeFixture(): { root: string; cleanup: () => void } {
  const root = mkdtempSync(join(tmpdir(), "island-hydration-guard-"));
  mkdirSync(join(root, "src", "islands"), { recursive: true });
  mkdirSync(join(root, "src", "pages"), { recursive: true });
  mkdirSync(join(root, "src", "layouts"), { recursive: true });
  writeFileSync(
    join(root, "src", "islands", "Widget.tsx"),
    "export default function Widget() { return null; }\n",
  );
  return {
    root,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}

test("findMissingHydrationDirectives passes a page whose island call site has client:load", () => {
  const { root, cleanup } = makeFixture();
  try {
    const page = join(root, "src", "pages", "ok.astro");
    writeFileSync(
      page,
      `---\nimport Widget from "../islands/Widget.tsx";\n---\n<Widget client:load prop="x" />\n`,
    );
    expect(findMissingHydrationDirectives([page], root)).toEqual([]);
  } finally {
    cleanup();
  }
});

test("findMissingHydrationDirectives flags a page whose island call site has no directive", () => {
  const { root, cleanup } = makeFixture();
  try {
    const page = join(root, "src", "pages", "bad.astro");
    writeFileSync(
      page,
      `---\nimport Widget from "../islands/Widget.tsx";\n---\n<Widget prop="x" />\n`,
    );
    const violations = findMissingHydrationDirectives([page], root);
    expect(violations).toHaveLength(1);
    expect(violations[0].island).toBe("Widget");
  } finally {
    cleanup();
  }
});

test("findMissingHydrationDirectives flags a lazier directive the same as a missing one is not applicable — client:visible still counts as present", () => {
  // KTD4 treats any non-client:load directive as its own parity defect
  // category (a behavior change), not a "missing directive" — this guard's
  // job is narrower: catch the call site that hydrates never. Confirms
  // client:visible does not trip THIS check (a separate KTD4 audit step
  // covers "only client:load" over the whole route tree).
  const { root, cleanup } = makeFixture();
  try {
    const page = join(root, "src", "pages", "visible.astro");
    writeFileSync(
      page,
      `---\nimport Widget from "../islands/Widget.tsx";\n---\n<Widget client:visible />\n`,
    );
    expect(findMissingHydrationDirectives([page], root)).toEqual([]);
  } finally {
    cleanup();
  }
});

test("findIslandImports ignores imports that do not resolve under src/islands/", () => {
  const { root, cleanup } = makeFixture();
  try {
    mkdirSync(join(root, "src", "layouts"), { recursive: true });
    writeFileSync(
      join(root, "src", "layouts", "Base.astro"),
      "---\n---\n<slot />\n",
    );
    const page = join(root, "src", "pages", "mixed.astro");
    writeFileSync(
      page,
      `---\nimport Base from "../layouts/Base.astro";\nimport Widget from "../islands/Widget.tsx";\n---\n<Widget client:load />\n`,
    );
    const imports = findIslandImports(page, root);
    expect(imports).toHaveLength(1);
    expect(imports[0].local).toBe("Widget");
  } finally {
    cleanup();
  }
});

test("the real route tree has no missing island hydration directives", () => {
  // Whole-tree regression run against the actual src/pages + src/layouts,
  // using the module's own default entry-point discovery.
  expect(findMissingHydrationDirectives()).toEqual([]);
});
