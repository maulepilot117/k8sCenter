import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The Cilium policy editor builds a YAML preview and a create request from
 * the same form.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: CiliumPolicyEditor } = await import(
  "./CiliumPolicyEditor.tsx"
);

let posts: { url: string; body: unknown }[] = [];

beforeEach(() => {
  posts = [];
  setAccessToken("test-token");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    if (init?.method === "POST") {
      posts.push({ url, body: JSON.parse(String(init.body)) });
      return new Response(JSON.stringify({ data: { resource: {} } }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response(JSON.stringify({ data: [] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

/** Lets the namespace list request finish before the form is used. */

function type(el: HTMLElement, value: string) {
  act(() => {
    fireEvent.change(el, { target: { value } });
  });
}

/** Fills a policy with endpoint labels, an ingress rule and an egress CIDR rule. */
function fillPolicy(root: HTMLElement) {
  const q = (sel: string) =>
    Array.from(root.querySelectorAll(sel)) as HTMLElement[];
  type(q('input[placeholder="my-policy"]')[0], "web-policy");
  // Applied-to label
  type(q('input[placeholder="key"]')[0], "app");
  type(q('input[placeholder="value"]')[0], "web");
  // Rule 1: ingress from endpoints env=prod on ports 80,443
  const rule1 = q("tbody tr")[0];
  type(rule1.querySelector('input[placeholder="key"]') as HTMLElement, "env");
  type(
    rule1.querySelector('input[placeholder="value"]') as HTMLElement,
    "prod",
  );
  type(
    rule1.querySelector('input[placeholder="80,443"]') as HTMLElement,
    "80, 443",
  );
  act(() => {
    fireEvent.click(body().getByText("+ Add Rule"));
  });
  // Rule 2: egress deny to a CIDR
  const rule2 = Array.from(root.querySelectorAll("tbody tr"))[1] as HTMLElement;
  const selects = Array.from(rule2.querySelectorAll("select")) as HTMLElement[];
  type(selects[0], "Egress");
  type(selects[1], "cidr");
  type(selects[2], "UDP");
  type(selects[3], "deny");
  type(
    rule2.querySelector(
      'input[placeholder="10.0.0.0/8, 192.168.0.0/16"]',
    ) as HTMLElement,
    "10.0.0.0/8, 192.168.0.0/16",
  );
  type(rule2.querySelector('input[placeholder="80,443"]') as HTMLElement, "53");
}

// The preview pins the editor's current one-space indentation.
test("the preview shows the YAML for the form", async () => {
  const view = render(<CiliumPolicyEditor />);
  await settle();
  fillPolicy(view.container as HTMLElement);
  act(() => {
    fireEvent.click(body().getByText("Show"));
  });
  await waitFor(() => {
    expect(view.container.querySelector("pre")?.textContent).toContain(
      "web-policy",
    );
  });
  expect(view.container.querySelector("pre")?.textContent).toBe(
    [
      "apiVersion: cilium.io/v2",
      "kind: CiliumNetworkPolicy",
      "metadata:",
      " name: web-policy",
      " namespace: default",
      "spec:",
      " endpointSelector:",
      " matchLabels:",
      " app: web",
      " ingress:",
      " -fromEndpoints:",
      " - matchLabels:",
      " env: prod",
      " toPorts:",
      " - ports:",
      ' - port:"80"',
      " protocol: TCP",
      ' - port:"443"',
      " protocol: TCP",
      " egressDeny:",
      " -toCIDR:",
      " - 10.0.0.0/8",
      " - 192.168.0.0/16",
      " toPorts:",
      " - ports:",
      ' - port:"53"',
      " protocol: UDP",
    ].join("\n"),
  );
});

test("creating the policy posts the payload built from the form", async () => {
  const view = render(<CiliumPolicyEditor />);
  await settle();
  fillPolicy(view.container as HTMLElement);
  act(() => {
    fireEvent.click(body().getByText("Create Policy"));
  });
  await waitFor(() => {
    expect(body().getByText(/Policy created successfully/)).toBeTruthy();
  });
  expect(posts).toHaveLength(1);
  expect(posts[0].url).toContain("/v1/resources/ciliumnetworkpolicies/");
  expect(posts[0].body).toEqual({
    name: "web-policy",
    namespace: "default",
    endpointSelector: { app: "web" },
    ingressRules: [
      {
        peerType: "endpoints",
        action: "allow",
        labels: { env: "prod" },
        ports: [
          { port: 80, protocol: "TCP" },
          { port: 443, protocol: "TCP" },
        ],
      },
    ],
    egressRules: [
      {
        peerType: "cidr",
        action: "deny",
        cidrs: ["10.0.0.0/8", "192.168.0.0/16"],
        ports: [{ port: 53, protocol: "UDP" }],
      },
    ],
  });
});

test("an invalid port is reported and nothing is posted", async () => {
  const view = render(<CiliumPolicyEditor />);
  await settle();
  const root = view.container as HTMLElement;
  type(
    root.querySelector('input[placeholder="my-policy"]') as HTMLElement,
    "p",
  );
  type(
    root.querySelector('input[placeholder="80,443"]') as HTMLElement,
    "99999",
  );
  act(() => {
    fireEvent.click(body().getByText("Create Policy"));
  });
  await waitFor(() => {
    expect(root.textContent).toContain("Rule 1: invalid port");
  });
  expect(posts).toHaveLength(0);
});
