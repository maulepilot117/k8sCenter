import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
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

const { default: CertificateWizard } = await import("./CertificateWizard.tsx");
const { default: IssuerWizard } = await import("./IssuerWizard.tsx");
const { default: PolicyWizard } = await import("./PolicyWizard.tsx");

afterAll(() => {
  globalThis.fetch = bunFetch;
});

interface Call {
  path: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
let policyStatus: Record<string, unknown> = {
  detected: "kyverno",
  lastChecked: "",
};

const json = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  calls = [];
  policyStatus = { detected: "kyverno", lastChecked: "" };
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).replace(/^.*\/api/, "");
    const raw = init?.body;
    calls.push({
      path,
      body: typeof raw === "string" ? JSON.parse(raw) : null,
    });
    if (path === "/v1/certificates/issuers") {
      return json([
        {
          uid: "u1",
          name: "my-issuer",
          namespace: "default",
          scope: "Namespaced",
          type: "SelfSigned",
        },
      ]);
    }
    if (path === "/v1/certificates/clusterissuers") return json([]);
    if (path === "/v1/policies/status") return json(policyStatus);
    if (path === "/v1/resources/namespaces") return json([]);
    if (path.endsWith("/preview")) return json({ yaml: "kind: Preview\n" });
    return json({});
  }) as unknown as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = bunFetch;
});

const previewCall = () => calls.find((c) => c.path.endsWith("/preview"));

function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

function pick(el: HTMLElement, value: string) {
  fireEvent.change(el, { target: { value } });
}

async function press(name: string | RegExp) {
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name }));
  });
}

// ─── CertificateWizard ───────────────────────────────────────────────────

async function fillCertificate(name: string) {
  type(body().getByLabelText("Name"), name);
  type(body().getByLabelText("DNS Names"), "app.example.com, www.example.com");
  await waitFor(() =>
    expect(body().getByLabelText("Issuer") as HTMLSelectElement).toHaveProperty(
      "disabled",
      false,
    ),
  );
  pick(body().getByLabelText("Issuer"), "Issuer:my-issuer");
}

test("certificate: filled configure step advances to review and posts the typed values", async () => {
  render(<CertificateWizard onClose={() => {}} />);
  await fillCertificate("web-cert");
  expect(body().getByText(/Step 1 of 2/)).toBeTruthy();

  await press("Continue");

  await waitFor(() => expect(body().getByText(/Step 2 of 2/)).toBeTruthy());
  await waitFor(() => expect(previewCall()).toBeTruthy());
  const sent = previewCall();
  expect(sent?.path).toBe("/v1/wizards/certificate/preview");
  expect(sent?.body?.name).toBe("web-cert");
  expect(sent?.body?.dnsNames).toEqual(["app.example.com", "www.example.com"]);
  expect(sent?.body?.issuerRef).toMatchObject({ name: "my-issuer" });
});

test("certificate: secret name follows the name until the user diverges it", async () => {
  render(<CertificateWizard onClose={() => {}} />);
  await fillCertificate("web-cert");
  expect((body().getByLabelText("Secret Name") as HTMLInputElement).value).toBe(
    "web-cert-tls",
  );
  type(body().getByLabelText("Secret Name"), "custom-secret");
  type(body().getByLabelText("Name"), "other-cert");
  expect((body().getByLabelText("Secret Name") as HTMLInputElement).value).toBe(
    "custom-secret",
  );

  await press("Continue");
  await waitFor(() => expect(previewCall()).toBeTruthy());
  expect(previewCall()?.body?.secretName).toBe("custom-secret");
});

test("certificate: Continue with an empty name shows the error and stays put", async () => {
  render(<CertificateWizard onClose={() => {}} />);
  await press("Continue");

  expect(
    body().getByText(/Must be a valid DNS label \(lowercase/),
  ).toBeTruthy();
  expect(body().getByText(/Step 1 of 2/)).toBeTruthy();
  expect(previewCall()).toBeUndefined();
});

// ─── IssuerWizard ────────────────────────────────────────────────────────

test("issuer: choosing a type and filling the name reaches review and posts the typed name", async () => {
  render(<IssuerWizard scope="namespaced" onClose={() => {}} />);
  await press(/^Self-Signed/);
  await press("Continue");
  type(body().getByLabelText("Name"), "my-selfsigned");
  await press("Continue");

  await waitFor(() => expect(body().getByText(/Step 3 of 3/)).toBeTruthy());
  await waitFor(() => expect(previewCall()).toBeTruthy());
  const sent = previewCall();
  expect(sent?.path).toBe("/v1/wizards/issuer/preview");
  expect(sent?.body?.name).toBe("my-selfsigned");
  expect(sent?.body?.type).toBe("selfSigned");
});

test("issuer: Continue on the type step without a type shows the error", async () => {
  render(<IssuerWizard scope="namespaced" onClose={() => {}} />);
  await press("Continue");

  expect(body().getByText("Select an issuer type")).toBeTruthy();
  expect(body().getByText(/Step 1 of 3/)).toBeTruthy();
});

test("issuer: Continue with an empty name shows the error and does not advance", async () => {
  render(<IssuerWizard scope="cluster" onClose={() => {}} />);
  await press(/^Self-Signed/);
  await press("Continue");
  await press("Continue");

  expect(body().getByText("Must be a valid DNS label")).toBeTruthy();
  expect(body().getByText(/Step 2 of 3/)).toBeTruthy();
  expect(previewCall()).toBeUndefined();
});

test("issuer: the ACME type shows its own fields, validates the email, and posts to the cluster endpoint", async () => {
  render(<IssuerWizard scope="cluster" onClose={() => {}} />);
  await press(/^ACME/);
  await press("Continue");
  expect(body().getByLabelText("ACME Server")).toBeTruthy();

  type(body().getByLabelText("Name"), "le-staging");
  expect(
    (body().getByLabelText("Account Private Key Secret") as HTMLInputElement)
      .value,
  ).toBe("le-staging-account");

  await press("Continue");
  expect(body().getByText("A valid email is required")).toBeTruthy();
  expect(body().getByText(/Step 2 of 3/)).toBeTruthy();

  type(body().getByLabelText("Contact Email"), "ops@example.com");
  await press("Continue");
  await waitFor(() => expect(previewCall()).toBeTruthy());
  const sent = previewCall();
  expect(sent?.path).toBe("/v1/wizards/cluster-issuer/preview");
  expect(sent?.body?.acme).toMatchObject({ email: "ops@example.com" });
});

// ─── PolicyWizard ────────────────────────────────────────────────────────

async function mountPolicy() {
  render(<PolicyWizard onClose={() => {}} />);
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/v1/policies/status")).toBe(true),
  );
  await act(async () => {});
}

const beforeUnload = () => {
  const ev = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(ev);
  return ev.defaultPrevented;
};

test("policy: choosing a template and continuing reaches configure, then review posts the typed values", async () => {
  await mountPolicy();
  fireEvent.click(body().getByText("Disallow Privileged Containers"));
  await press("Continue");

  expect(body().getByText("Policy Name")).toBeTruthy();
  const nameInput = body().getByPlaceholderText("disallow-privileged");
  type(nameInput, "my-policy");
  await press("Continue");

  await waitFor(() => expect(previewCall()).toBeTruthy());
  const sent = previewCall();
  expect(sent?.path).toBe("/v1/wizards/policy/preview");
  expect(sent?.body?.name).toBe("my-policy");
  expect(sent?.body?.templateId).toBe("disallow-privileged");
  expect(sent?.body?.engine).toBe("kyverno");
  expect(sent?.body?.action).toBe("Enforce");
});

test("policy: Continue without a template does not advance", async () => {
  await mountPolicy();
  await press("Continue");

  expect(
    body().getByText("Choose a policy template to get started."),
  ).toBeTruthy();
  expect(body().queryByText("Policy Name")).toBeNull();
});

test("policy: a blank policy name shows the error and does not advance", async () => {
  await mountPolicy();
  fireEvent.click(body().getByText("Disallow Privileged Containers"));
  await press("Continue");
  type(body().getByPlaceholderText("disallow-privileged"), "");
  await press("Continue");

  expect(body().getByText(/Must be lowercase alphanumeric/)).toBeTruthy();
  expect(previewCall()).toBeUndefined();
});

test("policy: a required list parameter with only a blank row is rejected", async () => {
  await mountPolicy();
  fireEvent.click(body().getByText("Restrict Image Registries"));
  await press("Continue");
  await press("+ Add value");
  await press("Continue");

  expect(body().getByText("Allowed registries is required")).toBeTruthy();
  expect(previewCall()).toBeUndefined();
});

test("policy: typing marks the wizard dirty for the beforeunload guard", async () => {
  await mountPolicy();
  expect(beforeUnload()).toBe(false);

  fireEvent.click(body().getByText("Disallow Privileged Containers"));
  await act(async () => {});
  expect(beforeUnload()).toBe(true);
});

test("policy: with no engine detected the wizard shows the install notice instead of the steps", async () => {
  policyStatus = { detected: "", lastChecked: "" };
  render(<PolicyWizard onClose={() => {}} />);

  await waitFor(() =>
    expect(body().getByText("No Policy Engine Detected")).toBeTruthy(),
  );
  expect(
    body().queryByText("Choose a policy template to get started."),
  ).toBeNull();
});
