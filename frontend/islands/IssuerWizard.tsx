import { useSignal } from "@preact/signals";
import { apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, LE_STAGING_ACME } from "@/lib/wizard-constants.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { IssuerTypePickerStep } from "@/components/wizard/IssuerTypePickerStep.tsx";
import { IssuerFormStep } from "@/components/wizard/IssuerFormStep.tsx";

export type IssuerType = "selfSigned" | "acme";

export interface IssuerWizardForm {
  type: IssuerType | "";
  name: string;
  namespace: string;
  acme: {
    server: string;
    email: string;
    privateKeySecretRefName: string;
    ingressClassName: string;
    privateKeySecretRefNameTouched: boolean;
  };
}

interface IssuerWizardProps {
  scope: "namespaced" | "cluster";
  onClose?: () => void;
}

const STEPS: WizardStep[] = [
  { label: "Type", sub: "SelfSigned or ACME" },
  { label: "Configure", sub: "Name & settings" },
  { label: "Review", sub: "Preview & apply" },
];

function initialAcme(): IssuerWizardForm["acme"] {
  return {
    server: LE_STAGING_ACME,
    email: "",
    privateKeySecretRefName: "",
    ingressClassName: "",
    privateKeySecretRefNameTouched: false,
  };
}

function initialForm(): IssuerWizardForm {
  return {
    type: "",
    name: "",
    namespace: initialNamespace(),
    acme: initialAcme(),
  };
}

function buildManifest(
  f: IssuerWizardForm,
  scope: "namespaced" | "cluster",
): string {
  const kind = scope === "cluster" ? "ClusterIssuer" : "Issuer";
  const nsMeta = scope === "namespaced" && f.namespace
    ? `\n  namespace: ${f.namespace}`
    : "";
  if (!f.type) {
    return `apiVersion: cert-manager.io/v1\nkind: ${kind}\nmetadata:\n  name: ${
      f.name || "<name>"
    }${nsMeta}\nspec: {}`;
  }
  if (f.type === "selfSigned") {
    return `apiVersion: cert-manager.io/v1
kind: ${kind}
metadata:
  name: ${f.name || "<name>"}${nsMeta}
spec:
  selfSigned: {}`;
  }
  // acme
  const solver = f.acme.ingressClassName
    ? `\n        ingressClassName: ${f.acme.ingressClassName}`
    : "";
  return `apiVersion: cert-manager.io/v1
kind: ${kind}
metadata:
  name: ${f.name || "<name>"}${nsMeta}
spec:
  acme:
    server: ${f.acme.server || "<acme-server>"}
    email: ${f.acme.email || "<email>"}
    privateKeySecretRef:
      name: ${f.acme.privateKeySecretRefName || "<secret-name>"}
    solvers:
      - http01:
          ingress:{}${solver}`;
}

export default function IssuerWizard({ scope, onClose }: IssuerWizardProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const step = useSignal(0);
  const form = useSignal<IssuerWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  const updateField = (field: string, value: unknown) => {
    const f = { ...form.value, [field]: value } as IssuerWizardForm;
    // Auto-derive ACME account-key Secret name from issuer name until touched
    if (
      field === "name" && typeof value === "string" && f.type === "acme" &&
      !f.acme.privateKeySecretRefNameTouched
    ) {
      f.acme = {
        ...f.acme,
        privateKeySecretRefName: value ? `${value}-account` : "",
      };
    }
    form.value = f;
  };

  const updateAcme = (field: string, value: unknown) => {
    const next = { ...form.value.acme, [field]: value };
    if (field === "privateKeySecretRefName") {
      next.privateKeySecretRefNameTouched = true;
    }
    form.value = { ...form.value, acme: next };
  };

  const selectType = (t: IssuerType) => {
    if (t === form.value.type) return;
    form.value = { ...form.value, type: t, acme: initialAcme() };
  };

  const validateStep = (s: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (s === 0 && !f.type) {
      errs.type = "Select an issuer type";
    }

    if (s === 1) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name = "Must be a valid DNS label";
      }
      if (scope === "namespaced") {
        if (!f.namespace || !DNS_LABEL_REGEX.test(f.namespace)) {
          errs.namespace = "Must be a valid DNS label";
        }
      }
      if (f.type === "acme") {
        if (!f.acme.server || !f.acme.server.startsWith("https://")) {
          errs["acme.server"] = "ACME server must be an HTTPS URL";
        }
        if (!f.acme.email || !f.acme.email.includes("@")) {
          errs["acme.email"] = "A valid email is required";
        }
        if (
          !f.acme.privateKeySecretRefName ||
          !DNS_LABEL_REGEX.test(f.acme.privateKeySecretRefName)
        ) {
          errs["acme.privateKeySecretRefName"] = "Must be a valid DNS label";
        }
      }
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;
    const f = form.value;
    if (!f.type) {
      previewError.value = "Issuer type is not selected";
      previewLoading.value = false;
      return;
    }
    const payload: Record<string, unknown> = { name: f.name, type: f.type };
    if (scope === "namespaced") payload.namespace = f.namespace;

    switch (f.type) {
      case "selfSigned":
        payload.selfSigned = {};
        break;
      case "acme": {
        const solver: Record<string, unknown> = { http01Ingress: {} };
        if (f.acme.ingressClassName) {
          (solver.http01Ingress as Record<string, unknown>).ingressClassName =
            f.acme.ingressClassName;
        }
        payload.acme = {
          server: f.acme.server,
          email: f.acme.email,
          privateKeySecretRefName: f.acme.privateKeySecretRefName,
          solvers: [solver],
        };
        break;
      }
      default: {
        const _exhaustive: never = f.type;
        previewError.value = `Unsupported issuer type: ${_exhaustive}`;
        previewLoading.value = false;
        return;
      }
    }

    const endpoint = scope === "cluster"
      ? "/v1/wizards/cluster-issuer/preview"
      : "/v1/wizards/issuer/preview";
    try {
      const resp = await apiPost<{ yaml: string }>(endpoint, payload);
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
    }
  };

  const handleNext = async () => {
    if (!validateStep(step.value)) return;
    if (step.value === 1) {
      step.value = 2;
      await fetchPreview();
    } else if (step.value < 2) {
      step.value += 1;
    } else {
      close();
    }
  };

  const heading = scope === "cluster"
    ? "Create ClusterIssuer"
    : "Create Issuer";
  const f = form.value;

  return (
    <WizardShell
      title={heading}
      subtitle={`Step ${step.value + 1} of 3`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M10 2l2.4 4.8L18 7.6l-4 3.9.9 5.5L10 14.5l-4.9 2.5.9-5.5L2 7.6l5.6-.8L10 2z" />
        </svg>
      }
      steps={STEPS}
      current={step.value}
      onStep={(i) => {
        if (i < step.value) step.value = i;
      }}
      onCancel={close}
      onBack={() => (step.value = Math.max(0, step.value - 1))}
      onNext={handleNext}
      nextLabel={step.value === 1
        ? "Preview YAML"
        : step.value === 2
        ? "Done"
        : "Continue"}
      yaml={step.value < 2 ? buildManifest(f, scope) : undefined}
    >
      {step.value === 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          <IssuerTypePickerStep
            selected={f.type}
            onSelect={selectType}
          />
          {errors.value.type && (
            <p
              style={{
                fontSize: "11.5px",
                color: "var(--error)",
                marginTop: "4px",
              }}
            >
              {errors.value.type}
            </p>
          )}
        </div>
      )}

      {step.value === 1 && f.type !== "" && (
        <IssuerFormStep
          scope={scope}
          form={f}
          errors={errors.value}
          namespaces={namespaces.value}
          onUpdate={updateField}
          onUpdateAcme={updateAcme}
        />
      )}

      {step.value === 2 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => {
            previewYaml.value = v;
          }}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath={scope === "cluster"
            ? "/security/certificates/cluster-issuers"
            : "/security/certificates/issuers"}
        />
      )}
    </WizardShell>
  );
}
