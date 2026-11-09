/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { getAccessToken, setAccessToken } from "@/lib/api.ts";
import { PRESETS } from "@/lib/namespace-presets.ts";
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

const { default: HPAWizard } = await import("./HPAWizard.tsx");
const { default: PDBWizard } = await import("./PDBWizard.tsx");
const { default: NamespaceLimitsWizard } = await import(
  "./NamespaceLimitsWizard.tsx"
);
const { default: SetupWizard } = await import("./SetupWizard.tsx");

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  credentials?: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
/** When set, the setup/init request waits on this before answering. */
let holdInit: Promise<void> | null = null;
/** When set, the setup/init request answers with this error message. */
let initError: string | null = null;

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
  holdInit = null;
  initError = null;
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: init?.method ?? "GET",
      headers: (init?.headers ?? {}) as Record<string, string>,
      credentials: init?.credentials,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json({ data: [{ metadata: { name: "default" } }] });
    }
    if (url.endsWith("/v1/setup/init")) {
      if (holdInit) await holdInit;
      if (initError) return json({ error: { message: initError } }, 400);
      return json({ data: {} });
    }
    if (url.endsWith("/v1/auth/login")) {
      return json({ data: { accessToken: "issued-access-token" } });
    }
    if (url.endsWith("/v1/settings")) {
      return json({ data: {} });
    }
    return json({ data: { yaml: "kind: Preview\n" } });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

/** Types into a text-like field the way a keystroke does. */
function type(el: HTMLElement | Element, value: string) {
  fireEvent.input(el, { target: { value } });
}

function pick(el: HTMLElement | Element, value: string) {
  fireEvent.change(el, { target: { value } });
}

function click(el: HTMLElement | Element) {
  fireEvent.click(el);
}

function all<T extends Element>(selector: string): T[] {
  return Array.from(document.body.querySelectorAll<T>(selector));
}

async function next() {
  await act(async () => {
    click(body().getByRole("button", { name: /^Continue/ }));
  });
}

function previewCall(kind: string): Call | undefined {
  return calls.find((c) => c.url.endsWith(`/v1/wizards/${kind}/preview`));
}

async function previewBody(kind: string) {
  await waitFor(() => expect(previewCall(kind)).toBeDefined());
  return previewCall(kind)?.body as Record<string, unknown>;
}

/** True when leaving the page would prompt: the wizard's dirty flag is set. */
function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

// --------------------------------------------------------------------- HPA

const DNS_ERROR =
  "Must be lowercase alphanumeric with hyphens, 1-63 characters";

function fillHpa(name = "web-hpa", target = "web") {
  type(body().getByPlaceholderText("e.g. my-app-hpa"), name);
  type(body().getByPlaceholderText("e.g. my-app"), target);
}

/** The Min and Max replica inputs, in document order. */
function replicaInputs() {
  const inputs = all<HTMLInputElement>("input[type=number]");
  return { min: inputs[0], max: inputs[1] };
}

test("HPA: required fields then Next advances to the review step", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa();
  await next();
  await waitFor(() =>
    expect(body().queryByPlaceholderText("e.g. my-app-hpa")).toBeNull(),
  );
  expect(previewCall("hpa")).toBeDefined();
});

test("HPA: empty name and target show their errors and do not advance", async () => {
  render(<HPAWizard onClose={() => {}} />);
  await next();
  expect(body().getAllByText(DNS_ERROR)).toHaveLength(2);
  body().getByPlaceholderText("e.g. my-app-hpa");
  expect(previewCall("hpa")).toBeUndefined();
});

test("HPA: the review step posts the typed values", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa("api-hpa", "api");
  const selects = all<HTMLSelectElement>("select");
  // Namespace, Kind, then the first metric's Resource and Target Type.
  pick(selects[1], "StatefulSet");
  pick(selects[2], "memory");
  pick(selects[3], "AverageValue");
  type(replicaInputs().min, "2");
  type(replicaInputs().max, "6");
  type(all<HTMLInputElement>("input[type=number]")[2], "300");
  await next();
  const sent = await previewBody("hpa");
  expect(sent.name).toBe("api-hpa");
  expect(sent.targetName).toBe("api");
  expect(sent.targetKind).toBe("StatefulSet");
  expect(sent.namespace).toBe(selects[0].value);
  expect(sent.minReplicas).toBe(2);
  expect(sent.maxReplicas).toBe(6);
  expect(sent.metrics).toEqual([
    {
      type: "Resource",
      resourceName: "memory",
      targetType: "AverageValue",
      targetAverageValue: 300,
    },
  ]);
});

test("HPA: typing marks the wizard dirty", () => {
  render(<HPAWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. my-app-hpa"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("HPA: min above max blocks Next with a field error", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa();
  type(replicaInputs().min, "8");
  type(replicaInputs().max, "5");
  await next();
  body().getByText("Min replicas cannot exceed max replicas");
  body().getByPlaceholderText("e.g. my-app-hpa");
  expect(previewCall("hpa")).toBeUndefined();
});

test("HPA: max above 1000 blocks Next", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa();
  type(replicaInputs().max, "1001");
  await next();
  body().getByText("Must be between 1 and 1000");
  expect(previewCall("hpa")).toBeUndefined();
});

test("HPA: a Utilization target above 100 blocks Next, AverageValue allows it", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa();
  type(all<HTMLInputElement>("input[type=number]")[2], "150");
  await next();
  body().getByText("Must be between 1 and 100 for Utilization (percentage)");
  expect(previewCall("hpa")).toBeUndefined();
  pick(all<HTMLSelectElement>("select")[3], "AverageValue");
  await next();
  expect((await previewBody("hpa")).metrics).toEqual([
    expect.objectContaining({
      targetType: "AverageValue",
      targetAverageValue: 150,
    }),
  ]);
});

test("HPA: metric rows add and remove, and removing the last restores one default row", async () => {
  render(<HPAWizard onClose={() => {}} />);
  fillHpa();
  click(body().getByRole("button", { name: "+ Add metric" }));
  expect(body().getAllByTitle("Remove metric")).toHaveLength(2);
  // Row 2 becomes Memory; removing row 1 leaves it.
  pick(all<HTMLSelectElement>("select")[4], "memory");
  click(body().getAllByTitle("Remove metric")[0]);
  const remaining = all<HTMLSelectElement>("select");
  expect(remaining[2].value).toBe("memory");
  // Removing the only row puts a fresh CPU row back.
  click(body().getByTitle("Remove metric"));
  expect(all<HTMLSelectElement>("select")[2].value).toBe("cpu");
  expect(body().getAllByTitle("Remove metric")).toHaveLength(1);
  await next();
  expect((await previewBody("hpa")).metrics).toEqual([
    expect.objectContaining({ resourceName: "cpu", targetType: "Utilization" }),
  ]);
});

// --------------------------------------------------------------------- PDB

function fillPdb(name = "web-pdb", key = "app", value = "web") {
  type(body().getByPlaceholderText("e.g. my-app-pdb"), name);
  type(body().getByPlaceholderText("Key"), key);
  type(body().getByPlaceholderText("Value"), value);
}

test("PDB: required fields then Next advances to the review step", async () => {
  render(<PDBWizard onClose={() => {}} />);
  fillPdb();
  await next();
  await waitFor(() =>
    expect(body().queryByPlaceholderText("e.g. my-app-pdb")).toBeNull(),
  );
  expect(previewCall("pdb")).toBeDefined();
});

test("PDB: an empty name and selector show their errors and do not advance", async () => {
  render(<PDBWizard onClose={() => {}} />);
  await next();
  body().getByText(DNS_ERROR);
  body().getByText(
    "At least one selector label with a non-empty key is required",
  );
  expect(previewCall("pdb")).toBeUndefined();
});

test("PDB: the review step posts minAvailable by default", async () => {
  render(<PDBWizard onClose={() => {}} />);
  fillPdb("typed-pdb", "tier", "frontend");
  type(body().getByPlaceholderText("e.g. 1 or 50%"), "2");
  await next();
  const sent = await previewBody("pdb");
  expect(sent.name).toBe("typed-pdb");
  expect(sent.selector).toEqual({ tier: "frontend" });
  expect(sent.minAvailable).toBe("2");
  expect("maxUnavailable" in sent).toBe(false);
});

test("PDB: choosing Max Unavailable posts maxUnavailable and not minAvailable", async () => {
  render(<PDBWizard onClose={() => {}} />);
  fillPdb();
  click(body().getByLabelText("Max Unavailable"));
  type(body().getByPlaceholderText("e.g. 1 or 50%"), "50%");
  await next();
  const sent = await previewBody("pdb");
  expect(sent.maxUnavailable).toBe("50%");
  expect("minAvailable" in sent).toBe(false);
});

test("PDB: typing marks the wizard dirty", () => {
  render(<PDBWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. my-app-pdb"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("PDB: a budget that is neither a number nor a percentage blocks Next", async () => {
  render(<PDBWizard onClose={() => {}} />);
  fillPdb();
  type(body().getByPlaceholderText("e.g. 1 or 50%"), "lots");
  await next();
  body().getByText("Enter a number (e.g. 2) or percentage (e.g. 50%)", {
    selector: "p.text-danger",
  });
  expect(previewCall("pdb")).toBeUndefined();
});

test("PDB: a value without a key does not count as a selector label", async () => {
  render(<PDBWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-app-pdb"), "orphan-pdb");
  type(body().getByPlaceholderText("Value"), "orphan");
  await next();
  body().getByText(
    "At least one selector label with a non-empty key is required",
  );
  expect(previewCall("pdb")).toBeUndefined();
});

test("PDB: removing a selector row keeps the other rows' values", () => {
  render(<PDBWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add label" }));
  click(body().getByRole("button", { name: "+ Add label" }));
  const keys = () =>
    body().getAllByPlaceholderText("Key") as HTMLInputElement[];
  type(keys()[0], "a");
  type(keys()[1], "b");
  type(keys()[2], "c");
  click(body().getAllByTitle("Remove label")[1]);
  expect(keys().map((k) => k.value)).toEqual(["a", "c"]);
});

// --------------------------------------------------------- NamespaceLimits

async function toStep(n: number) {
  for (let i = 1; i < n; i++) await next();
}

test("NamespaceLimits: the defaults advance through each step to the review", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  body().getByText("Step 1 of 4", { exact: false });
  await next();
  await waitFor(() => body().getByText("Step 2 of 4", { exact: false }));
  await next();
  await waitFor(() => body().getByText("Step 3 of 4", { exact: false }));
  await next();
  await waitFor(() => body().getByText("Step 4 of 4", { exact: false }));
  expect(previewCall("namespace-limits")).toBeDefined();
});

test("NamespaceLimits: an empty quota name shows its error and does not advance", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. default-quota"), "");
  await next();
  body().getByText("Must be lowercase alphanumeric with hyphens, 1-63 chars");
  body().getByText("Step 1 of 4", { exact: false });
});

test("NamespaceLimits: an empty CPU quota blocks the quota step", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  await toStep(2);
  type(body().getByPlaceholderText("e.g. 8 or 8000m"), "");
  await next();
  body().getByText("CPU limit is required");
  body().getByText("Step 2 of 4", { exact: false });
});

test("NamespaceLimits: an empty default CPU blocks the LimitRange step", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  await toStep(3);
  type(body().getAllByPlaceholderText("e.g. 250m")[0], "");
  await next();
  body().getByText("Required");
  body().getByText("Step 3 of 4", { exact: false });
  expect(previewCall("namespace-limits")).toBeUndefined();
});

test("NamespaceLimits: the review step posts the typed quota and limits", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. default-quota"), "team-quota");
  type(body().getByPlaceholderText("e.g. default-limits"), "team-limits");
  await next();
  type(body().getByPlaceholderText("e.g. 8 or 8000m"), "12");
  type(body().getByPlaceholderText("e.g. 16Gi"), "20Gi");
  await next();
  type(body().getAllByPlaceholderText("e.g. 250m")[0], "300m");
  await next();
  const sent = await previewBody("namespace-limits");
  expect(sent.quotaName).toBe("team-quota");
  expect(sent.limitRangeName).toBe("team-limits");
  const quota = sent.quota as { cpuHard: string; memoryHard: string };
  expect(quota.cpuHard).toBe("12");
  expect(quota.memoryHard).toBe("20Gi");
  const limits = sent.limits as { containerDefault: { cpu: string } };
  expect(limits.containerDefault.cpu).toBe("300m");
});

test("NamespaceLimits: applying a preset replaces the quota values", async () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: /^Large/ }));
  await toStep(4);
  const sent = await previewBody("namespace-limits");
  const quota = sent.quota as {
    cpuHard: string;
    memoryHard: string;
    podsHard: number;
  };
  expect(quota).toMatchObject({
    cpuHard: PRESETS.large.quota.cpuHard,
    memoryHard: PRESETS.large.quota.memoryHard,
    podsHard: PRESETS.large.quota.podsHard,
  });
});

test("NamespaceLimits: typing marks the wizard dirty", () => {
  render(<NamespaceLimitsWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. default-quota"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

// ------------------------------------------------------------------- Setup

const SECRET_PASSWORD = "s3cret-pw-value";
const SECRET_TOKEN = "tok-setup-abc123";

async function openAdminStep() {
  render(<SetupWizard />);
  click(body().getByRole("button", { name: "Get Started" }));
  await waitFor(() => body().getByText("Create Admin Account"));
}

function adminFields() {
  const text = all<HTMLInputElement>("input[type=text]");
  const pw = all<HTMLInputElement>("input[type=password]");
  return { user: text[0], pass: pw[0], confirm: pw[1], token: pw[2] };
}

function fillAdmin(confirm = SECRET_PASSWORD) {
  const f = adminFields();
  type(f.user, "root-admin");
  type(f.pass, SECRET_PASSWORD);
  type(f.confirm, confirm);
  type(f.token, SECRET_TOKEN);
}

const createButton = () =>
  body().getByRole("button", { name: /Create Account/ }) as HTMLButtonElement;

function spyConsole() {
  const seen: string[] = [];
  const saved: Record<string, unknown> = {};
  for (const level of ["log", "info", "warn", "error", "debug"] as const) {
    saved[level] = console[level];
    console[level] = (...args: unknown[]) => {
      seen.push(args.map((a) => String(a)).join(" "));
    };
  }
  return {
    seen,
    restore() {
      for (const level of ["log", "info", "warn", "error", "debug"] as const) {
        console[level] = saved[level] as typeof console.log;
      }
    },
  };
}

test("Setup: the welcome step leads to the admin account form", async () => {
  render(<SetupWizard />);
  body().getByText("Welcome to k8sCenter");
  click(body().getByRole("button", { name: "Get Started" }));
  await waitFor(() => body().getByText("Create Admin Account"));
});

test("Setup: Create Account stays disabled until a username and password are typed", async () => {
  await openAdminStep();
  expect(createButton().disabled).toBe(true);
  type(adminFields().user, "root-admin");
  expect(createButton().disabled).toBe(true);
  type(adminFields().pass, SECRET_PASSWORD);
  expect(createButton().disabled).toBe(false);
});

test("Setup: a mismatched password confirmation blocks submit", async () => {
  await openAdminStep();
  fillAdmin("something-else-entirely");
  await act(async () => {
    click(createButton());
  });
  body().getByText("Passwords do not match");
  expect(calls.filter((c) => c.url.includes("/setup/init"))).toHaveLength(0);
  body().getByText("Create Admin Account");
});

test("Setup: a password under 8 characters blocks submit", async () => {
  await openAdminStep();
  const f = adminFields();
  type(f.user, "root-admin");
  type(f.pass, "short");
  type(f.confirm, "short");
  await act(async () => {
    click(createButton());
  });
  body().getByText("Password must be at least 8 characters");
  expect(calls.filter((c) => c.url.includes("/setup/init"))).toHaveLength(0);
});

test("Setup: the request carries the typed username and token, keeps secrets out of URLs and logs, then advances", async () => {
  const spy = spyConsole();
  try {
    await openAdminStep();
    fillAdmin();
    await act(async () => {
      click(createButton());
    });
    await waitFor(() => body().getByText("Monitoring (Optional)"));

    const init = calls.find((c) => c.url === "/api/v1/setup/init");
    expect(init).toBeDefined();
    expect(init?.method).toBe("POST");
    expect(init?.headers["X-Requested-With"]).toBe("XMLHttpRequest");
    expect(init?.body).toEqual({
      username: "root-admin",
      password: SECRET_PASSWORD,
      setupToken: SECRET_TOKEN,
    });

    const login = calls.find((c) => c.url === "/api/v1/auth/login");
    expect(login?.credentials).toBe("include");
    expect(login?.body).toEqual({
      username: "root-admin",
      password: SECRET_PASSWORD,
    });
    expect(getAccessToken()).toBe("issued-access-token");

    for (const call of calls) {
      expect(call.url).not.toContain(SECRET_PASSWORD);
      expect(call.url).not.toContain(SECRET_TOKEN);
    }
    expect(globalThis.location.href).not.toContain(SECRET_PASSWORD);
    expect(globalThis.location.href).not.toContain(SECRET_TOKEN);
    for (const line of spy.seen) {
      expect(line).not.toContain(SECRET_PASSWORD);
      expect(line).not.toContain(SECRET_TOKEN);
    }
  } finally {
    spy.restore();
  }
});

test("Setup: a double-click on Create Account sends one setup request", async () => {
  await openAdminStep();
  fillAdmin();
  let release: () => void = () => {};
  holdInit = new Promise<void>((resolve) => {
    release = resolve;
  });
  await act(async () => {
    const btn = createButton();
    click(btn);
    click(btn);
  });
  expect(calls.filter((c) => c.url === "/api/v1/setup/init")).toHaveLength(1);
  await act(async () => {
    release();
  });
  await waitFor(() => body().getByText("Monitoring (Optional)"));
  expect(calls.filter((c) => c.url === "/api/v1/setup/init")).toHaveLength(1);
});

test("Setup: a rejected setup request shows the server message and can be retried", async () => {
  await openAdminStep();
  fillAdmin();
  initError = "Setup token is invalid";
  await act(async () => {
    click(createButton());
  });
  await waitFor(() => body().getByText("Setup token is invalid"));
  body().getByText("Create Admin Account");
  expect(createButton().disabled).toBe(false);
  initError = null;
  await act(async () => {
    click(createButton());
  });
  await waitFor(() => body().getByText("Monitoring (Optional)"));
  expect(calls.filter((c) => c.url === "/api/v1/setup/init")).toHaveLength(2);
});

test("Setup: saving monitoring and skipping alerting is reflected in the review", async () => {
  await openAdminStep();
  fillAdmin();
  await act(async () => {
    click(createButton());
  });
  await waitFor(() => body().getByText("Monitoring (Optional)"));
  type(
    body().getByPlaceholderText("http://prometheus:9090"),
    "http://prom.example:9090",
  );
  await act(async () => {
    click(body().getByRole("button", { name: /Save & Continue/ }));
  });
  await waitFor(() => body().getByText("Alerting (Optional)"));
  const put = calls.find((c) => c.url.endsWith("/v1/settings"));
  expect(put?.method).toBe("PUT");
  expect(put?.body).toMatchObject({
    monitoringPrometheusUrl: "http://prom.example:9090",
  });
  click(body().getByRole("button", { name: "Skip" }));
  await waitFor(() => body().getByText("Setup Complete"));
  body().getByText("Created (root-admin)");
  body().getByText("Configured");
  body().getByText("Skipped");
});
