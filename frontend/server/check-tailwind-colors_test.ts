// biome-ignore-all lint/suspicious/noTemplateCurlyInString: the fixtures are source text, where "${x}" is the literal the guard reads, not an interpolation.
import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import {
  type ClassResolver,
  extractCandidates,
  findUnknownColourClasses,
  loadResolver,
} from "./check-tailwind-colors.ts";

test("colour classes are read from class strings, with their variants", () => {
  const source = [
    '<div class="flex gap-2 bg-elevated hover:bg-hover text-sm" />',
    '<b class={`p-2 ${on ? "text-danger" : "text-text-muted"}`} />',
    'const tone = { error: "bg-danger/10 border-danger/30" };',
  ].join("\n");
  expect(extractCandidates(source).sort()).toEqual([
    "bg-danger/10",
    "bg-elevated",
    "border-danger/30",
    "hover:bg-hover",
    "text-danger",
    "text-sm",
    "text-text-muted",
  ]);
});

test("object keys, CSS words, interpolations and comments are not classes", () => {
  const source = [
    "/** Uses `text-muted` like the rest. */",
    "// old: bg-bg-elevated",
    "<!-- bg-bg-surface -->",
    'const attrs = { "stroke-width": "1.5" };',
    'const s = { boxSizing: "border-box", transition: "border-color 0.2s" };',
    "const c = `bg-${tone}`;",
  ].join("\n");
  expect(extractCandidates(source)).toEqual([]);
});

/**
 * The guard's verdicts come from Tailwind's unstable design-system API. This
 * pins them against the real stylesheet, so a Tailwind upgrade that changes
 * that API fails here instead of letting every class through.
 */
test("Tailwind resolves the app's tokens and rejects missing ones", async () => {
  const resolver = await loadResolver();
  const classes = [
    "bg-elevated",
    "hover:bg-hover",
    "bg-accent/10",
    "text-(--bg-base)",
    "border-border-primary",
    "bg-bg-elevated",
    "text-status-error",
    "border-border",
    "text-on-accent",
  ];
  const ok = resolver
    .candidatesToCss(classes)
    .map((css, i) => [classes[i], css !== null]);
  expect(ok).toEqual([
    ["bg-elevated", true],
    ["hover:bg-hover", true],
    ["bg-accent/10", true],
    ["text-(--bg-base)", true],
    ["border-border-primary", true],
    ["bg-bg-elevated", false],
    ["text-status-error", false],
    ["border-border", false],
    ["text-on-accent", false],
  ]);
});

test("the check lists each class that produces no CSS, with its files", () => {
  const root = mkdtempSync(join(tmpdir(), "k8sc-colours-"));
  try {
    const files: Record<string, string> = {
      "src/islands/A.tsx": '<div class="bg-elevated bg-bg-elevated" />',
      "components/B.tsx": '<div class="bg-bg-elevated" />',
      "src/islands/A_test.tsx": '<div class="bg-nowhere" />',
    };
    for (const [path, contents] of Object.entries(files)) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), contents);
    }
    const resolver: ClassResolver = {
      candidatesToCss: (classes) =>
        classes.map((c) => (c === "bg-elevated" ? ".bg-elevated{}" : null)),
    };
    expect(findUnknownColourClasses(resolver, root)).toEqual({
      candidates: 2,
      offenders: [
        {
          className: "bg-bg-elevated",
          files: ["components/B.tsx", "src/islands/A.tsx"],
        },
      ],
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
