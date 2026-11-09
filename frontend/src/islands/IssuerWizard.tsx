/** @jsxImportSource react */
import { useRef, useState } from "react";
import { IssuerFormStep } from "@/components/wizard/IssuerFormStep.tsx";
import { IssuerTypePickerStep } from "@/components/wizard/IssuerTypePickerStep.tsx";
import { apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX, LE_STAGING_ACME } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
  const nsMeta =
    scope === "namespaced" && f.namespace
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
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<IssuerWizardForm>(initialForm);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const namespaces = useSignalValue(useNamespaces());

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => {
      const f = { ...prev, [field]: value } as IssuerWizardForm;
      // Auto-derive ACME account-key Secret name from issuer name until touched
      if (
        field === "name" &&
        typeof value === "string" &&
        f.type === "acme" &&
        !f.acme.privateKeySecretRefNameTouched
      ) {
        f.acme = {
          ...f.acme,
          privateKeySecretRefName: value ? `${value}-account` : "",
        };
      }
      return f;
    });
  };

  const updateAcme = (field: string, value: unknown) => {
    setForm((prev) => {
      const next = { ...prev.acme, [field]: value };
      if (field === "privateKeySecretRefName") {
        next.privateKeySecretRefNameTouched = true;
      }
      return { ...prev, acme: next };
    });
  };

  const selectType = (t: IssuerType) => {
    setForm((prev) =>
      t === prev.type ? prev : { ...prev, type: t, acme: initialAcme() },
    );
  };

  const validateStep = (s: number): boolean => {
    const f = form;
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
        if (!f.acme.server?.startsWith("https://")) {
          errs["acme.server"] = "ACME server must be an HTTPS URL";
        }
        if (!f.acme.email?.includes("@")) {
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = form;
    if (!f.type) {
      if (gen !== previewGen.current) return;
      setPreviewError("Issuer type is not selected");
      setPreviewLoading(false);
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
        if (gen !== previewGen.current) return;
        setPreviewError(`Unsupported issuer type: ${_exhaustive}`);
        setPreviewLoading(false);
        return;
      }
    }

    const endpoint =
      scope === "cluster"
        ? "/v1/wizards/cluster-issuer/preview"
        : "/v1/wizards/issuer/preview";
    try {
      const resp = await apiPost<{ yaml: string }>(endpoint, payload);
      if (gen !== previewGen.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (gen !== previewGen.current) return;
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (gen === previewGen.current) setPreviewLoading(false);
    }
  };

  const handleNext = async () => {
    if (!validateStep(step)) return;
    if (step === 1) {
      setStep(2);
      await fetchPreview();
    } else if (step < 2) {
      setStep((prev) => prev + 1);
    } else {
      close();
    }
  };

  const heading =
    scope === "cluster" ? "Create ClusterIssuer" : "Create Issuer";
  const f = form;

  return (
    <WizardShell
      title={heading}
      subtitle={`Step ${step + 1} of 3`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M10 2l2.4 4.8L18 7.6l-4 3.9.9 5.5L10 14.5l-4.9 2.5.9-5.5L2 7.6l5.6-.8L10 2z" />
        </svg>
      }
      steps={STEPS}
      current={step}
      onStep={(i) => {
        if (i < step) setStep(i);
      }}
      onCancel={close}
      onBack={() => setStep((prev) => Math.max(0, prev - 1))}
      onNext={handleNext}
      nextLabel={step === 2 ? "Done" : "Continue"}
      yaml={step < 2 ? buildManifest(f, scope) : undefined}
    >
      {step === 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          <IssuerTypePickerStep selected={f.type} onSelect={selectType} />
          {errors.type && (
            <p
              style={{
                fontSize: "11.5px",
                color: "var(--error)",
                marginTop: "4px",
              }}
            >
              {errors.type}
            </p>
          )}
        </div>
      )}

      {step === 1 && f.type !== "" && (
        <IssuerFormStep
          scope={scope}
          form={f}
          errors={errors}
          namespaces={namespaces}
          onUpdate={updateField}
          onUpdateAcme={updateAcme}
        />
      )}

      {step === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath={
            scope === "cluster"
              ? "/security/certificates/cluster-issuers"
              : "/security/certificates/issuers"
          }
        />
      )}
    </WizardShell>
  );
}
