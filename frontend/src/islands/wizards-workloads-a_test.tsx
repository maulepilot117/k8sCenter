import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
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

const { default: ConfigMapWizard } = await import("./ConfigMapWizard.tsx");
const { default: DaemonSetWizard } = await import("./DaemonSetWizard.tsx");
const { default: DeploymentWizard } = await import("./DeploymentWizard.tsx");
const { default: ServiceWizard } = await import("./ServiceWizard.tsx");

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json([{ metadata: { name: "default" } }]);
    }
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

/** Types into a text-like field the way a keystroke does. */
function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

function click(el: HTMLElement) {
  fireEvent.click(el);
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

// ---------------------------------------------------------------- ConfigMap

function fillConfigMap(
  name = "app-config",
  key = "LOG_LEVEL",
  value = "debug",
) {
  type(body().getByPlaceholderText("e.g. my-config"), name);
  type(body().getByPlaceholderText("Key"), key);
  type(body().getByPlaceholderText("Value"), value);
}

test("ConfigMap: required fields then Next advances to the review step", async () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  body().getByText("Step 1 of 2", { exact: false });
  fillConfigMap();
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(body().queryByPlaceholderText("e.g. my-config")).toBeNull();
});

test("ConfigMap: an empty name shows its error and does not advance", async () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("configmap")).toBeUndefined();
});

test("ConfigMap: the review step posts the typed values", async () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  fillConfigMap("typed-name", "TYPED_KEY", "typed value");
  await next();
  const sent = await previewBody("configmap");
  expect(sent.name).toBe("typed-name");
  expect(sent.data).toEqual({ TYPED_KEY: "typed value" });
});

test("ConfigMap: typing marks the wizard dirty", () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. my-config"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("ConfigMap: a value without a key blocks Next with a field error", async () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-config"), "half-row");
  type(body().getByPlaceholderText("Value"), "orphan");
  await next();
  body().getByText("Key is required");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("configmap")).toBeUndefined();
});

test("ConfigMap: removing a row keeps the other rows' values", () => {
  render(<ConfigMapWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add entry" }));
  click(body().getByRole("button", { name: "+ Add entry" }));
  const keys = () =>
    body().getAllByPlaceholderText("Key") as HTMLInputElement[];
  const values = () =>
    body().getAllByPlaceholderText("Value") as HTMLTextAreaElement[];
  type(keys()[0], "first");
  type(values()[0], "1");
  type(keys()[1], "second");
  type(values()[1], "2");
  type(keys()[2], "third");
  type(values()[2], "3");
  click(body().getAllByTitle("Remove entry")[1]);
  expect(keys().map((k) => k.value)).toEqual(["first", "third"]);
  expect(values().map((v) => v.value)).toEqual(["1", "3"]);
});

// --------------------------------------------------------------- DaemonSet

function fillDaemonSet() {
  type(body().getByPlaceholderText("my-daemonset"), "node-agent");
  type(body().getByPlaceholderText("nginx:latest"), "agent:1.2");
}

test("DaemonSet: required fields then Next advances to the review step", async () => {
  render(<DaemonSetWizard onClose={() => {}} />);
  fillDaemonSet();
  await next();
  await waitFor(() =>
    expect(body().queryByPlaceholderText("my-daemonset")).toBeNull(),
  );
  expect(previewCall("daemonset")).toBeDefined();
});

test("DaemonSet: an empty image shows its error and does not advance", async () => {
  render(<DaemonSetWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-daemonset"), "node-agent");
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("my-daemonset");
  expect(previewCall("daemonset")).toBeUndefined();
});

test("DaemonSet: the review step posts the typed values", async () => {
  render(<DaemonSetWizard onClose={() => {}} />);
  fillDaemonSet();
  click(body().getByRole("button", { name: "+ Add Node Selector" }));
  type(body().getByPlaceholderText("kubernetes.io/os"), "disk");
  type(body().getByPlaceholderText("linux"), "ssd");
  type(body().getByPlaceholderText("1 or 25%"), "2");
  await next();
  const sent = await previewBody("daemonset");
  expect(sent.name).toBe("node-agent");
  expect((sent.container as { image: string }).image).toBe("agent:1.2");
  expect(sent.nodeSelector).toEqual({ disk: "ssd" });
  expect(sent.maxUnavailable).toBe("2");
});

test("DaemonSet: typing marks the wizard dirty", () => {
  render(<DaemonSetWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-daemonset"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("DaemonSet: removing a node selector row keeps the others", () => {
  render(<DaemonSetWizard onClose={() => {}} />);
  for (let i = 0; i < 3; i++) {
    click(body().getByRole("button", { name: "+ Add Node Selector" }));
  }
  const keys = () =>
    body().getAllByPlaceholderText("kubernetes.io/os") as HTMLInputElement[];
  type(keys()[0], "a");
  type(keys()[1], "b");
  type(keys()[2], "c");
  click(body().getAllByTitle("Remove selector")[1]);
  expect(keys().map((k) => k.value)).toEqual(["a", "c"]);
});

// -------------------------------------------------------------- Deployment

function fillDeploymentBasics() {
  type(body().getByPlaceholderText("my-deployment"), "web");
  type(body().getByPlaceholderText("nginx:1.25"), "nginx:1.27");
}

test("Deployment: required fields then Next advances through the steps", async () => {
  render(<DeploymentWizard onClose={() => {}} />);
  fillDeploymentBasics();
  await next();
  await waitFor(() =>
    expect(body().queryByPlaceholderText("my-deployment")).toBeNull(),
  );
  body().getByRole("button", { name: "+ Add Port" });
  await next();
  await waitFor(() => body().getByPlaceholderText("100m"));
});

test("Deployment: an empty image shows its error and does not advance", async () => {
  render(<DeploymentWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-deployment"), "web");
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("my-deployment");
});

test("Deployment: the review step posts the typed values", async () => {
  render(<DeploymentWizard onClose={() => {}} />);
  fillDeploymentBasics();
  await next();
  click(
    await waitFor(() => body().getByRole("button", { name: "+ Add Port" })),
  );
  type(body().getByPlaceholderText("80"), "8080");
  await next();
  await waitFor(() => body().getByPlaceholderText("100m"));
  type(body().getByPlaceholderText("100m"), "250m");
  await next();
  const sent = await previewBody("deployment");
  expect(sent.name).toBe("web");
  expect(sent.image).toBe("nginx:1.27");
  // The app label follows the typed name.
  expect(sent.labels).toEqual({ app: "web" });
  expect(sent.ports).toEqual([
    expect.objectContaining({ containerPort: 8080 }),
  ]);
  expect((sent.resources as { requestCpu: string }).requestCpu).toBe("250m");
});

test("Deployment: typing marks the wizard dirty", () => {
  render(<DeploymentWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-deployment"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Deployment: an out-of-range port blocks the Networking step", async () => {
  render(<DeploymentWizard onClose={() => {}} />);
  fillDeploymentBasics();
  await next();
  click(
    await waitFor(() => body().getByRole("button", { name: "+ Add Port" })),
  );
  type(body().getByPlaceholderText("80"), "70000");
  await next();
  body().getByText("Must be 1-65535");
  body().getByRole("button", { name: "+ Add Port" });
  expect(previewCall("deployment")).toBeUndefined();
});

// ----------------------------------------------------------------- Service

async function fillServiceBasics(name = "api") {
  type(body().getByPlaceholderText("my-service"), name);
  await next();
  await waitFor(() => body().getByRole("button", { name: "+ Add Port" }));
}

test("Service: required fields then Next advances through the steps", async () => {
  render(<ServiceWizard onClose={() => {}} />);
  body().getByText("Step 1 of 3", { exact: false });
  await fillServiceBasics();
  body().getByText("Step 2 of 3", { exact: false });
  await next();
  await waitFor(() => body().getByText("Step 3 of 3", { exact: false }));
});

test("Service: an empty name shows its error and does not advance", async () => {
  render(<ServiceWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Step 1 of 3", { exact: false });
});

test("Service: the review step posts the typed ports and the synced selector", async () => {
  render(<ServiceWizard onClose={() => {}} />);
  await fillServiceBasics("api");
  click(body().getByRole("button", { name: "+ Add Port" }));
  const names = body().getAllByPlaceholderText("http");
  const ports = body().getAllByPlaceholderText("80");
  type(names[1], "admin");
  type(ports[1], "9090");
  await next();
  const sent = await previewBody("service");
  expect(sent.name).toBe("api");
  expect(sent.selector).toEqual({ app: "api" });
  expect(sent.labels).toEqual({ app: "api" });
  expect(sent.ports).toEqual([
    expect.objectContaining({ port: 80 }),
    expect.objectContaining({ name: "admin", port: 9090, targetPort: 9090 }),
  ]);
});

test("Service: typing marks the wizard dirty", () => {
  render(<ServiceWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-service"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Service: an out-of-range port blocks Next and no port is dropped", async () => {
  render(<ServiceWizard onClose={() => {}} />);
  await fillServiceBasics();
  type(body().getByPlaceholderText("80"), "70000");
  await next();
  body().getByText("Must be 1-65535");
  body().getByText("Step 2 of 3", { exact: false });
  expect(previewCall("service")).toBeUndefined();
});

test("Service: removing every port blocks Next", async () => {
  render(<ServiceWizard onClose={() => {}} />);
  await fillServiceBasics();
  click(body().getByTitle("Remove port"));
  await next();
  body().getByText("At least one port is required");
  expect(previewCall("service")).toBeUndefined();
});
