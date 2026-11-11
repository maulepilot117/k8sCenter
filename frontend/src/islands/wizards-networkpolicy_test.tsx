import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
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

// The review step's editor loads Monaco from a CDN; serve an inert stand-in.
const MONACO_CDN =
  "https://esm.sh/monaco-editor@0.52.2/esm/vs/editor/editor.api.js";
mock.module(MONACO_CDN, () => ({
  editor: {
    create: () => ({
      onDidChangeModelContent: () => {},
      getValue: () => "",
      getModel: () => null,
      updateOptions: () => {},
      dispose: () => {},
    }),
    setModelMarkers: () => {},
  },
}));

const { default: NetworkPolicyWizard } = await import(
  "./NetworkPolicyWizard.tsx"
);

interface Call {
  url: string;
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
    const raw = typeof init?.body === "string" ? init.body : "";
    let parsed: Record<string, unknown> | null = null;
    try {
      parsed = raw ? JSON.parse(raw) : null;
    } catch {
      parsed = null;
    }
    calls.push({ url, body: parsed });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json([{ metadata: { name: "default" } }]);
    }
    return json({ yaml: "kind: NetworkPolicy\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
  mock.module(MONACO_CDN, () => ({}));
});

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

function previewCall(): Call | undefined {
  return calls.find((c) => c.url.endsWith("/v1/wizards/networkpolicy/preview"));
}

async function previewBody() {
  await waitFor(() => expect(previewCall()).toBeDefined());
  return previewCall()?.body as Record<string, unknown>;
}

function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

function nameInput(): HTMLElement {
  return body().getByPlaceholderText("my-network-policy");
}

/** Fills the basics step and moves to the rules step. */
async function toRules(opts: { egress?: boolean; ingress?: boolean } = {}) {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  type(nameInput(), "allow-web");
  if (opts.egress) click(body().getByRole("checkbox", { name: "Egress" }));
  if (opts.ingress === false) {
    click(body().getByRole("checkbox", { name: "Ingress" }));
  }
  await next();
  body().getByText("Step 2 of 3", { exact: false });
}

/** The peer-type selects of every peer on the rules step, in document order. */
function peerTypeSelects(): HTMLSelectElement[] {
  return [...document.querySelectorAll("select")].filter((s) =>
    [...s.options].some((o) => o.textContent === "IP Block (CIDR)"),
  ) as HTMLSelectElement[];
}

function cidrInputs(): HTMLInputElement[] {
  return body().queryAllByPlaceholderText("10.0.0.0/8") as HTMLInputElement[];
}

function keyInputs(): HTMLInputElement[] {
  return body().queryAllByPlaceholderText("key") as HTMLInputElement[];
}

function valueInputs(): HTMLInputElement[] {
  return body().queryAllByPlaceholderText("value") as HTMLInputElement[];
}

test("Basics: a valid name then Next advances to the rules step", async () => {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  body().getByText("Step 1 of 3", { exact: false });
  type(nameInput(), "allow-web");
  await next();
  body().getByText("Step 2 of 3", { exact: false });
  body().getByText("Ingress Rules");
  expect(body().queryByPlaceholderText("my-network-policy")).toBeNull();
});

test("Basics: an empty name shows its error and does not advance", async () => {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Step 1 of 3", { exact: false });
});

test("Basics: clearing every policy type blocks Next", async () => {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  type(nameInput(), "allow-web");
  click(body().getByRole("checkbox", { name: "Ingress" }));
  await next();
  body().getByText("At least one policy type is required");
  body().getByText("Step 1 of 3", { exact: false });
});

test("Basics: typing marks the wizard dirty", () => {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(nameInput(), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Rules: the sections follow the chosen policy types", async () => {
  await toRules({ egress: true, ingress: false });
  expect(body().queryByText("Ingress Rules")).toBeNull();
  body().getByText("Egress Rules");
  body().getByText("No rules — all egress traffic is denied by default.");
});

test("Rules: the review posts the typed pod selector, rules, peers and ports", async () => {
  render(<NetworkPolicyWizard onClose={() => {}} />);
  type(nameInput(), "typed-policy");
  click(body().getByRole("button", { name: "+ Add Label" }));
  type(body().getByPlaceholderText("key"), "app");
  type(body().getByPlaceholderText("value"), "web");
  click(body().getByRole("checkbox", { name: "Egress" }));
  await next();

  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  // The new peer is a pod selector with one blank label row.
  type(keyInputs()[0], "tier");
  type(valueInputs()[0], "frontend");
  click(body().getByRole("button", { name: "+ Add Port" }));
  type(body().getByPlaceholderText("Port (e.g. 80)"), "8443");
  fireEvent.change(
    [...document.querySelectorAll("select")].find((s) =>
      [...s.options].some((o) => o.textContent === "SCTP"),
    ) as HTMLSelectElement,
    { target: { value: "UDP" } },
  );

  click(body().getByRole("button", { name: "+ Add Egress Rule" }));
  // Second "+ Add Peer" belongs to the egress rule.
  click(body().getAllByRole("button", { name: "+ Add Peer" })[1]);
  fireEvent.change(peerTypeSelects()[1], { target: { value: "ipBlock" } });
  type(cidrInputs()[0], "10.1.0.0/16");
  click(body().getByRole("button", { name: "+ Add Except" }));
  type(body().getByPlaceholderText("192.168.0.0/24"), "10.1.5.0/24");

  await next();
  const sent = await previewBody();
  expect(sent.name).toBe("typed-policy");
  expect(sent.namespace).toBe("default");
  expect(sent.podSelector).toEqual({ app: "web" });
  expect(sent.policyTypes).toEqual(["Ingress", "Egress"]);
  expect(sent.ingress).toEqual([
    {
      from: [{ podSelector: { tier: "frontend" } }],
      ports: [{ port: 8443, protocol: "UDP" }],
    },
  ]);
  expect(sent.egress).toEqual([
    {
      to: [{ ipBlock: { cidr: "10.1.0.0/16", except: ["10.1.5.0/24"] } }],
      ports: [],
    },
  ]);
});

test("Rules: a namespace selector peer is posted under namespaceSelector", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  fireEvent.change(peerTypeSelects()[0], {
    target: { value: "namespaceSelector" },
  });
  type(keyInputs()[0], "team");
  type(valueInputs()[0], "payments");
  await next();
  const sent = await previewBody();
  expect((sent.ingress as { from: unknown[] }[])[0].from).toEqual([
    { namespaceSelector: { team: "payments" } },
  ]);
});

test("Rules: a peer left blank after Add blocks Next with a field error", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  await next();
  body().getByText("Pod Selector peer must have at least one label key", {
    exact: false,
  });
  body().getByText("Step 2 of 3", { exact: false });
  expect(previewCall()).toBeUndefined();
});

test("Rules: a namespace selector with a value but no key blocks Next", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  fireEvent.change(peerTypeSelects()[0], {
    target: { value: "namespaceSelector" },
  });
  type(valueInputs()[0], "orphan");
  await next();
  body().getByText("Namespace Selector peer must have at least one label key", {
    exact: false,
  });
  expect(previewCall()).toBeUndefined();
});

test("Rules: an IP block with a blank CIDR blocks Next", async () => {
  await toRules({ egress: true, ingress: false });
  click(body().getByRole("button", { name: "+ Add Egress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  fireEvent.change(peerTypeSelects()[0], { target: { value: "ipBlock" } });
  await next();
  body().getByText("Must be a valid CIDR (e.g. 10.0.0.0/8)");
  body().getByText("Step 2 of 3", { exact: false });
  expect(previewCall()).toBeUndefined();

  // Filling the CIDR clears the block.
  type(cidrInputs()[0], "10.0.0.0/8");
  await next();
  await waitFor(() => body().getByText("Step 3 of 3", { exact: false }));
  expect(previewCall()).toBeDefined();
});

test("Rules: a port outside 1-65535 blocks Next", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Port" }));
  type(body().getByPlaceholderText("Port (e.g. 80)"), "70000");
  await next();
  body().getByText("Must be 1-65535");
  expect(previewCall()).toBeUndefined();
});

test("Rules: removing a peer keeps the other peers' values", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  for (let i = 0; i < 3; i++) {
    click(body().getAllByRole("button", { name: "+ Add Peer" })[0]);
  }
  type(keyInputs()[0], "first");
  type(keyInputs()[1], "second");
  type(keyInputs()[2], "third");
  click(body().getAllByRole("button", { name: "Remove" })[1]);
  expect(keyInputs().map((k) => k.value)).toEqual(["first", "third"]);
});

test("Rules: removing a rule keeps the other rules' values", async () => {
  await toRules();
  for (let i = 0; i < 3; i++) {
    click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  }
  const addPorts = () => body().getAllByRole("button", { name: "+ Add Port" });
  for (const btn of addPorts()) click(btn);
  const ports = () =>
    body().getAllByPlaceholderText("Port (e.g. 80)") as HTMLInputElement[];
  type(ports()[0], "81");
  type(ports()[1], "82");
  type(ports()[2], "83");
  click(body().getAllByRole("button", { name: "Remove Rule" })[1]);
  expect(ports().map((p) => p.value)).toEqual(["81", "83"]);
});

test("Rules: removing a port keeps the other ports' values", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  for (let i = 0; i < 3; i++) {
    click(body().getByRole("button", { name: "+ Add Port" }));
  }
  const ports = () =>
    body().getAllByPlaceholderText("Port (e.g. 80)") as HTMLInputElement[];
  type(ports()[0], "81");
  type(ports()[1], "82");
  type(ports()[2], "83");
  // Each port row's remove control is a multiplication sign.
  click(body().getAllByRole("button", { name: "×" })[1]);
  expect(ports().map((p) => p.value)).toEqual(["81", "83"]);
});

test("Rules: removing a label keeps the other labels' values", async () => {
  await toRules();
  click(body().getByRole("button", { name: "+ Add Ingress Rule" }));
  click(body().getByRole("button", { name: "+ Add Peer" }));
  click(body().getByRole("button", { name: "+ Add Label" }));
  click(body().getByRole("button", { name: "+ Add Label" }));
  type(keyInputs()[0], "a");
  type(keyInputs()[1], "b");
  type(keyInputs()[2], "c");
  click(body().getAllByRole("button", { name: "×" })[1]);
  expect(keyInputs().map((k) => k.value)).toEqual(["a", "c"]);
});
