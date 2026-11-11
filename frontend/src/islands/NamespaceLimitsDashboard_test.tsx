import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type {
  NamespaceLimits,
  NamespaceSummary,
  ThresholdStatus,
} from "@/lib/limits-types.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The namespace limits dashboard lists every namespace with its ResourceQuota
 * utilisation, filters by status and name, pages at 50, opens a detail panel
 * for one namespace (quotas and limit ranges) and offers the create wizard,
 * also when the page is opened with ?action=create.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: NamespaceLimitsDashboard } = await import(
  "./NamespaceLimitsDashboard.tsx"
);

let urls: string[] = [];
let summaries: NamespaceSummary[] = [];
let detail: NamespaceLimits | null = null;
let failDetail = false;

function ns(
  name: string,
  status: ThresholdStatus = "ok",
  hasQuota = true,
  pct = 40,
): NamespaceSummary {
  return {
    namespace: name,
    hasQuota,
    hasLimitRange: false,
    cpuUsedPercent: hasQuota ? pct : undefined,
    memoryUsedPercent: hasQuota ? pct : undefined,
    highestUtilization: hasQuota ? pct : 0,
    status,
    quotaCount: hasQuota ? 1 : 0,
    limitRangeCount: 0,
  };
}

function stubApi() {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    if (url.endsWith("/limits/status")) return json({ available: true });
    if (url.endsWith("/limits/namespaces")) return json(summaries);
    if (failDetail) return new Response("{}", { status: 500 });
    return json(detail);
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  urls = [];
  failDetail = false;
  summaries = [];
  detail = {
    namespace: "alpha",
    quotas: [
      {
        name: "compute",
        warnThreshold: 80,
        criticalThreshold: 90,
        utilization: {
          "requests.cpu": {
            used: "2",
            hard: "4",
            percentage: 50,
            status: "ok",
          },
        },
      },
    ],
    limitRanges: [
      {
        name: "defaults",
        limits: [
          { type: "Container", default: { cpu: "500m" }, max: { cpu: "2" } },
        ],
      },
    ],
  };
  setAccessToken("t");
  globalThis.history.replaceState({}, "", "/");
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  globalThis.history.replaceState({}, "", "/");
});

async function mount() {
  stubApi();
  const view = render(<NamespaceLimitsDashboard />);
  await settle();
  return view.container as HTMLElement;
}

const rowNames = (root: HTMLElement) =>
  [...root.querySelectorAll("tbody tr td:first-child")].map(
    (e) => e.textContent,
  );

const tileValue = (root: HTMLElement, label: string) => {
  const t = [...root.querySelectorAll("div")].find(
    (d) => d.children.length === 0 && d.textContent === label,
  );
  return t?.nextElementSibling?.textContent;
};

test("shows the KPI tiles and one row per namespace", async () => {
  summaries = [
    ns("alpha"),
    ns("beta", "warning", true, 85),
    ns("gamma", "critical", true, 97),
    ns("delta", "ok", false),
  ];
  const root = await mount();
  expect(urls).toContain("/api/v1/limits/status");
  expect(urls).toContain("/api/v1/limits/namespaces");
  expect(rowNames(root)).toEqual(["alpha", "beta", "gamma", "delta"]);
  expect(tileValue(root, "With Quota")).toBe("3");
  expect(tileValue(root, "Warning")).toBe("1");
  expect(tileValue(root, "Critical")).toBe("1");
  expect(tileValue(root, "No Quota")).toBe("1");
  expect(root.textContent).toContain("97.0%");
});

test("filters by status and by name", async () => {
  summaries = [
    ns("alpha"),
    ns("beta", "warning", true, 85),
    ns("gamma", "critical", true, 97),
    ns("delta", "ok", false),
  ];
  const root = await mount();
  const select = root.querySelector("select") as HTMLSelectElement;
  await act(async () => {
    fireEvent.change(select, { target: { value: "warning" } });
  });
  expect(rowNames(root)).toEqual(["beta"]);
  await act(async () => {
    fireEvent.change(select, { target: { value: "no-quota" } });
  });
  expect(rowNames(root)).toEqual(["delta"]);
  await act(async () => {
    fireEvent.change(select, { target: { value: "all" } });
  });
  const box = body().getByPlaceholderText("Search namespaces...");
  await act(async () => {
    fireEvent.change(box, { target: { value: "GAM" } });
  });
  expect(rowNames(root)).toEqual(["gamma"]);
  await act(async () => {
    fireEvent.change(box, { target: { value: "nothing" } });
  });
  expect(root.textContent).toContain("No namespaces found");
});

test("pages at 50 namespaces and a filter returns to page 1", async () => {
  summaries = Array.from({ length: 120 }, (_, i) =>
    ns(`ns-${String(i).padStart(3, "0")}`),
  );
  const root = await mount();
  expect(rowNames(root)).toHaveLength(50);
  expect(root.textContent).toContain("Page 1 of 3 (120 total)");
  expect(
    (body().getByRole("button", { name: "Previous" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Next" }));
  });
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Next" }));
  });
  expect(root.textContent).toContain("Page 3 of 3");
  expect(rowNames(root)).toHaveLength(20);
  expect(
    (body().getByRole("button", { name: "Next" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);

  const box = body().getByPlaceholderText("Search namespaces...");
  await act(async () => {
    fireEvent.change(box, { target: { value: "ns-01" } });
  });
  expect(rowNames(root)).toHaveLength(10);
  expect(root.textContent).not.toContain("Page 3");
});

test("clicking a row opens the detail panel and clicking it again closes it", async () => {
  summaries = [ns("alpha"), ns("beta")];
  const root = await mount();
  await act(async () => {
    fireEvent.click(root.querySelectorAll("tbody tr")[0]);
  });
  await settle();
  expect(urls).toContain("/api/v1/limits/namespaces/alpha");
  expect(root.textContent).toContain("Namespace Details");
  expect(root.textContent).toContain("ResourceQuotas (1)");
  expect(root.textContent).toContain("requests.cpu: 2/4");
  expect(root.textContent).toContain("LimitRanges (1)");
  expect(root.textContent).toContain("cpu: 500m");
  expect(root.textContent).toContain("80% / 90%");

  await act(async () => {
    fireEvent.click(root.querySelectorAll("tbody tr")[1]);
  });
  await settle();
  expect(urls).toContain("/api/v1/limits/namespaces/beta");

  await act(async () => {
    fireEvent.click(root.querySelectorAll("tbody tr")[1]);
  });
  expect(root.textContent).not.toContain("Namespace Details");
});

test("the close button hides the panel", async () => {
  summaries = [ns("alpha")];
  const root = await mount();
  await act(async () => {
    fireEvent.click(root.querySelectorAll("tbody tr")[0]);
  });
  await settle();
  await act(async () => {
    fireEvent.click(body().getByLabelText("Close panel"));
  });
  expect(root.textContent).not.toContain("Namespace Details");
});

test("a failed detail request shows an error in the panel", async () => {
  summaries = [ns("alpha")];
  const root = await mount();
  failDetail = true;
  await act(async () => {
    fireEvent.click(root.querySelectorAll("tbody tr")[0]);
  });
  await settle();
  expect(root.textContent).toContain("Failed to load namespace details");
});

test("Refresh reloads the status and the list", async () => {
  summaries = [ns("alpha")];
  const root = await mount();
  const before = urls.length;
  summaries = [ns("alpha"), ns("beta")];
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  });
  await settle();
  expect(urls.length).toBe(before + 2);
  expect(rowNames(root)).toEqual(["alpha", "beta"]);
});

test("shows an error banner when the list fails to load", async () => {
  globalThis.fetch = (async () =>
    new Response("{}", { status: 500 })) as unknown as typeof fetch;
  const view = render(<NamespaceLimitsDashboard />);
  await settle();
  expect(view.container.textContent).toContain(
    "Failed to load namespace limits data",
  );
  expect(view.container.querySelector("table")).toBeNull();
});

test("Create Limits opens the wizard", async () => {
  summaries = [ns("alpha")];
  const root = await mount();
  expect(root.textContent).not.toContain("Create Namespace Limits");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Create Limits" }));
  });
  expect(document.body.textContent).toContain("Create Namespace Limits");
});

test("?action=create opens the wizard and removes the parameter", async () => {
  summaries = [ns("alpha")];
  globalThis.history.replaceState({}, "", "/?action=create&keep=1");
  await mount();
  expect(document.body.textContent).toContain("Create Namespace Limits");
  expect(globalThis.location.search).toBe("?keep=1");
});
