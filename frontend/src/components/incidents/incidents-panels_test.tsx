/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { CapturePanel } = await import("./CapturePanel.tsx");
const { ExportMenu } = await import("./ExportMenu.tsx");
const { GrantsPanel } = await import("./GrantsPanel.tsx");

const ID = "00000000-0000-4000-8000-000000000001";

interface Call {
  url: string;
  method: string;
  body?: string;
}
type Route = (call: Call) => Response | Promise<Response> | undefined;

let calls: Call[] = [];

const json = (status: number, payload: unknown, headers: HeadersInit = {}) =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });

function stubFetch(...routes: Route[]) {
  calls = [];
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? init.body : undefined,
    };
    calls.push(call);
    for (const r of routes) {
      const res = r(call);
      if (res) return Promise.resolve(res);
    }
    return Promise.resolve(
      json(500, { error: { code: 500, message: "unrouted" } }),
    );
  }) as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
});
afterEach(() => {
  globalThis.fetch = bunFetch;
});

const flush = () =>
  act(async () => {
    for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0));
  });

const CAPTURED = {
  inserted: 2,
  deduplicated: 0,
  dropped: 0,
  completeness: "complete",
  sources: [{ id: "object", completeness: "complete", items: 2 }],
};

function fillCapture(ui: ReturnType<typeof render>) {
  fireEvent.input(ui.getByLabelText("Namespace"), {
    target: { value: "shop" },
  });
  fireEvent.input(ui.getByLabelText("Name"), { target: { value: "web" } });
}

const captureCalls = () => calls.filter((c) => c.url.endsWith("/capture"));

test("one capture click sends exactly one request with the typed target", async () => {
  stubFetch((c) =>
    c.url.endsWith("/capture") ? json(200, { data: CAPTURED }) : undefined,
  );
  let captured = 0;
  const ui = render(
    <CapturePanel
      incidentId={ID}
      closed={false}
      onCaptured={() => {
        captured++;
      }}
    />,
  );
  fillCapture(ui);
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Capture" }));
  });
  await flush();

  expect(captureCalls()).toHaveLength(1);
  const sent = JSON.parse(captureCalls()[0]?.body ?? "{}");
  expect(sent).toMatchObject({
    namespace: "shop",
    kind: "Deployment",
    name: "web",
  });
  expect(sent.sources.length).toBeGreaterThan(0);
  expect(captured).toBe(1);
  expect(body().getByText(/2 new items recorded/)).toBeTruthy();
});

test("a second click while a capture is in flight sends no second request", async () => {
  let release: (r: Response) => void = () => {};
  stubFetch((c) =>
    c.url.endsWith("/capture")
      ? (new Promise<Response>((res) => {
          release = res;
        }) as unknown as Response)
      : undefined,
  );
  const ui = render(
    <CapturePanel incidentId={ID} closed={false} onCaptured={() => {}} />,
  );
  fillCapture(ui);
  // Both clicks land before any render, as a fast double click does.
  const btn = ui.getByRole("button", { name: "Capture" });
  await act(async () => {
    fireEvent.click(btn);
    fireEvent.click(btn);
  });
  expect(captureCalls()).toHaveLength(1);
  await act(async () => {
    release(json(200, { data: CAPTURED }));
  });
  await flush();
  expect(body().getByText(/2 new items recorded/)).toBeTruthy();
});

test("a retry after a failed capture resends the same request and clears the error", async () => {
  let attempt = 0;
  stubFetch((c) => {
    if (!c.url.endsWith("/capture")) return undefined;
    attempt++;
    return attempt === 1
      ? json(503, {
          error: {
            code: 503,
            message: "x",
            reason: "incident_capture_outcome_unknown",
          },
        })
      : json(200, { data: CAPTURED });
  });
  let captured = 0;
  const ui = render(
    <CapturePanel
      incidentId={ID}
      closed={false}
      onCaptured={() => {
        captured++;
      }}
    />,
  );
  fillCapture(ui);
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Capture" }));
  });
  await flush();
  expect(body().getByText(/may or may not have been recorded/)).toBeTruthy();
  expect(captured).toBe(1);

  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Capture" }));
  });
  await flush();
  expect(captureCalls()).toHaveLength(2);
  expect(captureCalls()[1]?.body).toBe(captureCalls()[0]?.body);
  expect(body().queryByText(/may or may not have been recorded/)).toBeNull();
  expect(body().getByText(/2 new items recorded/)).toBeTruthy();
});

test("capture refuses an empty target without a request", async () => {
  stubFetch();
  const ui = render(
    <CapturePanel incidentId={ID} closed={false} onCaptured={() => {}} />,
  );
  await act(async () => {
    fireEvent.submit(
      ui.getByRole("button", { name: "Capture" }).closest("form") as Element,
    );
  });
  expect(body().getByText(/Enter the namespace and name/)).toBeTruthy();
  expect(calls).toHaveLength(0);
});

const grant = (id: string, canAnnotate = false) => ({
  incidentId: ID,
  granteeId: id,
  canAnnotate,
  createdAt: "2026-10-01T09:00:00Z",
});

test("granting adds the grantee to the list", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.endsWith("/grants")
        ? json(200, { data: [grant("bob")] })
        : undefined,
    (c) =>
      c.method === "POST" && c.url.endsWith("/grants")
        ? json(200, { data: grant("carol", true) })
        : undefined,
  );
  const ui = render(<GrantsPanel incidentId={ID} />);
  await flush();
  expect(body().getByText("bob")).toBeTruthy();

  fireEvent.input(ui.getByLabelText("User id"), {
    target: { value: " carol " },
  });
  const boxes = ui.getAllByLabelText("Can add notes");
  fireEvent.click(boxes[boxes.length - 1] as HTMLElement);
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Share" }));
  });
  await flush();

  const post = calls.find((c) => c.method === "POST");
  expect(JSON.parse(post?.body ?? "{}")).toEqual({
    granteeId: "carol",
    canAnnotate: true,
  });
  expect(body().getByText("carol")).toBeTruthy();
  expect(body().getByText("bob")).toBeTruthy();
  expect(body().getByText("Shared with carol.")).toBeTruthy();
});

test("a failed revoke shows the error and keeps the grantee", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.endsWith("/grants")
        ? json(200, { data: [grant("bob")] })
        : undefined,
    (c) =>
      c.method === "DELETE"
        ? json(500, { error: { code: 500, message: "boom" } })
        : undefined,
  );
  const ui = render(<GrantsPanel incidentId={ID} />);
  await flush();
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Remove" }));
  });
  await flush();

  const del = calls.find((c) => c.method === "DELETE");
  expect(del?.url).toContain("/grants/bob");
  expect(body().getByText("The change could not be saved.")).toBeTruthy();
  expect(body().getByText("bob")).toBeTruthy();
  expect(body().queryByText(/Stopped sharing/)).toBeNull();
});

test("a successful revoke removes the grantee", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.endsWith("/grants")
        ? json(200, { data: [grant("bob")] })
        : undefined,
    (c) =>
      c.method === "DELETE" ? new Response(null, { status: 204 }) : undefined,
  );
  const ui = render(<GrantsPanel incidentId={ID} />);
  await flush();
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "Remove" }));
  });
  await flush();
  expect(body().queryByText("bob")).toBeNull();
  expect(body().getByText("Stopped sharing with bob.")).toBeTruthy();
});

test("the export menu fetches the json and markdown exports", async () => {
  const realCreate = URL.createObjectURL;
  const realRevoke = URL.revokeObjectURL;
  URL.createObjectURL = () => "blob:x";
  URL.revokeObjectURL = () => {};
  try {
    stubFetch((c) =>
      c.url.includes("/export")
        ? new Response("{}", {
            status: 200,
            headers: {
              "Content-Disposition": 'attachment; filename="inc.json"',
            },
          })
        : undefined,
    );
    const ui = render(<ExportMenu incidentId={ID} />);
    expect(ui.getByRole("group", { name: "Export" })).toBeTruthy();
    await act(async () => {
      fireEvent.click(ui.getByRole("button", { name: "JSON" }));
    });
    await flush();
    await act(async () => {
      fireEvent.click(ui.getByRole("button", { name: "Markdown" }));
    });
    await flush();
    const urls = calls.map((c) => c.url);
    expect(urls[0]).toContain(`/incidents/${ID}/export?format=json`);
    expect(urls[1]).toContain(`/incidents/${ID}/export?format=markdown`);
  } finally {
    URL.createObjectURL = realCreate;
    URL.revokeObjectURL = realRevoke;
  }
});

test("a failed export shows the rate-limit message and re-enables the buttons", async () => {
  stubFetch((c) =>
    c.url.includes("/export")
      ? json(429, { error: { code: 429, message: "slow down" } })
      : undefined,
  );
  const ui = render(<ExportMenu incidentId={ID} />);
  await act(async () => {
    fireEvent.click(ui.getByRole("button", { name: "JSON" }));
  });
  await waitFor(() => body().getByText(/rate-limited/));
  expect(
    ui.getByRole("button", { name: "JSON" }).getAttribute("aria-disabled"),
  ).toBe("false");
});
