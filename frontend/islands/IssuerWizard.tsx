import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, LE_STAGING_ACME } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { IssuerTypePickerStep } from "@/components/wizard/IssuerTypePickerStep.tsx";
import { IssuerFormStep } from "@/components/wizard/IssuerFormStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

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
    // True once the user has edited privateKeySecretRefName themselves;
    // we stop auto-deriving it from `name` after that.
    privateKeySecretRefNameTouched: boolean;
  };
}

interface IssuerWizardProps {
  scope: "namespaced" | "cluster";
}

const STEPS = [
  { title: "Type" },
  { title: "Configure" },
  { title: "Review" },
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

export default function IssuerWizard({ scope }: IssuerWizardProps) {
  const currentStep = useSignal(0);
  const form = useSignal<IssuerWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);
  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = (field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value } as IssuerWizardForm;
    // Auto-derive ACME account-key Secret name from the issuer name until the
    // user explicitly edits it. Uses a touched flag instead of comparing
    // against `${oldName}-account`, which breaks after switching type.
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
    dirty.value = true;
    const next = { ...form.value.acme, [field]: value };
    if (field === "privateKeySecretRefName") {
      next.privateKeySecretRefNameTouched = true;
    }
    form.value = { ...form.value, acme: next };
  };

  // Changing type resets the ACME subform so stale values from a prior session
  // never surface after toggling back.
  const selectType = (t: IssuerType) => {
    dirty.value = true;
    form.value = { ...form.value, type: t, acme: initialAcme() };
  };

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0 && !f.type) {
      errs.type = "Select an issuer type";
    }

    if (step === 1) {
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

    const payload: Record<string, unknown> = {
      name: f.name,
      type: f.type,
    };
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
        // Exhaustiveness check: adding a new IssuerType without a switch arm
        // here becomes a compile error.
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

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;
    if (currentStep.value === 1) {
      currentStep.value = 2;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value = currentStep.value - 1;
  };

  if (!IS_BROWSER) return <div class="p-6">Loading wizard...</div>;

  const heading = scope === "cluster"
    ? "Create ClusterIssuer"
    : "Create Issuer";

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">{heading}</h1>
        <a
          href="/security/certificates/issuers"
          class="text-sm text-text-muted hover:text-text-primary"
        >
          Cancel
        </a>
      </div>

      <WizardStepper
        steps={STEPS}
        currentStep={currentStep.value}
        onStepClick={(step) => {
          if (step < currentStep.value) currentStep.value = step;
        }}
      />

      <div class="mt-6">
        {currentStep.value === 0 && (
          <div class="space-y-4">
            <IssuerTypePickerStep
              selected={form.value.type}
              onSelect={selectType}
            />
            {errors.value.type && (
              <p class="text-xs text-danger">{errors.value.type}</p>
            )}
          </div>
        )}

        {currentStep.value === 1 && form.value.type !== "" && (
          <IssuerFormStep
            scope={scope}
            form={form.value}
            errors={errors.value}
            namespaces={namespaces.value}
            onUpdate={updateField}
            onUpdateAcme={updateAcme}
          />
        )}

        {currentStep.value === 2 && (
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
      </div>

      {currentStep.value < 2 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 1 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 2 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>Back</Button>
        </div>
      )}
    </div>
  );
}
