import { expect, mock, test } from "bun:test";
import type { Issuer } from "@/lib/certmanager-types.ts";
import type { EngineStatus } from "@/lib/policy-types.ts";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { CertificateForm } = await import("./CertificateForm.tsx");
const { IssuerFormStep } = await import("./IssuerFormStep.tsx");
const { IssuerTypePickerStep } = await import("./IssuerTypePickerStep.tsx");
const { PolicyConfigStep } = await import("./PolicyConfigStep.tsx");
const { PolicyTemplateStep } = await import("./PolicyTemplateStep.tsx");

const certForm = {
  name: "web",
  namespace: "default",
  secretName: "web-tls",
  issuerRefValue: "",
  dnsNamesInput: "",
  commonName: "",
  duration: "",
  renewBefore: "",
  privateKey: {
    algorithm: "RSA" as const,
    size: 2048,
    rotationPolicy: "Always" as const,
  },
  isCA: false,
};

const issuers = [
  { name: "le", scope: "Cluster", type: "ACME", uid: "u1" },
  {
    name: "own",
    namespace: "shop",
    scope: "Namespaced",
    type: "CA",
    uid: "u2",
  },
] as unknown as Issuer[];

function renderCert() {
  const onUpdate = mock((_f: string, _v: unknown) => {});
  const onUpdatePrivateKey = mock((_f: string, _v: unknown) => {});
  render(
    <CertificateForm
      form={certForm}
      errors={{ name: "name is bad", dnsNames: "need a name" }}
      issuers={issuers}
      issuersLoading={false}
      namespaces={["default", "shop"]}
      onUpdate={onUpdate}
      onUpdatePrivateKey={onUpdatePrivateKey}
    />,
  );
  return { onUpdate, onUpdatePrivateKey };
}

test("CertificateForm shows fields, errors and issuer groups", () => {
  renderCert();
  body().getByLabelText("Secret Name");
  body().getByText("name is bad");
  body().getByText("need a name");
  body().getByText("le (ACME)");
  body().getByText("own / shop (CA)");
});

test("CertificateForm reports typing, select and checkbox changes", () => {
  const { onUpdate, onUpdatePrivateKey } = renderCert();
  fireEvent.input(body().getByLabelText("Secret Name"), {
    target: { value: "new-tls" },
  });
  expect(onUpdate).toHaveBeenCalledWith("secretName", "new-tls");
  fireEvent.change(body().getByLabelText("Issuer"), {
    target: { value: "ClusterIssuer:le" },
  });
  expect(onUpdate).toHaveBeenCalledWith("issuerRefValue", "ClusterIssuer:le");
  fireEvent.change(body().getByLabelText("Algorithm"), {
    target: { value: "ECDSA" },
  });
  expect(onUpdatePrivateKey).toHaveBeenCalledWith("algorithm", "ECDSA");
  fireEvent.change(body().getByLabelText("Key Size"), {
    target: { value: "4096" },
  });
  expect(onUpdatePrivateKey).toHaveBeenCalledWith("size", 4096);
  fireEvent.click(body().getByRole("checkbox"));
  expect(onUpdate).toHaveBeenCalledWith("isCA", true);
});

const issuerForm = (type: "selfSigned" | "acme") => ({
  type,
  name: "iss",
  namespace: "default",
  acme: {
    server: "https://x/dir",
    email: "a@b.c",
    privateKeySecretRefName: "key",
    ingressClassName: "",
    privateKeySecretRefNameTouched: false,
  },
});

test("IssuerFormStep self-signed shows the note", () => {
  render(
    <IssuerFormStep
      scope="cluster"
      form={issuerForm("selfSigned")}
      errors={{}}
      namespaces={[]}
      onUpdate={() => {}}
      onUpdateAcme={() => {}}
    />,
  );
  body().getByText(/no additional configuration/);
  expect(body().queryByLabelText("ACME Server")).toBeNull();
});

test("IssuerFormStep acme reports edits and presets and shows errors", () => {
  const onUpdate = mock((_f: string, _v: unknown) => {});
  const onUpdateAcme = mock((_f: string, _v: unknown) => {});
  render(
    <IssuerFormStep
      scope="namespaced"
      form={issuerForm("acme")}
      errors={{ "acme.email": "bad email" }}
      namespaces={["default"]}
      onUpdate={onUpdate}
      onUpdateAcme={onUpdateAcme}
    />,
  );
  body().getByText("bad email");
  fireEvent.input(body().getByLabelText("Name"), { target: { value: "n2" } });
  expect(onUpdate).toHaveBeenCalledWith("name", "n2");
  fireEvent.input(body().getByLabelText("Contact Email"), {
    target: { value: "z@y.x" },
  });
  expect(onUpdateAcme).toHaveBeenCalledWith("email", "z@y.x");
  fireEvent.click(body().getByRole("button", { name: /Staging/ }));
  expect(onUpdateAcme).toHaveBeenCalledWith(
    "server",
    expect.stringContaining("staging"),
  );
});

test("IssuerTypePickerStep marks the selection and reports a pick", () => {
  const onSelect = mock((_t: string) => {});
  render(<IssuerTypePickerStep selected="acme" onSelect={onSelect} />);
  body().getByText("Selected");
  fireEvent.click(body().getByRole("button", { name: /Self-Signed/ }));
  expect(onSelect).toHaveBeenCalledWith("selfSigned");
});

function renderPolicy(detected: EngineStatus["detected"] = "both") {
  const onUpdate = mock((_f: string, _v: unknown) => {});
  const onUpdateParam = mock((_k: string, _v: unknown) => {});
  render(
    <PolicyConfigStep
      form={{
        templateId: "restrict-capabilities",
        engine: "kyverno",
        name: "p1",
        action: "Audit",
        targetKinds: ["Pod"],
        excludedNamespaces: ["kube-system"],
        description: "",
        params: { dropAll: true, allowedAdd: ["NET_RAW"] },
      }}
      errors={{ name: "name required", "param.allowedAdd": "bad cap" }}
      engineStatus={{ detected, lastChecked: "" } as EngineStatus}
      onUpdate={onUpdate}
      onUpdateParam={onUpdateParam}
    />,
  );
  return { onUpdate, onUpdateParam };
}

test("PolicyConfigStep shows fields and errors", () => {
  renderPolicy();
  body().getByText("name required");
  body().getByText("bad cap");
  body().getByText("Template Parameters");
  body().getByDisplayValue("kube-system");
  body().getByText("Kyverno");
  body().getByText("Gatekeeper");
});

test("PolicyConfigStep reports edits", () => {
  const { onUpdate, onUpdateParam } = renderPolicy();
  fireEvent.input(body().getByPlaceholderText("disallow-privileged"), {
    target: { value: "p2" },
  });
  expect(onUpdate).toHaveBeenCalledWith("name", "p2");
  fireEvent.change(body().getByRole("combobox"), {
    target: { value: "Enforce" },
  });
  expect(onUpdate).toHaveBeenCalledWith("action", "Enforce");
  fireEvent.click(body().getByRole("button", { name: "Deployment" }));
  expect(onUpdate).toHaveBeenCalledWith("targetKinds", ["Pod", "Deployment"]);
  fireEvent.click(body().getByRole("button", { name: "Pod" }));
  expect(onUpdate).toHaveBeenCalledWith("targetKinds", []);
  fireEvent.click(body().getByRole("button", { name: "+ Add namespace" }));
  expect(onUpdate).toHaveBeenCalledWith("excludedNamespaces", [
    "kube-system",
    "",
  ]);
  fireEvent.input(body().getByDisplayValue("kube-system"), {
    target: { value: "ops" },
  });
  expect(onUpdate).toHaveBeenCalledWith("excludedNamespaces", ["ops"]);
  fireEvent.click(body().getByRole("radio", { name: "Gatekeeper" }));
  expect(onUpdate).toHaveBeenCalledWith("engine", "gatekeeper");
  fireEvent.click(body().getByRole("checkbox"));
  expect(onUpdateParam).toHaveBeenCalledWith("dropAll", false);
  fireEvent.input(body().getByDisplayValue("NET_RAW"), {
    target: { value: "NET_ADMIN" },
  });
  expect(onUpdateParam).toHaveBeenCalledWith("allowedAdd", ["NET_ADMIN"]);
  fireEvent.click(body().getByRole("button", { name: "+ Add value" }));
  expect(onUpdateParam).toHaveBeenCalledWith("allowedAdd", ["NET_RAW", ""]);
});

test("PolicyConfigStep with a single engine shows it auto-selected", () => {
  renderPolicy("gatekeeper");
  body().getByText(/Auto-selected/);
  expect(body().queryByRole("radio")).toBeNull();
});

test("PolicyTemplateStep lists templates and reports a pick", () => {
  const onSelect = mock((_id: string) => {});
  render(<PolicyTemplateStep selectedId="" onSelect={onSelect} />);
  body().getByText("Pod Security");
  fireEvent.click(body().getByText("Restrict Image Registries"));
  expect(onSelect).toHaveBeenCalledWith("allowed-registries");
});
