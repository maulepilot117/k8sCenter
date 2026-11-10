/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * Compliance snapshots are recorded for the local cluster only, so under a
 * remote selection the history route answers 501 `remote_history_unsupported`
 * (#530). The trend card must say that, not report a failed read with a Retry
 * that can never succeed, and must keep any other failure an error.
 *
 * The island fetches only when `IS_BROWSER` is true, so this file sets it.
 */
setupDom({ browser: true });

const { default: ComplianceTrendChart } = await import(
  "./ComplianceTrendChart.tsx"
);

const originalFetch = globalThis.fetch;

beforeEach(() => setAccessToken("test-token"));

afterEach(() => {
  globalThis.fetch = originalFetch;
  setAccessToken(null);
});

function serve(status: number, body: unknown) {
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
}

async function mountUntil(text: string): Promise<Element> {
  const { container } = render(<ComplianceTrendChart />);
  for (let i = 0; i < 50 && !container.textContent?.includes(text); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  return container;
}

test("a remote cluster's refusal says history is local-only, with no retry", async () => {
  serve(501, {
    error: {
      code: 501,
      message: "compliance history is recorded for the local cluster only",
      reason: "remote_history_unsupported",
    },
  });
  const root = await mountUntil("local cluster only");

  expect(
    root.querySelector('[data-testid="compliance-trend-remote-unsupported"]'),
  ).not.toBeNull();
  expect(root.textContent).not.toContain("Failed to load");
  expect(root.textContent).not.toContain("Retry");
});

test("any other failure is still a failed read with a retry", async () => {
  serve(500, { error: { code: 500, message: "boom" } });
  const root = await mountUntil("Failed to load");

  expect(root.textContent).toContain("Failed to load compliance trend data");
  expect(root.textContent).toContain("Retry");
  expect(
    root.querySelector('[data-testid="compliance-trend-remote-unsupported"]'),
  ).toBeNull();
});

// ---- remote history: not-configured, partial, markers, gaps ----

const { addDays, fillDateGaps, GAP_MESSAGES } = await import(
  "./ComplianceTrendChart.tsx"
);
const { trendStartIndex } = await import("@/lib/dashboard/severity.ts");
const { selectedCluster } = await import("@/src/lib/cluster.ts");

function pt(date: string, score: number, extra: Record<string, unknown> = {}) {
  return { date, score, pass: 1, fail: 0, warn: 0, total: 1, ...extra };
}

function today(offset = 0): string {
  return addDays(new Date().toISOString().slice(0, 10), offset);
}

test("a not-configured answer shows the not-configured state and no points", async () => {
  serve(404, {
    error: {
      code: 404,
      message: "compliance history is not configured for this cluster",
      reason: "compliance_history_not_configured",
    },
  });
  const root = await mountUntil("not configured for this cluster");

  expect(
    root.querySelector('[data-testid="compliance-trend-not-configured"]'),
  ).not.toBeNull();
  expect(root.querySelector("svg")).toBeNull();
  expect(root.textContent).not.toContain("Failed to load");
  expect(root.textContent).not.toContain("local cluster only");
  const link = root.querySelector('a[href="/settings/clusters"]');
  expect(link).not.toBeNull();
});

test("a partial point is drawn as partial and its tooltip names Gatekeeper", async () => {
  const d = today(-1);
  serve(200, {
    data: [
      pt(today(-2), 80),
      pt(d, 70, { partial: true, unreadable: ["gatekeeper"] }),
    ],
    gaps: [],
  });
  const root = await mountUntil("Current:");

  const marker = root.querySelector(
    `[data-testid="compliance-trend-partial-${d}"]`,
  );
  expect(marker).not.toBeNull();
  expect(marker?.textContent).toContain("Gatekeeper");
  expect(marker?.textContent).toContain("partial");
  expect(
    root.querySelector(`[data-testid="compliance-trend-partial-${today(-2)}"]`),
  ).toBeNull();
});

test("an identity change is marked and the delta, best and worst count only points since it", async () => {
  const sw = today(-1);
  serve(200, {
    data: [
      pt(today(-3), 90),
      pt(today(-2), 10),
      pt(sw, 50, { identityChanged: true }),
      pt(today(), 60),
    ],
    gaps: [],
  });
  const root = await mountUntil("Current:");

  expect(
    root.querySelector(
      `[data-testid="compliance-trend-identity-change-${sw}"]`,
    ),
  ).not.toBeNull();
  expect(
    root.querySelector('[data-testid="compliance-trend-delta"]')?.textContent,
  ).toBe("+10.0");
  expect(
    root.querySelector('[data-testid="compliance-trend-best"]')?.textContent,
  ).toBe("60.0");
  expect(
    root.querySelector('[data-testid="compliance-trend-worst"]')?.textContent,
  ).toBe("50.0");
});

test("a gap after the last point is shown with its reason's message", async () => {
  const last = today(-3);
  serve(200, {
    data: [pt(today(-4), 80), pt(last, 82)],
    gaps: [{ date: today(-1), reason: "unreachable" }],
  });
  const root = await mountUntil("Current:");

  const gap = root.querySelector(
    `[data-testid="compliance-trend-gap-${today(-1)}"]`,
  );
  expect(gap).not.toBeNull();
  expect(gap?.textContent).toContain(GAP_MESSAGES.unreachable);
  expect(
    root.querySelector('[data-testid="compliance-trend-gaps"]')?.textContent,
  ).toContain(GAP_MESSAGES.unreachable);
  // The unrecorded day after the gap is empty, not a gap.
  expect(
    root.querySelector(`[data-testid="compliance-trend-gap-${today()}"]`),
  ).toBeNull();
});

test("every gap reason code has its own message", () => {
  for (const reason of [
    "unreachable",
    "credentials_invalid",
    "impersonation_refused",
    "discovery_unavailable",
    "no_policy_engine",
    "forbidden",
  ]) {
    expect(GAP_MESSAGES[reason]).toBeTruthy();
  }
  expect(new Set(Object.values(GAP_MESSAGES)).size).toBe(
    Object.keys(GAP_MESSAGES).length,
  );
});

test("switching the selected cluster refetches the chart", async () => {
  const seen: (string | null)[] = [];
  globalThis.fetch = ((_url: unknown, init?: RequestInit) => {
    seen.push(new Headers(init?.headers).get("X-Cluster-ID"));
    return Promise.resolve(
      new Response(JSON.stringify({ data: [pt(today(), 70)], gaps: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as unknown as typeof globalThis.fetch;
  selectedCluster.value = "local";
  await mountUntil("Current:");
  const before = seen.length;

  await act(async () => {
    selectedCluster.value = "remote-1";
    await new Promise((r) => setTimeout(r, 20));
  });

  expect(seen.length).toBeGreaterThan(before);
  expect(seen[seen.length - 1]).toBe("remote-1");
  selectedCluster.value = "local";
});

test("addDays advances one day under a UTC+10 timezone", () => {
  const prev = process.env.TZ;
  process.env.TZ = "Australia/Brisbane";
  try {
    expect(addDays("2026-10-12", 1)).toBe("2026-10-13");
    expect(addDays("2026-10-31", 1)).toBe("2026-11-01");
    expect(addDays("2026-10-12", -1)).toBe("2026-10-11");
    const filled = fillDateGaps(
      [pt("2026-10-10", 80)],
      [{ date: "2026-10-12", reason: "forbidden" }],
      "2026-10-13",
    );
    expect(filled.map((p) => p.date)).toEqual([
      "2026-10-10",
      "2026-10-11",
      "2026-10-12",
      "2026-10-13",
    ]);
    expect(filled[2].gapReason).toBe("forbidden");
    expect(filled[3].gapReason).toBeUndefined();
  } finally {
    if (prev === undefined) delete process.env.TZ;
    else process.env.TZ = prev;
  }
});

test("trend start is the latest identity change, else the first point", () => {
  expect(trendStartIndex([{}, {}])).toBe(0);
  expect(
    trendStartIndex([
      {},
      { identityChanged: true },
      {},
      { identityChanged: true },
      {},
    ]),
  ).toBe(3);
});

test("choosing a range refetches that many days", async () => {
  const urls: string[] = [];
  globalThis.fetch = ((url: unknown) => {
    urls.push(String(url));
    return Promise.resolve(
      new Response(JSON.stringify({ data: [pt(today(), 70)], gaps: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as unknown as typeof globalThis.fetch;
  const root = await mountUntil("Current:");
  expect(urls.at(-1)).toContain("days=30");

  const sevenDays = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "7d",
  ) as HTMLButtonElement;
  await act(async () => {
    fireEvent.click(sevenDays);
    await new Promise((r) => setTimeout(r, 20));
  });

  expect(urls.at(-1)).toContain("days=7");
  expect(sevenDays.className).toContain("text-brand");
});

// ---- Recharts chart: figure, gaps in the line, focusable markers ----

const { settle } = await import("@/src/test-utils/render.tsx");

// Put the prototype back exactly as it was: leaving an own property behind
// would shadow stubs other test files set on Element.prototype.
const ownRect = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  "getBoundingClientRect",
);
afterEach(() => {
  if (ownRect) {
    Object.defineProperty(
      HTMLElement.prototype,
      "getBoundingClientRect",
      ownRect,
    );
  } else {
    delete (HTMLElement.prototype as Partial<HTMLElement>)
      .getBoundingClientRect;
  }
});

async function mountChart(body: unknown): Promise<HTMLElement> {
  serve(200, body);
  const root = (await mountUntil("Current:")) as HTMLElement;
  await settle();
  return root;
}

function marker(root: HTMLElement, kind: string, date: string): Element {
  const el = root.querySelector(
    `[data-testid="compliance-trend-marker-${kind}-${date}"]`,
  );
  expect(el).not.toBeNull();
  return el as Element;
}

function markerTip(root: HTMLElement): string | undefined {
  return root.querySelector('[data-testid="compliance-trend-marker-tip"]')
    ?.textContent;
}

test("the chart is a figure named for what it shows", async () => {
  const root = await mountChart({
    data: [pt(today(-2), 80), pt(today(-1), 82)],
    gaps: [],
  });
  const figure = root.querySelector('[role="figure"]');
  expect(figure).not.toBeNull();
  expect(figure?.getAttribute("aria-label")).toMatch(/compliance score/i);
  expect(figure?.querySelector("svg")).not.toBeNull();
});

test("a gap day breaks the line in two", async () => {
  const root = await mountChart({
    data: [
      pt(today(-5), 80),
      pt(today(-4), 82),
      pt(today(-2), 70),
      pt(today(-1), 71),
    ],
    gaps: [{ date: today(-3), reason: "unreachable" }],
  });
  const d = root.querySelector(".recharts-line-curve")?.getAttribute("d") ?? "";
  expect(d.match(/M/g)?.length).toBe(2);
});

test("the score line is drawn in the theme's chart line colour", async () => {
  const root = await mountChart({
    data: [pt(today(-2), 80), pt(today(-1), 82)],
  });
  const line = root.querySelector(".recharts-line-curve");
  expect(line?.getAttribute("stroke")).toBe("var(--accent)");
});

test("the gap marker names the reason when focused", async () => {
  const gapDay = today(-3);
  const root = await mountChart({
    data: [pt(today(-5), 80), pt(today(-4), 82), pt(today(-2), 70)],
    gaps: [{ date: gapDay, reason: "credentials_invalid" }],
  });
  expect(markerTip(root)).toBeUndefined();

  const el = marker(root, "gap", gapDay);
  expect(el.getAttribute("tabindex")).toBe("0");
  await act(async () => {
    fireEvent.focus(el);
  });
  expect(markerTip(root)).toContain(GAP_MESSAGES.credentials_invalid);
});

test("an identity-change marker shows its text on focus; Escape and blur close it", async () => {
  const sw = today(-1);
  const root = await mountChart({
    data: [pt(today(-3), 90), pt(sw, 50, { identityChanged: true })],
    gaps: [],
  });
  const el = marker(root, "identity-change", sw);
  expect(el.getAttribute("aria-label")).toContain(
    "background identity changed",
  );

  await act(async () => {
    fireEvent.focus(el);
  });
  expect(markerTip(root)).toContain("background identity changed");

  await act(async () => {
    fireEvent.keyDown(el, { key: "Escape" });
  });
  expect(markerTip(root)).toBeUndefined();

  await act(async () => {
    fireEvent.focus(el);
  });
  expect(markerTip(root)).toContain("background identity changed");
  await act(async () => {
    fireEvent.blur(el);
  });
  expect(markerTip(root)).toBeUndefined();
});

test("hover and tap open a partial marker's text", async () => {
  const d = today(-1);
  const root = await mountChart({
    data: [
      pt(today(-2), 80),
      pt(d, 70.5, { partial: true, unreadable: ["kyverno"] }),
    ],
    gaps: [],
  });
  const el = marker(root, "partial", d);
  await act(async () => {
    fireEvent.mouseEnter(el);
  });
  expect(markerTip(root)).toContain("Kyverno");
  expect(markerTip(root)).toContain("70.5%");
  await act(async () => {
    fireEvent.mouseLeave(el);
  });
  expect(markerTip(root)).toBeUndefined();
  await act(async () => {
    fireEvent.click(el);
  });
  expect(markerTip(root)).toContain("Kyverno");
});

test("the time axis labels days over the selected range", async () => {
  const root = await mountChart({ data: [pt(today(), 70)], gaps: [] });
  const labels = [...root.querySelectorAll("text")]
    .map((t) => t.textContent ?? "")
    .filter((s) => /^[A-Z][a-z]{2} \d{1,2}$/.test(s));
  expect(labels.length).toBeGreaterThanOrEqual(2);
  const yLabels = [...root.querySelectorAll("text")].map((t) => t.textContent);
  expect(yLabels).toContain("0%");
  expect(yLabels).toContain("100%");
});
