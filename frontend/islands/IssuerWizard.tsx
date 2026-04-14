import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { IssuerTypePickerStep } from "@/components/wizard/IssuerTypePickerStep.tsx";
import { IssuerFormStep } from "@/components/wizard/IssuerFormStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

export type IssuerType = "selfSigned" | "acme" | "ca" | "vault";
export type VaultAuthMethod = "token" | "appRole" | "kubernetes";

export interface IssuerWizardForm {
  type: IssuerType | "";
  name: string;
  namespace: string;
  acme: {
    server: string;
    email: string;
    privateKeySecretRefName: string;
    ingressClassName: string;
  };
  ca: {
    secretName: string;
  };
  vault: {
    server: string;
    path: string;
    authMethod: VaultAuthMethod;
    authValue: string;
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

const LE_STAGING = "https://acme-staging-v02.api.letsencrypt.org/directory";

function initialForm(): IssuerWizardForm {
  return {
    type: "",
    name: "",
    namespace: "default",
    acme: {
      server: LE_STAGING,
      email: "",
      privateKeySecretRefName: "",
      ingressClassName: "",
    },
    ca: { secretName: "" },
    vault: { server: "", path: "", authMethod: "token", authValue: "" },
  };
}

export default function IssuerWizard({ scope }: IssuerWizardProps) {
  const currentStep = useSignal(0);
  const form = useSignal<IssuerWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value } as IssuerWizardForm;
    // Default ACME private key secret from issuer name when untouched.
    if (
      field === "name" && typeof value === "string" && f.type === "acme" &&
      (f.acme.privateKeySecretRefName === "" ||
        f.acme.privateKeySecretRefName === `${form.value.name}-account`)
    ) {
      f.acme = {
        ...f.acme,
        privateKeySecretRefName: value ? `${value}-account` : "",
      };
    }
    form.value = f;
  }, []);

  const updateAcme = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      acme: { ...form.value.acme, [field]: value },
    };
  }, []);

  const updateCa = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      ca: { ...form.value.ca, [field]: value },
    };
  }, []);

  const updateVault = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      vault: { ...form.value.vault, [field]: value },
    };
  }, []);

  const updateVaultAuth = useCallback(
    (method: VaultAuthMethod, value: string) => {
      dirty.value = true;
      form.value = {
        ...form.value,
        vault: { ...form.value.vault, authMethod: method, authValue: value },
      };
    },
    [],
  );

  const selectType = useCallback((t: IssuerType) => {
    dirty.value = true;
    form.value = { ...form.value, type: t };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.type) errs.type = "Select an issuer type";
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

      if (f.type === "ca") {
        if (!f.ca.secretName || !DNS_LABEL_REGEX.test(f.ca.secretName)) {
          errs["ca.secretName"] = "Must be a valid DNS label";
        }
      }

      if (f.type === "vault") {
        if (!f.vault.server || !f.vault.server.startsWith("https://")) {
          errs["vault.server"] = "Vault server must be an HTTPS URL";
        }
        if (!f.vault.path) errs["vault.path"] = "PKI path is required";
        if (!f.vault.authValue) {
          errs["vault.auth"] = "One authentication method must be configured";
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
    const payload: Record<string, unknown> = {
      scope,
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
      case "ca":
        payload.ca = { secretName: f.ca.secretName };
        break;
      case "vault": {
        const auth: Record<string, unknown> = {};
        if (f.vault.authMethod === "token") {
          auth.tokenSecretRefName = f.vault.authValue;
        } else if (f.vault.authMethod === "appRole") {
          auth.appRoleSecretRefName = f.vault.authValue;
        } else {
          auth.kubernetesRole = f.vault.authValue;
        }
        payload.vault = {
          server: f.vault.server,
          path: f.vault.path,
          auth,
        };
        break;
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
            onUpdate={updateField}
            onUpdateAcme={updateAcme}
            onUpdateCa={updateCa}
            onUpdateVault={updateVault}
            onUpdateVaultAuth={updateVaultAuth}
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
            detailBasePath="/security/certificates/issuers"
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
