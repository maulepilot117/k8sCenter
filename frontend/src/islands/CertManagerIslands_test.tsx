/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * cert-manager list and detail islands: filters, the expiring URL filter,
 * and the renew / re-issue actions.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: CertificatesList } = await import("./CertificatesList.tsx");
const { default: CertificateDetail } = await import("./CertificateDetail.tsx");
const { default: IssuersList } = await import("./IssuersList.tsx");
const { default: ESOChainPanel } = await import("./ESOChainPanel.tsx");
const { default: ESOChainPage } = await import("./ESOChainPage.tsx");

const realSearch = globalThis.location.search;

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  globalThis.history.replaceState(null, "", `/${realSearch}`);
});

const cert = (name: string, status: string, ns = "default", issuer = "le") => ({
  uid: name,
  name,
  namespace: ns,
  status,
  issuerRef: { name: issuer, kind: "Issuer" },
  secretName: `${name}-tls`,
  dnsNames: [`${name}.example.com`],
  daysRemaining: 10,
});

const CERTS = [
  cert("alpha", "Ready"),
  cert("beta", "Expiring", "prod", "corp-ca"),
  cert("gamma", "Expired"),
];

function stubList() {
  globalThis.fetch = (async () => json(CERTS)) as unknown as typeof fetch;
}

function stubTopology() {
  globalThis.fetch = (async () =>
    json({ nodes: [], edges: [] })) as unknown as typeof fetch;
}

function buttonByLabel(root: Element, label: string): HTMLButtonElement {
  return [...root.querySelectorAll("button")].find(
    (b) => b.textContent === label,
  ) as HTMLButtonElement;
}

test("certificates list filters by search text across name, namespace and issuer", async () => {
  setAccessToken("t");
  stubList();
  const view = render(<CertificatesList />);
  await settle();
  const root = view.container;
  expect(root.textContent).toContain("3 of 3 certificates");

  const input = root.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.input(input, { target: { value: "corp-ca" } });
    fireEvent.change(input, { target: { value: "corp-ca" } });
  });
  expect(root.textContent).toContain("1 of 3 certificates");
  expect(root.textContent).toContain("beta");
  expect(root.textContent).not.toContain("alpha");

  await act(async () => {
    fireEvent.input(input, { target: { value: "nomatch" } });
    fireEvent.change(input, { target: { value: "nomatch" } });
  });
  expect(root.textContent).toContain("No certificates match your filters.");
});

test("certificates list honours ?status=expiring", async () => {
  setAccessToken("t");
  stubList();
  globalThis.history.replaceState(null, "", "/?status=expiring");
  const view = render(<CertificatesList />);
  await settle();
  const root = view.container;
  expect(root.textContent).toContain("2 of 3 certificates");
  expect(root.textContent).toContain(
    "Showing expiring and expired certificates only.",
  );
  expect(root.textContent).not.toContain("alpha");
  expect(root.textContent).toContain("beta");
  expect(root.textContent).toContain("gamma");
});

test("certificates list pages at 100 rows and returns to page 1 on search", async () => {
  setAccessToken("t");
  const many = Array.from({ length: 150 }, (_, i) =>
    cert(`c${String(i).padStart(3, "0")}`, "Ready"),
  );
  globalThis.fetch = (async () => json(many)) as unknown as typeof fetch;
  const view = render(<CertificatesList />);
  await settle();
  const root = view.container;
  expect(root.textContent).toContain("Page 1 of 2");
  await act(async () => {
    fireEvent.click(buttonByLabel(root, "Next"));
  });
  expect(root.textContent).toContain("Page 2 of 2");
  expect(root.textContent).toContain("c149");
  const input = root.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.input(input, { target: { value: "c001" } });
    fireEvent.change(input, { target: { value: "c001" } });
  });
  expect(root.textContent).toContain("1 of 150 certificates");
  expect(root.textContent).toContain("c001");
});

test("issuers list merges issuers and cluster issuers", async () => {
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/clusterissuers")) {
      return json([
        {
          uid: "2",
          name: "root-ca",
          scope: "Cluster",
          type: "CA",
          ready: false,
          reason: "BadCA",
        },
      ]);
    }
    return json([
      {
        uid: "1",
        name: "le",
        scope: "Namespaced",
        namespace: "web",
        type: "ACME",
        ready: true,
        acmeServer: "https://acme.example",
      },
    ]);
  }) as unknown as typeof fetch;
  const view = render(<IssuersList />);
  await settle();
  const text = view.container.textContent ?? "";
  expect(text).toContain("root-ca");
  expect(text).toContain("https://acme.example");
  expect(text).toContain("BadCA");
  expect(text).toContain("Not Ready");
});

const DETAIL = {
  certificate: {
    ...cert("alpha", "Ready"),
    warningThresholdDays: 60,
    criticalThresholdDays: 10,
    warningThresholdSource: "issuer",
    criticalThresholdSource: "certificate",
  },
};

function stubDetail(posts: string[]) {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    if (init?.method === "POST") {
      posts.push(new URL(url, "http://x").pathname);
      return json({});
    }
    return json(DETAIL);
  }) as unknown as typeof fetch;
}

test("certificate detail renders threshold attribution", async () => {
  setAccessToken("t");
  stubDetail([]);
  const view = render(<CertificateDetail namespace="default" name="alpha" />);
  await settle();
  const text = view.container.textContent ?? "";
  expect(text).toContain("Warns at 60d");
  expect(text).toContain("From Issuer le");
  expect(text).toContain("critical at 10d");
  expect(text).toContain("From this certificate");
});

test("renew sends one POST and shows the result", async () => {
  setAccessToken("t");
  const posts: string[] = [];
  stubDetail(posts);
  const view = render(<CertificateDetail namespace="default" name="alpha" />);
  await settle();
  await act(async () => {
    fireEvent.click(buttonByLabel(view.container, "Renew"));
  });
  await settle();
  expect(posts).toEqual([
    "/api/v1/certificates/certificates/default/alpha/renew",
  ]);
  expect(view.container.textContent).toContain("Renewal triggered.");
});

test("re-issue asks for confirmation before posting", async () => {
  setAccessToken("t");
  const posts: string[] = [];
  stubDetail(posts);
  const view = render(<CertificateDetail namespace="default" name="alpha" />);
  await settle();
  const root = view.container;
  await act(async () => {
    fireEvent.click(buttonByLabel(root, "Re-issue"));
  });
  expect(root.textContent).toContain("Confirm Re-issue");
  expect(posts).toEqual([]);
  await act(async () => {
    fireEvent.click(buttonByLabel(root, "Cancel"));
  });
  expect(root.textContent).not.toContain("Confirm Re-issue");
  await act(async () => {
    fireEvent.click(buttonByLabel(root, "Re-issue"));
  });
  await act(async () => {
    fireEvent.click(buttonByLabel(root, "Yes, re-issue"));
  });
  await settle();
  expect(posts).toEqual([
    "/api/v1/certificates/certificates/default/alpha/reissue",
  ]);
  expect(root.textContent).toContain("Re-issue triggered.");
  expect(root.textContent).not.toContain("Confirm Re-issue");
});

test("chain panel shows the prompt until a namespace is typed, then links to topology", async () => {
  setAccessToken("t");
  stubTopology();
  const view = render(<ESOChainPanel focusedNodeId="n/1" />);
  await settle();
  const root = view.container;
  expect(root.textContent).toContain("Select a namespace to inspect");
  const input = root.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.input(input, { target: { value: " payments " } });
    fireEvent.change(input, { target: { value: " payments " } });
  });
  expect(root.textContent).not.toContain("Select a namespace to inspect");
  const link = root.querySelector("a") as HTMLAnchorElement;
  expect(link.getAttribute("href")).toBe(
    "/observability/topology?namespace=payments&overlay=eso-chain&focus=n%2F1",
  );
});

test("chain panel with a namespace prop hides the input", async () => {
  setAccessToken("t");
  stubTopology();
  const view = render(<ESOChainPanel namespace="ops" focusedNodeId="x" />);
  await settle();
  expect(view.container.querySelector("input")).toBeNull();
});

test("chain page seeds the namespace from the page's parameter and follows typing", async () => {
  setAccessToken("t");
  stubTopology();
  const view = render(<ESOChainPage initialNamespace="seeded" />);
  await settle();
  const root = view.container;
  const input = root.querySelector("input") as HTMLInputElement;
  expect(input.value).toBe("seeded");
  await act(async () => {
    fireEvent.input(input, { target: { value: "" } });
    fireEvent.change(input, { target: { value: "" } });
  });
  expect(root.textContent).toContain("Select a namespace");
});
