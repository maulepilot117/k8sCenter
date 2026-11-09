/** @jsxImportSource react */
import { type ComponentType, useRef, useState } from "react";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { AWSForm } from "@/components/wizard/secretstore/AWSForm.tsx";
import { AWSPSForm } from "@/components/wizard/secretstore/AWSPSForm.tsx";
import { AzureKVForm } from "@/components/wizard/secretstore/AzureKVForm.tsx";
import { DopplerForm } from "@/components/wizard/secretstore/DopplerForm.tsx";
import { GCPSMForm } from "@/components/wizard/secretstore/GCPSMForm.tsx";
import { KubernetesForm } from "@/components/wizard/secretstore/KubernetesForm.tsx";
import { OnePasswordForm } from "@/components/wizard/secretstore/OnePasswordForm.tsx";
import { SecretStoreProviderPickerStep } from "@/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx";
import { VaultForm } from "@/components/wizard/secretstore/VaultForm.tsx";
import { ApiError, apiPost } from "@/lib/api.ts";
import {
  type ProviderFormProps,
  READY_SECRET_STORE_PROVIDERS,
  type SecretStoreProvider,
} from "@/lib/eso-types.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

export type { ProviderFormProps };

/** Registry mapping provider keys to their Configure-step form component.
 *  Single edit point as U19 sub-PRs ship additional providers. */
const PROVIDER_FORMS: Partial<
  Record<SecretStoreProvider, ComponentType<ProviderFormProps>>
> = {
  vault: VaultForm,
  onepassword: OnePasswordForm,
  aws: AWSForm,
  azurekv: AzureKVForm,
  awsps: AWSPSForm,
  gcpsm: GCPSMForm,
  kubernetes: KubernetesForm,
  doppler: DopplerForm,
};

// Re-export for any downstream consumers that imported from this island.
export type { SecretStoreProvider };

export interface SecretStoreWizardForm {
  name: string;
  namespace: string;
  provider: SecretStoreProvider | "";
  refreshInterval: string;
  /** Provider-specific spec block. Phase H Unit 18 ships an empty placeholder;
   *  Unit 19 per-provider forms write into this map. The wizard sends it
   *  verbatim under spec.provider.<provider>. */
  providerSpec: Record<string, unknown>;
}

export interface SecretStoreWizardProps {
  scope: "namespaced" | "cluster";
  onClose?: () => void;
}

// Steps shown when no per-provider form ships yet (or the user picks a
// "coming soon" provider). The Configure step is interleaved at index 2
// only when the selected provider's form is ready.
const STEPS_WITHOUT_CONFIGURE: WizardStep[] = [
  { label: "Identity", sub: "Name & namespace" },
  { label: "Provider", sub: "Backend type" },
  { label: "Review", sub: "Preview & apply" },
];

const STEPS_WITH_CONFIGURE: WizardStep[] = [
  { label: "Identity", sub: "Name & namespace" },
  { label: "Provider", sub: "Backend type" },
  { label: "Configure", sub: "Provider settings" },
  { label: "Review", sub: "Preview & apply" },
];

/** Resolve the active step list based on the currently-selected provider.
 *  Provider readiness is the single edit point as U19 sub-PRs ship — adding
 *  a provider to READY_SECRET_STORE_PROVIDERS in lib/eso-types.ts surfaces
 *  the Configure step automatically. */
function stepsFor(provider: SecretStoreProvider | ""): WizardStep[] {
  if (provider && READY_SECRET_STORE_PROVIDERS.has(provider)) {
    return STEPS_WITH_CONFIGURE;
  }
  return STEPS_WITHOUT_CONFIGURE;
}

function initialForm(scope: "namespaced" | "cluster"): SecretStoreWizardForm {
  return {
    name: "",
    namespace: scope === "namespaced" ? initialNamespace() : "",
    provider: "",
    refreshInterval: "1h",
    providerSpec: {},
  };
}

export default function SecretStoreWizard({
  scope,
  onClose,
}: SecretStoreWizardProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const isClient = useIsClient();
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<SecretStoreWizardForm>(() =>
    initialForm(scope),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const [namespaceList] = useNamespaces();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewSeq = useRef(0);
  // The preview's 422 handler runs after an await and picks the Configure
  // step from the provider chosen by then.
  const providerRef = useLatest(form.provider);

  useDirtyGuard(dirty);

  function update<K extends keyof SecretStoreWizardForm>(
    field: K,
    value: SecretStoreWizardForm[K],
  ) {
    setDirty(true);
    // Switching provider clears the spec slate cleanly so a stale value from
    // a prior provider can't leak into the new spec (L7.5 touched-flag
    // pattern applied at the provider level).
    if (field === "provider") {
      setForm((prev) => ({
        ...prev,
        provider: value as SecretStoreProvider,
        providerSpec: {},
      }));
      return;
    }
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  function validateStep(step: number): boolean {
    const f = form;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name = "Must be a valid DNS label";
      }
      if (scope === "namespaced") {
        if (!f.namespace || !DNS_LABEL_REGEX.test(f.namespace)) {
          errs.namespace = "Must be a valid DNS label";
        }
      }
      if (
        f.refreshInterval &&
        !/^(0|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$/i.test(
          f.refreshInterval,
        )
      ) {
        errs.refreshInterval = "Must be a Go duration (e.g. 1h, 30m, 1h30m)";
      }
    }

    if (step === 1 && !f.provider) {
      errs.provider = "Select a provider";
    }

    // Light client-side gate for the Configure step — only catches obviously
    // empty inputs. The server-side validator (surfaced via #1's 422 handler)
    // catches everything else and routes field errors back here on preview.
    if (step === 2 && f.provider === "vault") {
      const ps = f.providerSpec;
      const server = typeof ps.server === "string" ? ps.server.trim() : "";
      if (!server) {
        errs.server = "Server URL is required";
      }
      const auth = ps.auth as Record<string, unknown> | undefined;
      if (!auth || Object.keys(auth).length === 0) {
        errs.auth = "Select an authentication method";
      }
    }
    if (step === 2 && f.provider === "azurekv") {
      const ps = f.providerSpec;
      const vaultUrl =
        typeof ps.vaultUrl === "string" ? ps.vaultUrl.trim() : "";
      if (!vaultUrl) {
        errs.vaultUrl = "Vault URL is required";
      }
      const authType = typeof ps.authType === "string" ? ps.authType : "";
      if (!authType) {
        errs.authType = "Select an authentication type";
      }
      // tenantId is required for ServicePrincipal and WorkloadIdentity.
      if (authType === "ServicePrincipal" || authType === "WorkloadIdentity") {
        const tenantId =
          typeof ps.tenantId === "string" ? ps.tenantId.trim() : "";
        if (!tenantId) {
          errs.tenantId = "Tenant ID is required";
        }
      }
    }

    if (step === 2 && f.provider === "onepassword") {
      const ps = f.providerSpec;
      const connectHost =
        typeof ps.connectHost === "string" ? ps.connectHost.trim() : "";
      if (!connectHost) {
        errs.connectHost = "is required";
      }
    }

    if (step === 2 && f.provider === "awsps") {
      const ps = f.providerSpec;
      const region = typeof ps.region === "string" ? ps.region.trim() : "";
      if (!region) {
        errs.region = "AWS Region is required";
      }
      const auth = ps.auth as Record<string, unknown> | undefined;
      if (!auth || Object.keys(auth).length === 0) {
        errs.auth = "Select an authentication method";
      }
      // role is a top-level AWSProvider field; required when using jwt (IRSA).
      if (auth && "jwt" in auth) {
        const role = typeof ps.role === "string" ? ps.role.trim() : "";
        if (!role) {
          errs.role = "IAM role ARN is required for IRSA";
        }
      }
    }

    if (step === 2 && f.provider === "gcpsm") {
      const ps = f.providerSpec;
      const projectID =
        typeof ps.projectID === "string" ? ps.projectID.trim() : "";
      if (!projectID) {
        errs.projectID = "Project ID is required";
      }
      // Gate on auth only when an auth block is present but empty — a degenerate
      // state that should not occur via the normal picker UI but could happen if
      // providerSpec is mutated externally. When auth is absent the form encodes
      // the "Default Credentials" method, which is a valid ESO configuration
      // (GKE metadata server / Application Default Credentials) and must not be
      // blocked. This mirrors the Vault auth gate but accounts for GCP's
      // first-class default-credentials path.
      const auth = ps.auth as Record<string, unknown> | undefined;
      if (auth !== undefined && Object.keys(auth).length === 0) {
        errs.auth = "Select an authentication method";
      }
    }

    // Intentionally lighter than vault's gate: remoteNamespace is optional in
    // ESO (defaults to "default") so we don't require it client-side.
    if (step === 2 && f.provider === "kubernetes") {
      const ps = f.providerSpec;
      const auth = ps.auth as Record<string, unknown> | undefined;
      if (!auth || Object.keys(auth).length === 0) {
        errs.auth = "Select an authentication method";
      }
    }

    if (step === 2 && f.provider === "doppler") {
      const ps = f.providerSpec;
      if (!ps.project || (ps.project as string).trim() === "") {
        errs.project = "Project is required";
      }
      if (!ps.config || (ps.config as string).trim() === "") {
        errs.config = "Config is required";
      }
      const auth = ps.auth as Record<string, unknown> | undefined;
      if (
        !auth ||
        !Object.keys(auth).some((k) => ["secretRef", "oidcConfig"].includes(k))
      ) {
        errs.auth = "Select an authentication method";
      }
    }

    setErrors(errs);
    return Object.keys(errs).length === 0;
  }

  async function fetchPreview() {
    const seq = ++previewSeq.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = form;

    const payload = {
      name: f.name,
      namespace: scope === "namespaced" ? f.namespace : undefined,
      provider: f.provider,
      providerSpec: f.providerSpec,
      refreshInterval: f.refreshInterval || undefined,
    };

    const endpoint =
      scope === "cluster"
        ? "/v1/wizards/cluster-secret-store/preview"
        : "/v1/wizards/secret-store/preview";

    try {
      const resp = await apiPost<{ yaml: string }>(endpoint, payload);
      if (seq !== previewSeq.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (seq !== previewSeq.current) return;
      // On 422 the backend encodes FieldError[] as JSON in error.detail.
      // Parse it and route field errors back to the Configure step so users
      // can fix them in context rather than seeing a raw error string.
      if (err instanceof ApiError && err.status === 422) {
        const detail = err.body?.error?.detail;
        if (typeof detail === "string") {
          try {
            const fieldErrors = JSON.parse(detail) as Array<{
              field: string;
              message: string;
            }>;
            if (Array.isArray(fieldErrors) && fieldErrors.length > 0) {
              const errsMap: Record<string, string> = {};
              for (const fe of fieldErrors) {
                errsMap[fe.field] = fe.message;
              }
              setErrors(errsMap);
              // Navigate back to Configure step so field errors are visible.
              const steps = stepsFor(providerRef.current);
              const configureIdx = steps.findIndex(
                (s) => s.label === "Configure",
              );
              if (configureIdx >= 0) {
                setCurrentStep(configureIdx);
              }
              return;
            }
          } catch {
            // detail was not JSON — fall through to generic error display
          }
        }
      }
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (seq === previewSeq.current) {
        setPreviewLoading(false);
      }
    }
  }

  async function goNext() {
    if (!validateStep(currentStep)) return;
    const steps = stepsFor(form.provider);
    if (currentStep === steps.length - 2) {
      setCurrentStep(steps.length - 1);
      await fetchPreview();
    } else {
      setCurrentStep(currentStep + 1);
    }
  }

  function goBack() {
    if (currentStep > 0) setCurrentStep(currentStep - 1);
  }

  if (!isClient) return null;

  // Resolve step list once per render; all step-count references use this
  // local rather than re-calling stepsFor(form.provider) six times.
  const steps = stepsFor(form.provider);

  const title =
    scope === "cluster" ? "Create ClusterSecretStore" : "Create SecretStore";

  const detailBasePath =
    scope === "cluster"
      ? "/external-secrets/cluster-stores"
      : "/external-secrets/stores";

  // Resolve the Configure-step form component from the registry (or undefined
  // when the selected provider has no guided form yet). This is the single
  // edit point for adding new provider forms — no scattered if/else branches.
  const ProviderForm = form.provider
    ? PROVIDER_FORMS[form.provider]
    : undefined;

  const isLastStep = currentStep === steps.length - 1;

  const storeIcon = (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M21 12c0 1.66-4.03 3-9 3S3 13.66 3 12" />
      <path d="M3 5v14c0 1.66 4.03 3 9 3s9-1.34 9-3V5" />
    </svg>
  );

  return (
    <WizardShell
      title={title}
      icon={storeIcon}
      subtitle={`Step ${currentStep + 1} of ${steps.length}`}
      steps={steps}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={async () => {
        if (isLastStep) {
          close();
        } else {
          await goNext();
        }
      }}
      nextLabel={isLastStep ? "Close" : "Continue"}
      yaml={isLastStep ? previewYaml || undefined : undefined}
    >
      {currentStep === 0 && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4">
            <Input
              id="store-name"
              label="Name"
              required
              value={form.name}
              onChange={(e) => update("name", e.currentTarget.value)}
              placeholder={
                scope === "cluster" ? "shared-vault-store" : "vault-store"
              }
              error={errors.name}
            />
            {scope === "namespaced" && (
              <NamespaceSelect
                value={form.namespace}
                namespaces={namespaceList}
                error={errors.namespace}
                onChange={(ns) => update("namespace", ns)}
              />
            )}
          </div>
          <Input
            id="store-refresh"
            label="Refresh interval"
            value={form.refreshInterval}
            onChange={(e) => update("refreshInterval", e.currentTarget.value)}
            placeholder="1h"
            description="Go duration. Leave blank to use ESO's default. Use `0` to disable polling."
            error={errors.refreshInterval}
          />
        </div>
      )}

      {currentStep === 1 && (
        <div className="space-y-3">
          <SecretStoreProviderPickerStep
            selected={form.provider}
            onSelect={(p) => update("provider", p)}
          />
          {errors.provider && (
            <p className="text-sm text-danger">{errors.provider}</p>
          )}
        </div>
      )}

      {currentStep === 2 && ProviderForm && (
        <ProviderForm
          spec={form.providerSpec}
          errors={errors}
          onUpdateSpec={(spec) => update("providerSpec", spec)}
        />
      )}

      {isLastStep && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath={detailBasePath}
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
