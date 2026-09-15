/**
 * KTD14 visual spot-check: Fresh (Deno) vs Astro (Bun) render parity.
 *
 * SUPERSEDED BY U12 -- the Fresh half of this comparison no longer exists.
 *
 * U12 deleted frontend/routes and frontend/islands, so FRESH_ORIGIN cannot be
 * served from a checkout of main any more. To run this again, build the Fresh
 * tree from the last commit that had it:
 *
 *     git worktree add ../k8scenter-fresh <commit-before-U12>
 *     cd ../k8scenter-fresh/frontend && deno task build
 *     deno serve --port 8100 _fresh/server.js
 *
 * It is kept rather than deleted for two reasons. --control mode does not need
 * the Fresh tree at all: it captures one origin against itself and reports the
 * noise floor, which is what tells you whether a diff percentage means
 * anything. And the class of defect it found is now covered going forward by
 * frontend/server/check-placeholder-root-parity.ts, which was written from
 * this harness's findings -- so this file is the provenance of that guard's
 * baseline.
 *
 * Plan: docs/plans/2026-09-14-0850-refactor-bun-astro-migration-plan.md
 *   U12 step 1b -- "Run the visual spot-check across the 49 navigation-reachable
 *   pages (KTD14) before deleting. It can only run while both trees exist."
 *   Done: 47/50 pixel-identical, max residual 0.104%, equal to the
 *   Fresh-vs-Fresh control. See the 2026-09-15 session log.
 *
 * WHY THIS EXISTS AND WHY IT IS NOT A PLAYWRIGHT SPEC
 *
 * The E2E suite asserts behaviour and contains no screenshot assertion anywhere.
 * That is deliberate, and it is also why it could not see the defect this
 * harness was written to find: two islands rendered with their SSR placeholder's
 * class/style instead of the hydrated root's, because Preact hydration matches
 * the server-rendered root element and recurses into its children WITHOUT
 * re-applying that root's attributes. The DOM was present, the markup valid, the
 * accessibility tree correct. Only the computed style was wrong, so `bun run
 * check`, `bun test` and the whole E2E suite stayed green while the YAML Apply
 * editor drew its contents off-screen behind an opaque nav.
 *
 * This is a one-shot instrument, not a gate: it needs BOTH trees running, so it
 * stops working the moment U12 deletes the Fresh tree. It is committed so the
 * pre-U12 check does not have to be rebuilt from memory, and so the numbers a
 * future run produces can be compared against the ones in the U12 record.
 *
 * READ THE CONTROL BEFORE BELIEVING A DIFF
 *
 * Both trees render live cluster data, which ticks between captures: pod ages,
 * metric values, event timestamps. A small cross-tree difference usually means
 * the data moved, not the renderer. `--control` measures that noise floor by
 * capturing the SAME tree twice and diffing it. A cross-tree number is only
 * meaningful above its page's own control number. When this was run against the
 * fix in PR #448, /storage/overview showed 0.104% cross-tree and 0.104%
 * Fresh-vs-Fresh -- pure churn.
 *
 * USAGE
 *
 *   # start both production builds against ONE backend, then:
 *   cd e2e
 *   npm run spotcheck                 # Fresh vs Astro across every nav page
 *   npm run spotcheck -- --control    # each tree against itself (noise floor)
 *
 *   FRESH_ORIGIN   default http://localhost:8100   (deno serve _fresh/server.js)
 *   ASTRO_ORIGIN   default http://localhost:8000   (bun run start)
 *   KTD14_OUT      default ./ktd14-out             (gitignored)
 *
 * Both origins must point at the same backend or every data-bearing page
 * differs and the run tells you nothing.
 */
import { chromium } from "@playwright/test";
import pixelmatch from "pixelmatch";
import { PNG } from "pngjs";
import fs from "node:fs";
import path from "node:path";

const ORIGINS = {
  fresh: process.env.FRESH_ORIGIN ?? "http://localhost:8100",
  astro: process.env.ASTRO_ORIGIN ?? "http://localhost:8000",
};

const OUT = process.env.KTD14_OUT ?? path.join(process.cwd(), "ktd14-out");
const VIEWPORT = { width: 1440, height: 900 };
const CONTROL = process.argv.includes("--control");

// Anti-aliasing and subpixel text rendering differ slightly between two
// independently-built bundles even when the CSS is identical. 0.1 is
// pixelmatch's usual "same image, different encoder" band.
const PIXEL_THRESHOLD = 0.1;

// Credentials match fixtures/auth.setup.ts -- the same admin the E2E suite
// creates. Both origins are pointed at a dev backend, never a real cluster.
const USER = "admin";
const PASS = "admin123";
const SETUP_TOKEN = "e2e-setup-token";

/** Single source of truth: the nav list the E2E suite already asserts on. */
function loadNavItems() {
  const spec = fs.readFileSync(
    path.join(process.cwd(), "tests", "navigation.spec.ts"),
    "utf8",
  );
  const block = spec.match(/const NAV_ITEMS = \[([\s\S]*?)\];/);
  if (!block) {
    throw new Error(
      "could not parse NAV_ITEMS from tests/navigation.spec.ts -- the spec's " +
        "shape changed; update this parser rather than duplicating the list",
    );
  }
  return [...block[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]);
}

async function authenticate(context, origin) {
  // Idempotent: 410 when the admin already exists.
  await context.request.post(`${origin}/api/v1/setup/init`, {
    data: { username: USER, password: PASS, setupToken: SETUP_TOKEN },
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });

  const page = await context.newPage();
  await page.goto(`${origin}/login`, { waitUntil: "domcontentloaded" });
  await page.getByLabel("Username").fill(USER);
  await page.getByLabel("Password").fill(PASS);
  await page.getByRole("button", { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"), {
    timeout: 30_000,
  });
  return page;
}

async function capture(page, origin, href) {
  await page.goto(`${origin}${href}`, {
    waitUntil: "domcontentloaded",
    timeout: 30_000,
  });
  // Islands hydrate and fetch after DOMContentLoaded; let them settle.
  await page.waitForLoadState("networkidle", { timeout: 20_000 }).catch(() => {});
  await page.waitForTimeout(1200);
  return page.screenshot({ fullPage: true, animations: "disabled", scale: "css" });
}

function diff(aBuf, bBuf, outPath) {
  const a = PNG.sync.read(aBuf);
  const b = PNG.sync.read(bBuf);
  const width = Math.min(a.width, b.width);
  const height = Math.min(a.height, b.height);
  const sizeMismatch = a.width !== b.width || a.height !== b.height;

  // pixelmatch needs identical dimensions. Crop both to the overlap and report
  // the size delta separately rather than silently padding -- a height
  // difference is itself a finding, not something to normalise away.
  const crop = (src) => {
    const dst = new PNG({ width, height });
    PNG.bitblt(src, dst, 0, 0, width, height, 0, 0);
    return dst;
  };
  const out = new PNG({ width, height });
  const changed = pixelmatch(
    crop(a).data,
    crop(b).data,
    out.data,
    width,
    height,
    { threshold: PIXEL_THRESHOLD, includeAA: false },
  );
  if (changed > 0 && outPath) fs.writeFileSync(outPath, PNG.sync.write(out));
  return {
    changed,
    total: width * height,
    pct: (changed / (width * height)) * 100,
    sizeMismatch,
    dims: { a: [a.width, a.height], b: [b.width, b.height] },
  };
}

/** Capture each tree twice and diff it against itself: the data-churn floor. */
async function runControl(browser, navItems) {
  console.log(`KTD14 control: ${navItems.length} pages, each tree vs itself\n`);
  const results = [];
  for (const [name, origin] of Object.entries(ORIGINS)) {
    const ctx = await browser.newContext({
      viewport: VIEWPORT,
      reducedMotion: "reduce",
      deviceScaleFactor: 1,
    });
    const page = await authenticate(ctx, origin);
    console.log(`=== ${name} vs itself ===`);
    for (const href of navItems) {
      const first = await capture(page, origin, href);
      await page.waitForTimeout(3000);
      const second = await capture(page, origin, href);
      const r = diff(first, second, null);
      results.push({ tree: name, href, pct: r.pct, changed: r.changed });
      if (r.changed > 0) {
        console.log(`  ${href.padEnd(32)} ${r.pct.toFixed(3)}% (${r.changed} px)`);
      }
    }
    await ctx.close();
  }
  fs.mkdirSync(OUT, { recursive: true });
  fs.writeFileSync(
    path.join(OUT, "control.json"),
    JSON.stringify(results, null, 2),
  );
  const noisy = results.filter((r) => r.changed > 0);
  console.log(
    `\nPages that churn on their own: ${noisy.length}/${results.length}. ` +
      `A cross-tree diff at or below a page's number here is data, not rendering.`,
  );
  console.log(`artifacts: ${OUT}`);
}

async function runComparison(browser, navItems) {
  for (const sub of ["fresh", "astro", "diff"]) {
    fs.mkdirSync(path.join(OUT, sub), { recursive: true });
  }

  console.log(`KTD14 spot-check: ${navItems.length} navigation-reachable pages`);
  console.log(`  fresh = ${ORIGINS.fresh}`);
  console.log(`  astro = ${ORIGINS.astro}\n`);

  const mk = () =>
    browser.newContext({
      viewport: VIEWPORT,
      reducedMotion: "reduce",
      deviceScaleFactor: 1,
    });
  const freshCtx = await mk();
  const astroCtx = await mk();
  const freshPage = await authenticate(freshCtx, ORIGINS.fresh);
  const astroPage = await authenticate(astroCtx, ORIGINS.astro);
  console.log("authenticated on both origins\n");

  const results = [];
  for (const href of navItems) {
    const slug =
      href === "/" ? "_root" : href.replace(/^\//, "").replace(/\//g, "_");
    const row = { href, slug };
    try {
      // Back-to-back so live cluster data has the least chance to move.
      const fBuf = await capture(freshPage, ORIGINS.fresh, href);
      const aBuf = await capture(astroPage, ORIGINS.astro, href);
      fs.writeFileSync(path.join(OUT, "fresh", `${slug}.png`), fBuf);
      fs.writeFileSync(path.join(OUT, "astro", `${slug}.png`), aBuf);
      Object.assign(row, diff(fBuf, aBuf, path.join(OUT, "diff", `${slug}.png`)));
    } catch (err) {
      row.error = String(err).split("\n")[0];
    }
    results.push(row);
    const label = row.error
      ? `ERROR ${row.error}`
      : `${row.pct.toFixed(3)}% (${row.changed} px)${
          row.sizeMismatch ? " SIZE-DIFF" : ""
        }`;
    console.log(`  ${href.padEnd(34)} ${label}`);
  }

  fs.writeFileSync(
    path.join(OUT, "report.json"),
    JSON.stringify(results, null, 2),
  );

  const errors = results.filter((r) => r.error);
  const clean = results.filter((r) => !r.error && r.changed === 0);
  const differing = results
    .filter((r) => !r.error && r.changed > 0)
    .sort((x, y) => y.pct - x.pct);

  console.log(`\n${"=".repeat(64)}`);
  console.log(`pixel-identical : ${clean.length}/${results.length}`);
  console.log(`differing       : ${differing.length}`);
  console.log(`errored         : ${errors.length}`);
  if (differing.length) {
    console.log(`\ntop differences:`);
    for (const r of differing.slice(0, 20)) {
      console.log(
        `  ${r.pct.toFixed(3).padStart(8)}%  ${r.href}` +
          (r.sizeMismatch
            ? `  [fresh ${r.dims.a.join("x")} vs astro ${r.dims.b.join("x")}]`
            : ""),
      );
    }
    console.log(
      `\nBefore treating any of these as a rendering difference, run ` +
        `--control and compare each page against its own churn number.`,
    );
  }
  if (errors.length) {
    console.log(`\nerrors:`);
    for (const r of errors) console.log(`  ${r.href}: ${r.error}`);
  }
  console.log(`\nartifacts: ${OUT}`);
}

const main = async () => {
  const navItems = loadNavItems();
  const browser = await chromium.launch();
  try {
    if (CONTROL) await runControl(browser, navItems);
    else await runComparison(browser, navItems);
  } finally {
    await browser.close();
  }
};

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
