/** @jsxImportSource react */
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

// The review step's editor loads Monaco from a CDN. Serve a stand-in that
// records what the editor was handed, so a test can read the text the review
// step shows and whether it is editable.
const MONACO_CDN =
  "https://esm.sh/monaco-editor@0.52.2/esm/vs/editor/editor.api.js";
const editors: { value: string; readOnly: boolean }[] = [];
mock.module(MONACO_CDN, () => ({
  editor: {
    create: (_el: HTMLElement, opts: { value: string; readOnly: boolean }) => {
      editors.push({ value: opts.value, readOnly: opts.readOnly });
      return {
        onDidChangeModelContent: () => {},
        getValue: () => "",
        getModel: () => null,
        updateOptions: () => {},
        dispose: () => {},
      };
    },
    setModelMarkers: () => {},
  },
}));

const { default: SecretWizard } = await import("./SecretWizard.tsx");
const { default: ExternalSecretWizard } = await import(
  "./ExternalSecretWizard.tsx"
);
const { default: SecretStoreWizard } = await import("./SecretStoreWizard.tsx");

interface Call {
  url: string;
  method: string;
  /** Parsed JSON body, or null when the body is not JSON. */
  body: Record<string, unknown> | null;
  raw: string;
}

let calls: Call[] = [];

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

/** A preview endpoint that echoes the request's data back as YAML. */
function secretYaml(req: Record<string, unknown> | null): string {
  const data = (req?.data ?? {}) as Record<string, string>;
  const lines = Object.entries(data).map(([k, v]) => `  ${k}: ${v}`);
  return `kind: Secret\nstringData:\n${lines.join("\n")}\n`;
}

beforeEach(() => {
  calls = [];
  editors.length = 0;
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    const raw = typeof init?.body === "string" ? init.body : "";
    let parsed: Record<string, unknown> | null = null;
    try {
      parsed = raw ? JSON.parse(raw) : null;
    } catch {
      parsed = null;
    }
    calls.push({ url, method: init?.method ?? "GET", body: parsed, raw });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json([{ metadata: { name: "default" } }]);
    }
    if (url.endsWith("/v1/externalsecrets/stores")) {
      return json([
        { name: "vault-main", namespace: "default", provider: "vault" },
      ]);
    }
    if (url.endsWith("/v1/externalsecrets/clusterstores")) {
      return json([{ name: "shared-store", provider: "aws" }]);
    }
    if (url.endsWith("/v1/wizards/secret/preview")) {
      return json({ yaml: secretYaml(parsed) });
    }
    if (url.endsWith("/v1/yaml/apply")) {
      return json({
        results: [
          {
            kind: "Secret",
            name: "s",
            namespace: "default",
            action: "created",
          },
        ],
        summary: { created: 1, configured: 0, unchanged: 0, failed: 0 },
      });
    }
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
  // The real editor module cannot be captured (it is a network import), so
  // leave an empty one: a later load fails and falls back to a textarea, as
  // it does with no network.
  mock.module(MONACO_CDN, () => ({}));
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

function byId(id: string): HTMLElement {
  const el = document.getElementById(id);
  if (!el) throw new Error(`no element #${id}`);
  return el;
}

function previewCall(path: string): Call | undefined {
  return calls.find((c) => c.url.endsWith(`/v1/wizards/${path}/preview`));
}

async function previewBody(path: string) {
  await waitFor(() => expect(previewCall(path)).toBeDefined());
  return previewCall(path)?.body as Record<string, unknown>;
}

/** True when leaving the page would prompt: the wizard's dirty flag is set. */
function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

// ------------------------------------------------------------------ Secret

function fillSecret(name = "api-creds", key = "API_KEY", value = "abc123") {
  type(body().getByPlaceholderText("e.g. my-secret"), name);
  type(body().getByPlaceholderText("Key"), key);
  type(body().getByPlaceholderText("Value"), value);
}

test("Secret: required fields then Next advances to the review step", async () => {
  render(<SecretWizard onClose={() => {}} />);
  body().getByText("Step 1 of 2", { exact: false });
  fillSecret();
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(body().queryByPlaceholderText("e.g. my-secret")).toBeNull();
});

test("Secret: an empty name shows its error and does not advance", async () => {
  render(<SecretWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("secret")).toBeUndefined();
});

test("Secret: the review step posts the typed values", async () => {
  render(<SecretWizard onClose={() => {}} />);
  fillSecret("typed-name", "TYPED_KEY", "typed value");
  await next();
  const sent = await previewBody("secret");
  expect(sent.name).toBe("typed-name");
  expect(sent.type).toBe("Opaque");
  expect(sent.data).toEqual({ TYPED_KEY: "typed value" });
});

test("Secret: typing marks the wizard dirty", () => {
  render(<SecretWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. my-secret"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Secret: the review shows the mask, is read-only, and applies the real YAML", async () => {
  const logged: string[] = [];
  const saved = {
    log: console.log,
    info: console.info,
    warn: console.warn,
    error: console.error,
    debug: console.debug,
  };
  for (const level of Object.keys(saved) as (keyof typeof saved)[]) {
    console[level] = (...args: unknown[]) => {
      logged.push(args.map(String).join(" "));
    };
  }
  try {
    const secret = "s3cr3t-hunter2";
    render(<SecretWizard onClose={() => {}} />);
    fillSecret("masked", "API_TOKEN", secret);
    await next();
    await previewBody("secret");

    await waitFor(() => expect(editors.length).toBeGreaterThan(0));
    const shown = editors[editors.length - 1];
    expect(shown.value).not.toContain(secret);
    expect(shown.value).toContain("API_TOKEN: ••••••••");
    expect(shown.readOnly).toBe(true);
    body().getByText("Values are hidden", { exact: false });

    // The preview request carried the real value, and so does the apply.
    expect(previewCall("secret")?.body?.data).toEqual({ API_TOKEN: secret });
    await act(async () => {
      click(body().getByRole("button", { name: "Apply" }));
    });
    const applied = calls.find((c) => c.url.endsWith("/v1/yaml/apply"));
    expect(applied?.raw).toContain(`API_TOKEN: ${secret}`);

    // The value is never in a URL and never logged.
    expect(calls.some((c) => c.url.includes(secret))).toBe(false);
    expect(logged.some((line) => line.includes(secret))).toBe(false);
  } finally {
    Object.assign(console, saved);
  }
});

test("Secret: a clean apply lets the page be left without a prompt", async () => {
  render(<SecretWizard onClose={() => {}} />);
  fillSecret("applied", "API_TOKEN", "value");
  expect(leaveIsBlocked()).toBe(true);
  await next();
  await previewBody("secret");

  await act(async () => {
    click(body().getByRole("button", { name: "Apply" }));
  });
  // The apply answer is handled after the click returns.
  await waitFor(() => body().getByText("Created", { exact: false }));
  expect(calls.some((c) => c.url.endsWith("/v1/yaml/apply"))).toBe(true);
  expect(leaveIsBlocked()).toBe(false);
});

test("Secret: a value with no key blocks Next with a field error", async () => {
  render(<SecretWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-secret"), "half-row");
  type(body().getByPlaceholderText("Value"), "orphan");
  await next();
  body().getByText("At least one key-value pair is required");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("secret")).toBeUndefined();
});

test("Secret: removing a row keeps the other rows' values", () => {
  render(<SecretWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add Entry" }));
  click(body().getByRole("button", { name: "+ Add Entry" }));
  const keys = () =>
    body().getAllByPlaceholderText("Key") as HTMLInputElement[];
  const values = () =>
    body().getAllByPlaceholderText("Value") as HTMLInputElement[];
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

test("Secret: switching to TLS asks for the certificate and key", async () => {
  render(<SecretWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-secret"), "tls-cert");
  click(body().getByRole("radio", { name: /TLS Certificate/ }));
  await next();
  body().getByText("Certificate is required");
  body().getByText("Private key is required");
  expect(previewCall("secret")).toBeUndefined();
});

// ---------------------------------------------------------- ExternalSecret

async function fillExternalSecret(name = "app-config") {
  type(byId("es-name"), name);
  // Store options load asynchronously.
  await waitFor(() => {
    const select = byId("es-store-name") as HTMLSelectElement;
    expect(select.disabled).toBe(false);
  });
  fireEvent.change(byId("es-store-name"), { target: { value: "vault-main" } });
  type(byId("es-data-0-secretkey"), "DB_PASSWORD");
  type(byId("es-data-0-key"), "secret/data/db");
}

test("ExternalSecret: required fields then Next advances to the review step", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  body().getByText("Step 1 of 2", { exact: false });
  await fillExternalSecret();
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(document.getElementById("es-name")).toBeNull();
});

test("ExternalSecret: an unselected store shows its error and does not advance", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  type(byId("es-name"), "app-config");
  await next();
  // The placeholder <option> carries the same words, so read the error itself.
  expect(byId("es-store-name-error").textContent).toBe("Select a store");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("external-secret")).toBeUndefined();
});

test("ExternalSecret: the review step posts the typed values and store reference", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  await fillExternalSecret("typed-es");
  type(byId("es-data-0-property"), "password");
  await next();
  const sent = await previewBody("external-secret");
  expect(sent.name).toBe("typed-es");
  // Typing the name fills the target Secret name until it is edited itself.
  expect(sent.targetSecretName).toBe("typed-es");
  expect(sent.storeRef).toEqual({ name: "vault-main", kind: "SecretStore" });
  expect(sent.data).toEqual([
    {
      secretKey: "DB_PASSWORD",
      remoteRef: { key: "secret/data/db", property: "password" },
    },
  ]);
});

test("ExternalSecret: typing marks the wizard dirty", () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(byId("es-name"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("ExternalSecret: switching the store kind swaps the options and clears the selection", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  await waitFor(() => {
    expect((byId("es-store-name") as HTMLSelectElement).disabled).toBe(false);
  });
  const optionNames = () =>
    [...byId("es-store-name").querySelectorAll("option")].map(
      (o) => o.textContent,
    );
  expect(optionNames().join("|")).toContain("vault-main");
  expect(optionNames().join("|")).not.toContain("shared-store");
  fireEvent.change(byId("es-store-name"), { target: { value: "vault-main" } });
  expect((byId("es-store-name") as HTMLSelectElement).value).toBe("vault-main");

  fireEvent.change(byId("es-store-kind"), {
    target: { value: "ClusterSecretStore" },
  });
  expect((byId("es-store-name") as HTMLSelectElement).value).toBe("");
  expect(optionNames().join("|")).toContain("shared-store");
  expect(optionNames().join("|")).not.toContain("vault-main");
});

test("ExternalSecret: a data row without a secret key or remote key blocks Next", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  await fillExternalSecret();
  click(body().getByRole("button", { name: "+ Add data item" }));
  await next();
  expect(body().getAllByText("Required").length).toBeGreaterThanOrEqual(2);
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("external-secret")).toBeUndefined();
});

test("ExternalSecret: removing a data row keeps the other rows' values", async () => {
  render(<ExternalSecretWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add data item" }));
  click(body().getByRole("button", { name: "+ Add data item" }));
  const keys = () =>
    body().getAllByPlaceholderText("DB_PASSWORD") as HTMLInputElement[];
  type(keys()[0], "A");
  type(keys()[1], "B");
  type(keys()[2], "C");
  click(body().getAllByRole("button", { name: "Remove" })[1]);
  expect(keys().map((k) => k.value)).toEqual(["A", "C"]);
});

// ------------------------------------------------------------- SecretStore

function fillStoreIdentity(name = "vault-store") {
  type(body().getByPlaceholderText("vault-store"), name);
}

async function pickVault() {
  fillStoreIdentity();
  await next();
  click(await waitFor(() => body().getByText("HashiCorp Vault")));
}

test("SecretStore: identity then provider then the provider's Configure step", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  body().getByText("Step 1 of 3", { exact: false });
  await pickVault();
  // A provider with a guided form adds a Configure step.
  body().getByText("Step 2 of 4", { exact: false });
  await next();
  await waitFor(() =>
    body().getByPlaceholderText("https://vault.example.com:8200"),
  );
  body().getByText("Step 3 of 4", { exact: false });
});

test("SecretStore: an empty name shows its error and does not advance", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  await next();
  body().getByText("Must be a valid DNS label");
  body().getByText("Step 1 of 3", { exact: false });
});

test("SecretStore: no provider chosen blocks the provider step", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  fillStoreIdentity();
  await next();
  await waitFor(() => body().getByText("HashiCorp Vault"));
  await next();
  body().getByText("Select a provider");
  expect(previewCall("secret-store")).toBeUndefined();
});

test("SecretStore: an empty Vault server and auth block the Configure step", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  await pickVault();
  await next();
  await waitFor(() =>
    body().getByPlaceholderText("https://vault.example.com:8200"),
  );
  await next();
  body().getByText("Server URL is required");
  body().getByText("Select an authentication method");
  expect(previewCall("secret-store")).toBeUndefined();
});

test("SecretStore: the review step posts the typed values and provider spec", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  await pickVault();
  await next();
  type(
    await waitFor(() =>
      body().getByPlaceholderText("https://vault.example.com:8200"),
    ),
    "https://vault.test:8200",
  );
  click(body().getByRole("button", { name: /^Token/ }));
  type(body().getByPlaceholderText("vault-token"), "tok-secret");
  type(body().getByPlaceholderText("token"), "value");
  await next();
  const sent = await previewBody("secret-store");
  expect(sent.name).toBe("vault-store");
  expect(sent.namespace).toBe("default");
  expect(sent.provider).toBe("vault");
  expect(sent.providerSpec).toEqual({
    server: "https://vault.test:8200",
    auth: { token: { tokenSecretRef: { name: "tok-secret", key: "value" } } },
  });
});

test("SecretStore: a cluster-scoped store posts to its own endpoint without a namespace", async () => {
  render(<SecretStoreWizard scope="cluster" onClose={() => {}} />);
  type(body().getByPlaceholderText("shared-vault-store"), "shared");
  await next();
  click(await waitFor(() => body().getByText("HashiCorp Vault")));
  await next();
  type(
    await waitFor(() =>
      body().getByPlaceholderText("https://vault.example.com:8200"),
    ),
    "https://vault.test",
  );
  click(body().getByRole("button", { name: /^Token/ }));
  await next();
  const sent = await previewBody("cluster-secret-store");
  expect(sent.name).toBe("shared");
  expect(sent.namespace).toBeUndefined();
});

test("SecretStore: typing marks the wizard dirty", () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  fillStoreIdentity("x");
  expect(leaveIsBlocked()).toBe(true);
});

test("SecretStore: switching provider clears the previous provider's spec", async () => {
  render(<SecretStoreWizard scope="namespaced" onClose={() => {}} />);
  await pickVault();
  await next();
  type(
    await waitFor(() =>
      body().getByPlaceholderText("https://vault.example.com:8200"),
    ),
    "https://old.example",
  );
  click(body().getByRole("button", { name: "Back" }));
  click(await waitFor(() => body().getByText("Doppler")));
  await next();
  await waitFor(() => body().getByText("Step 3 of 4", { exact: false }));
  expect(
    body().queryByPlaceholderText("https://vault.example.com:8200"),
  ).toBeNull();
});
