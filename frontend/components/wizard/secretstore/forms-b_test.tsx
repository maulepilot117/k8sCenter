/** @jsxImportSource react */
import { expect, test } from "bun:test";
// Loads preact/compat so `onChange` on text inputs fires per keystroke.
import "react";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { GCPSMForm } = await import("./GCPSMForm.tsx");
const { KubernetesForm } = await import("./KubernetesForm.tsx");
const { OnePasswordForm } = await import("./OnePasswordForm.tsx");
const { VaultForm } = await import("./VaultForm.tsx");

type Spec = Record<string, unknown>;

/** Renders a form whose spec follows what it reports, like the wizard does. */
function harness(
  Form: (p: {
    spec: Spec;
    errors: Record<string, string>;
    onUpdateSpec: (s: Spec) => void;
  }) => React.ReactElement,
  initial: Spec,
) {
  const calls: Spec[] = [];
  let spec = initial;
  const props = () => ({
    spec,
    errors: {},
    onUpdateSpec: (s: Spec) => {
      calls.push(s);
      spec = s;
      view.rerender(<Form {...props()} />);
    },
  });
  const view = render(<Form {...props()} />);
  return { calls, view };
}

function click(name: string | RegExp) {
  fireEvent.click(body().getByRole("button", { name }));
}

function type(label: string | RegExp, value: string) {
  fireEvent.input(body().getByLabelText(label), { target: { value } });
}

test("Vault: default fields, two auth switches, typing", () => {
  const { calls } = harness(VaultForm, {
    server: "https://v:8200",
    auth: { token: { tokenSecretRef: { name: "t", key: "k" } } },
  });
  expect((body().getByLabelText("Server URL") as HTMLInputElement).value).toBe(
    "https://v:8200",
  );
  expect((body().getByLabelText("Secret name") as HTMLInputElement).value).toBe(
    "t",
  );

  click(/^AppRole/);
  expect(calls.at(-1)).toEqual({
    server: "https://v:8200",
    auth: { appRole: { secretRef: {} } },
  });
  body().getByLabelText("Role ID");
  body().getByLabelText("Auth path");
  expect(body().queryByText("Token Secret reference")).toBeNull();

  click(/^TLS Cert/);
  expect(calls.at(-1)).toEqual({
    server: "https://v:8200",
    auth: { cert: { clientCert: {}, secretRef: {} } },
  });
  body().getByText("Client certificate");
  body().getByText("Client key");
  expect(body().queryByLabelText("Role ID")).toBeNull();

  click(/^Kubernetes/);
  expect(calls.at(-1)).toEqual({
    server: "https://v:8200",
    auth: { kubernetes: {} },
  });
  body().getByLabelText("Mount path");
  body().getByLabelText("Role");

  type("Role", "my-app");
  expect(calls.at(-1)).toEqual({
    server: "https://v:8200",
    auth: { kubernetes: { role: "my-app" } },
  });
  type("Server URL", "");
  expect(calls.at(-1)).toEqual({
    auth: { kubernetes: { role: "my-app" } },
  });
});

test("Vault: clicking the active method reports nothing", () => {
  const { calls } = harness(VaultForm, {
    auth: { jwt: { secretRef: {} } },
  });
  body().getByText("JWT / OIDC auth");
  click(/^JWT/);
  expect(calls).toHaveLength(0);
});

test("Kubernetes: switch methods and type", () => {
  const { calls } = harness(KubernetesForm, {
    remoteNamespace: "ns",
    auth: { serviceAccount: { name: "sa" } },
  });
  expect(
    (body().getByLabelText("ServiceAccount name") as HTMLInputElement).value,
  ).toBe("sa");

  click(/^Bearer Token/);
  expect(calls.at(-1)).toEqual({
    remoteNamespace: "ns",
    auth: { token: { bearerToken: {} } },
  });
  body().getByText("Bearer token Secret reference");
  expect(body().queryByLabelText("ServiceAccount name")).toBeNull();

  click(/^TLS Cert/);
  expect(calls.at(-1)).toEqual({
    remoteNamespace: "ns",
    auth: { cert: { clientCert: {}, clientKey: {} } },
  });
  body().getByText("Client key");

  type("Remote namespace", "other");
  expect(calls.at(-1)).toEqual({
    remoteNamespace: "other",
    auth: { cert: { clientCert: {}, clientKey: {} } },
  });
});

test("Kubernetes: audiences split on commas", () => {
  const { calls } = harness(KubernetesForm, {
    auth: { serviceAccount: { name: "sa" } },
  });
  type("Token audiences (optional)", "a, b");
  expect(calls.at(-1)).toEqual({
    auth: { serviceAccount: { name: "sa", audiences: ["a", "b"] } },
  });
});

test("GCP SM: default, workload identity, key, back to default", () => {
  const { calls } = harness(GCPSMForm, { projectID: "p" });
  body().getByText(/No additional configuration required/);

  click(/^Workload Identity/);
  expect(calls.at(-1)).toEqual({
    projectID: "p",
    auth: { workloadIdentity: { serviceAccountRef: {} } },
  });
  body().getByLabelText("ServiceAccount name");
  expect(body().queryByText(/No additional configuration/)).toBeNull();

  click(/^SA Key/);
  expect(calls.at(-1)).toEqual({
    projectID: "p",
    auth: { secretRef: { secretAccessKeySecretRef: {} } },
  });
  body().getByText("Service Account key Secret reference");

  type("Project ID", "q");
  expect(calls.at(-1)).toEqual({
    projectID: "q",
    auth: { secretRef: { secretAccessKeySecretRef: {} } },
  });

  click(/^Default Credentials/);
  expect(calls.at(-1)).toEqual({ projectID: "q" });
  body().getByText(/No additional configuration required/);
});

test("1Password: token ref and vault rows", () => {
  const { calls } = harness(OnePasswordForm, {
    connectHost: "https://c",
    vaults: { prod: 2 },
  });
  expect(
    (body().getByLabelText("Connect server URL") as HTMLInputElement).value,
  ).toBe("https://c");

  type("Secret name", "tok");
  expect(calls.at(-1)).toEqual({
    connectHost: "https://c",
    vaults: { prod: 2 },
    auth: { secretRef: { connectTokenSecretRef: { name: "tok" } } },
  });

  click("+ Add vault");
  expect(calls.at(-1)?.vaults).toEqual({ prod: 2 });
  const removes = body().getAllByRole("button", { name: /Remove vault row/ });
  expect(removes).toHaveLength(2);

  fireEvent.input(
    document.getElementById("op-vault-name-1") as HTMLInputElement,
    { target: { value: "dev" } },
  );
  expect(calls.at(-1)?.vaults).toEqual({ prod: 2, dev: 1 });

  fireEvent.click(removes[0]);
  expect(calls.at(-1)?.vaults).toEqual({ dev: 1 });
  expect(
    body().getAllByRole("button", { name: /Remove vault row/ }),
  ).toHaveLength(1);
});
