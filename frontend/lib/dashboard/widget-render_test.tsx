/** @jsxImportSource preact */
import { beforeEach, describe, expect, test } from "bun:test";
import { render } from "preact-render-to-string";
// Registers every widget. Without it the registry is empty and each lookup
// below would return undefined, so these tests would pass vacuously.
import "@/components/dashboard/widgets/index.ts";
import type { SourceState } from "./data.ts";
import { dashboardData } from "./data.ts";
import { PARAM_KEY_NAMESPACE, sourceKeyFor } from "./params.ts";
import { getWidget } from "./registry.ts";

/**
 * Render coverage for the widget bodies.
 *
 * Everything else in lib/dashboard is a pure function with its own tests, and
 * those are where the arithmetic lives. What had no coverage at all was the
 * JSX wiring on top of them: which source key a widget reads, and which of
 * its branches that produces. A widget can pass every pure-module test in the
 * repo and still render the wrong branch, or read a key nothing fetches and
 * sit in a skeleton for good, and nothing would catch it -- the catalog's
 * e2e specs cover placement mechanics and the ten default widgets, not a
 * card's rendered content against known data.
 *
 * These are a representative set rather than one per widget. All 38 bodies
 * are built to one shape, so a defect in the shape is a defect 38 times and a
 * per-widget file would be 38 near-identical copies of the same three
 * assertions. The ones chosen below span the shapes that actually differ:
 * a list page with a readable/unreadable distinction, an envelope-backed
 * card, and a card whose absence arrives as a status code rather than a
 * discovery route.
 *
 * `preact-render-to-string` rather than a DOM harness: these branches are
 * static output, no events and no effects, so a DOM adds a dependency and a
 * global registrator for nothing. It was already in the tree via
 * @astrojs/preact; this file is why it is now declared directly.
 */

/** Seeds one source key's state, the way a landed fetch would. */
function seed(key: string, over: Partial<SourceState>): void {
  dashboardData.signalFor(key).value = {
    data: null,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
    ...over,
  };
}

/** Renders a registered widget by id, exactly as the host would. */
function renderWidget(id: string, params: Record<string, string> = {}): string {
  const def = getWidget(id);
  if (def === undefined) {
    throw new Error(
      `${id} is not registered. The manifest import at the top of this file ` +
        `is what registers widgets; a lookup failing here means the widget ` +
        `was renamed or dropped, not that the test is wrong.`,
    );
  }
  return render(def.render({ mode: "normal", params }));
}

/** A ResourceListPage payload, including the malformed shapes. */
function page(items: unknown, total?: number): unknown {
  return { items, total: total ?? (Array.isArray(items) ? items.length : 0) };
}

function node(name: string, conditions: Array<[string, string]>) {
  return {
    metadata: { name },
    status: {
      conditions: conditions.map(([type, status]) => ({ type, status })),
    },
  };
}

describe("node-conditions", () => {
  beforeEach(() => {
    seed("nodes-list", { data: null });
  });

  test("a readable page with a problem renders the list, not the empty state", () => {
    seed("nodes-list", {
      data: page([
        node("n1", [["Ready", "False"]]),
        node("n2", [["Ready", "True"]]),
      ]),
    });
    const html = renderWidget("node-conditions");
    expect(html).toContain("node-conditions-list");
    expect(html).not.toContain("node-conditions-empty");
    expect(html).not.toContain("node-conditions-unreadable-page");
  });

  test("a readable empty page renders the clean state", () => {
    seed("nodes-list", { data: page([node("n1", [["Ready", "True"]])]) });
    const html = renderWidget("node-conditions");
    expect(html).toContain("node-conditions-empty");
    expect(html).not.toContain("node-conditions-unreadable-page");
  });

  // The defect this release exists to prevent, at the point it reaches a
  // screen. Every count is zero in both cases; only `readable` tells them
  // apart, and only if the card actually branches on it.
  test("an unreadable page does NOT render the clean state", () => {
    for (const bad of [page(null), page("nodes"), page(7)]) {
      seed("nodes-list", { data: bad });
      const html = renderWidget("node-conditions");
      expect(html).toContain("node-conditions-unreadable-page");
      expect(html).not.toContain("node-conditions-empty");
      expect(html).not.toContain("node-conditions-list");
    }
  });
});

// A PARAMETERIZED widget, which is the shape most likely to read a key
// nothing ever fetched: its source is cached under a key carrying the
// parameter values, so a body that looked up the bare source name would find
// an idle entry and render a skeleton for good.
describe("vulnerability-severity", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod" };
  const key = sourceKeyFor("vulnerability-reports", params);

  test("reads the key its parameters resolve to, not the bare source name", () => {
    // Seeded ONLY under the parameterized key. If the body read
    // "vulnerability-reports" directly it would see idle data here.
    seed(key, { data: { vulnerabilities: [] } });
    const html = renderWidget("vulnerability-severity", params);
    // An empty list is a READ that found nothing scanned -- distinct from
    // both the unreadable state and the skeleton a wrong key would leave.
    // The namespace chip proves the params reached the body too.
    expect(html).toContain("vulnerability-unscanned");
    expect(html).toContain("prod");
    expect(html).not.toContain("vulnerability-unreadable");
  });

  // The finding #476 shipped to fix, at the point it reaches a screen: the
  // scanning route answers 200 with a null list when both scanner fetches
  // failed, or when the viewer holds one scanner's grant and every report
  // came from the other. Neither is a clean scan.
  test("a null list is not a clean scan", () => {
    seed(key, { data: { vulnerabilities: null } });
    const html = renderWidget("vulnerability-severity", params);
    expect(html).toContain("vulnerability-unreadable");
    expect(html).not.toContain("vulnerability-clear");
  });
});

// A PLATFORM widget, whose absence is a status code rather than a discovery
// route, and whose empty state is the common case -- an account with nothing
// unread. That combination is what made its null-instead-of-empty defect the
// worst of the release: the card would sit in its skeleton forever on exactly
// the account it has the best news for.
describe("notifications-feed", () => {
  test("an empty feed renders the none state, not a skeleton", () => {
    seed("unread-notifications", { data: { items: [] } });
    const html = renderWidget("notifications-feed");
    expect(html).toContain("notifications-feed-none");
    expect(html).not.toContain("notifications-feed-unreadable");
  });

  test("a null body is unreadable, not empty", () => {
    seed("unread-notifications", { data: { items: null } });
    const html = renderWidget("notifications-feed");
    expect(html).toContain("notifications-feed-unreadable");
    expect(html).not.toContain("notifications-feed-none");
  });
});
