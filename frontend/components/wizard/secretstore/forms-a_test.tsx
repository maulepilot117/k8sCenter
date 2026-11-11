import { expect, mock, test } from "bun:test";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { AWSForm } = await import("./AWSForm.tsx");
const { AWSPSForm } = await import("./AWSPSForm.tsx");
const { AzureKVForm } = await import("./AzureKVForm.tsx");
const { DopplerForm } = await import("./DopplerForm.tsx");
const { SecretStoreProviderPickerStep } = await import(
  "./SecretStoreProviderPickerStep.tsx"
);

function field(id: string): HTMLInputElement {
  const node = document.getElementById(id);
  if (!(node instanceof HTMLInputElement)) throw new Error(`no input #${id}`);
  return node;
}

function has(id: string): boolean {
  return document.getElementById(id) !== null;
}

function type(id: string, value: string) {
  fireEvent.input(field(id), { target: { value } });
}

function click(name: string) {
  fireEvent.click(body().getByRole("button", { name: new RegExp(name) }));
}

const NO_ERRORS: Record<string, string> = {};

test("AWSForm shows the fields of the method in the spec and reports edits", () => {
  const onUpdateSpec = mock((_: Record<string, unknown>) => {});
  const spec = {
    region: "us-east-1",
    auth: { jwt: { serviceAccountRef: { name: "sa" } } },
  };
  render(
    <AWSForm spec={spec} errors={NO_ERRORS} onUpdateSpec={onUpdateSpec} />,
  );
  expect(field("aws-region").value).toBe("us-east-1");
  expect(field("aws-jwt-sa-name").value).toBe("sa");
  expect(has("aws-akid-name")).toBe(false);

  type("aws-jwt-sa-name", "other");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "us-east-1",
    auth: { jwt: { serviceAccountRef: { name: "other" } } },
  });

  type("aws-region", "");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    auth: { jwt: { serviceAccountRef: { name: "sa" } } },
  });

  click("Static credentials");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "us-east-1",
    auth: {
      secretRef: { accessKeyIDSecretRef: {}, secretAccessKeySecretRef: {} },
    },
  });
  expect(has("aws-jwt-sa-name")).toBe(false);
  expect(has("aws-akid-name")).toBe(true);
  expect(has("aws-sak-key")).toBe(true);

  type("aws-akid-key", "akid");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "us-east-1",
    auth: {
      jwt: { serviceAccountRef: { name: "sa" } },
      secretRef: {
        accessKeyIDSecretRef: { key: "akid" },
      },
    },
  });
});

test("AWSPSForm switches auth method and relabels the role field", () => {
  const onUpdateSpec = mock((_: Record<string, unknown>) => {});
  const spec = {
    region: "eu-west-1",
    role: "arn:role",
    auth: { jwt: { serviceAccountRef: { name: "sa" } } },
  };
  render(
    <AWSPSForm spec={spec} errors={NO_ERRORS} onUpdateSpec={onUpdateSpec} />,
  );
  expect(body().getByLabelText("IAM role ARN")).toBe(field("awsps-role"));
  expect(field("awsps-jwt-sa-name").value).toBe("sa");

  type("awsps-jwt-sa-name", "x");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "eu-west-1",
    role: "arn:role",
    auth: { jwt: { serviceAccountRef: { name: "x" } } },
  });

  click("Static credentials");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "eu-west-1",
    role: "arn:role",
    auth: {
      secretRef: { accessKeyIDSecretRef: {}, secretAccessKeySecretRef: {} },
    },
  });
  expect(body().getByLabelText("Assume-role ARN (optional)")).toBe(
    field("awsps-role"),
  );
  expect(has("awsps-jwt-sa-name")).toBe(false);
  expect(has("awsps-ak-name")).toBe(true);

  type("awsps-sak-name", "creds");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    region: "eu-west-1",
    role: "arn:role",
    auth: {
      jwt: { serviceAccountRef: { name: "sa" } },
      secretRef: { secretAccessKeySecretRef: { name: "creds" } },
    },
  });
});

test("AzureKVForm switching auth type clears the owned fields", () => {
  const onUpdateSpec = mock((_: Record<string, unknown>) => {});
  const spec = {
    vaultUrl: "https://v.vault.azure.net",
    authType: "ManagedIdentity",
    tenantId: "t",
    identityId: "mi",
    serviceAccountRef: { name: "sa" },
    authSecretRef: { clientId: { name: "a" } },
  };
  render(
    <AzureKVForm spec={spec} errors={NO_ERRORS} onUpdateSpec={onUpdateSpec} />,
  );
  expect(field("azurekv-vault-url").value).toBe("https://v.vault.azure.net");
  expect(field("azurekv-mi-identity-id").value).toBe("mi");
  expect(has("azurekv-sp-tenant-id")).toBe(false);

  type("azurekv-mi-identity-id", "mi2");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({ ...spec, identityId: "mi2" });

  click("Service Principal");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    vaultUrl: "https://v.vault.azure.net",
    authType: "ServicePrincipal",
  });
  expect(has("azurekv-mi-identity-id")).toBe(false);
  expect(has("azurekv-sp-tenant-id")).toBe(true);
  expect(has("azurekv-sp-secret-key")).toBe(true);

  type("azurekv-sp-clientid-name", "cid");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    ...spec,
    authSecretRef: { clientId: { name: "cid" } },
  });

  click("Workload Identity");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    vaultUrl: "https://v.vault.azure.net",
    authType: "WorkloadIdentity",
  });
  expect(has("azurekv-wi-sa-name")).toBe(true);
  expect(has("azurekv-sp-tenant-id")).toBe(false);
});

test("DopplerForm switches between service token and OIDC", () => {
  const onUpdateSpec = mock((_: Record<string, unknown>) => {});
  const spec = {
    project: "p",
    config: "prd",
    auth: { secretRef: { dopplerToken: { name: "tok", key: "k" } } },
  };
  render(
    <DopplerForm spec={spec} errors={NO_ERRORS} onUpdateSpec={onUpdateSpec} />,
  );
  expect(field("doppler-project").value).toBe("p");
  expect(field("doppler-token-ref-name").value).toBe("tok");
  expect(has("doppler-oidc-identity")).toBe(false);

  type("doppler-token-ref-key", "k2");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    project: "p",
    config: "prd",
    auth: { secretRef: { dopplerToken: { name: "tok", key: "k2" } } },
  });

  click("OIDC");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    project: "p",
    config: "prd",
    auth: { oidcConfig: { serviceAccountRef: {} } },
  });
  expect(has("doppler-token-ref-name")).toBe(false);
  expect(has("doppler-oidc-identity")).toBe(true);

  type("doppler-oidc-sa-name", "app");
  expect(onUpdateSpec).toHaveBeenLastCalledWith({
    project: "p",
    config: "prd",
    auth: {
      secretRef: { dopplerToken: { name: "tok", key: "k" } },
      oidcConfig: { serviceAccountRef: { name: "app" } },
    },
  });
});

test("provider picker lists providers and reports a ready choice", () => {
  const onSelect = mock((_: string) => {});
  render(<SecretStoreProviderPickerStep selected="aws" onSelect={onSelect} />);
  for (const title of [
    "HashiCorp Vault",
    "AWS Secrets Manager",
    "AWS Parameter Store",
    "Azure Key Vault",
    "Doppler",
    "Generic webhook",
  ]) {
    body().getByText(title);
  }
  expect(body().getAllByRole("button")).toHaveLength(19);
  expect(body().getByText("Selected")).toBeTruthy();
  expect(
    body()
      .getByRole("button", { name: /AWS Secrets Manager/ })
      .getAttribute("aria-pressed"),
  ).toBe("true");

  fireEvent.click(body().getByRole("button", { name: /Doppler/ }));
  expect(onSelect).toHaveBeenCalledTimes(1);
  expect(onSelect).toHaveBeenCalledWith("doppler");
});
