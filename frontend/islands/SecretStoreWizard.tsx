import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import type { ComponentType } from "preact";
import { ApiError, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { SecretStoreProviderPickerStep } from "@/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx";
import { VaultForm } from "@/components/wizard/secretstore/VaultForm.tsx";
import { OnePasswordForm } from "@/components/wizard/secretstore/OnePasswordForm.tsx";
import { AWSForm } from "@/components/wizard/secretstore/AWSForm.tsx";
import { AzureKVForm } from "@/components/wizard/secretstore/AzureKVForm.tsx";
import { AWSPSForm } from "@/components/wizard/secretstore/AWSPSForm.tsx";
import { GCPSMForm } from "@/components/wizard/secretstore/GCPSMForm.tsx";
import { KubernetesForm } from "@/components/wizard/secretstore/KubernetesForm.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { useRef } from "preact/hooks";
import {
  type ProviderFormProps,
  READY_SECRET_STORE_PROVIDERS,
  type SecretStoreProvider,
} from "@/lib/eso-types.ts";

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
}

// Steps shown when no per-provider form ships yet (or the user picks a
// "coming soon" provider). The Configure step is interleaved at index 2
// only when the selected provider's form is ready.
const STEPS_WITHOUT_CONFIGURE = [
  { title: "Identity" },
  { title: "Provider" },
  { title: "Review" },
];

const STEPS_WITH_CONFIGURE = [
  { title: "Identity" },
  { title: "Provider" },
  { title: "Configure" },
  { title: "Review" },
];

/** Resolve the active step list based on the currently-selected provider.
 *  Provider readiness is the single edit point as U19 sub-PRs ship — adding
 *  a provider to READY_SECRET_STORE_PROVIDERS in lib/eso-types.ts surfaces
 *  the Configure step automatically. */
function stepsFor(
  provider: SecretStoreProvider | "",
): typeof STEPS_WITHOUT_CONFIGURE {
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

export default function SecretStoreWizard({ scope }: SecretStoreWizardProps) {
  const currentStep = useSignal(0);
  const form = useSignal<SecretStoreWizardForm>(initialForm(scope));
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);
  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const previewSeq = useRef(0);

  useDirtyGuard(dirty);

  function update<K extends keyof SecretStoreWizardForm>(
    field: K,
    value: SecretStoreWizardForm[K],
  ) {
    dirty.value = true;
    // Switching provider clears the spec slate cleanly so a stale value from
    // a prior provider can't leak into the new spec (L7.5 touched-flag
    // pattern applied at the provider level).
    if (field === "provider") {
      form.value = {
        ...form.value,
        provider: value as SecretStoreProvider,
        providerSpec: {},
      };
      return;
    }
    form.value = { ...form.value, [field]: value };
  }

  function validateStep(step: number): boolean {
    const f = form.value;
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
      const vaultUrl = typeof ps.vaultUrl === "string"
        ? ps.vaultUrl.trim()
        : "";
      if (!vaultUrl) {
        errs.vaultUrl = "Vault URL is required";
      }
      const authType = typeof ps.authType === "string" ? ps.authType : "";
      if (!authType) {
        errs.authType = "Select an authentication type";
      }
      // tenantId is required for ServicePrincipal and WorkloadIdentity.
      if (authType === "ServicePrincipal" || authType === "WorkloadIdentity") {
        const tenantId = typeof ps.tenantId === "string"
          ? ps.tenantId.trim()
          : "";
        if (!tenantId) {
          errs.tenantId = "Tenant ID is required";
        }
      }
    }

    if (step === 2 && f.provider === "onepassword") {
      const ps = f.providerSpec;
      const connectHost = typeof ps.connectHost === "string"
        ? ps.connectHost.trim()
        : "";
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
      const projectID = typeof ps.projectID === "string"
        ? ps.projectID.trim()
        : "";
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

    errors.value = errs;
    return Object.keys(errs).length === 0;
  }

  async function fetchPreview() {
    const seq = ++previewSeq.current;
    previewLoading.value = true;
    previewError.value = null;
    const f = form.value;

    const payload = {
      name: f.name,
      namespace: scope === "namespaced" ? f.namespace : undefined,
      provider: f.provider,
      providerSpec: f.providerSpec,
      refreshInterval: f.refreshInterval || undefined,
    };

    const endpoint = scope === "cluster"
      ? "/v1/wizards/cluster-secret-store/preview"
      : "/v1/wizards/secret-store/preview";

    try {
      const resp = await apiPost<{ yaml: string }>(endpoint, payload);
      if (seq !== previewSeq.current) return;
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      if (seq !== previewSeq.current) return;
      // On 422 the backend encodes FieldError[] as JSON in error.detail.
      // Parse it and route field errors back to the Configure step so users
      // can fix them in context rather than seeing a raw error string.
      if (err instanceof ApiError && err.status === 422) {
        const detail = err.body?.error?.detail;
        if (typeof detail === "string") {
          try {
            const fieldErrors = JSON.parse(detail) as Array<
              { field: string; message: string }
            >;
            if (Array.isArray(fieldErrors) && fieldErrors.length > 0) {
              const errsMap: Record<string, string> = {};
              for (const fe of fieldErrors) {
                errsMap[fe.field] = fe.message;
              }
              errors.value = errsMap;
              // Navigate back to Configure step so field errors are visible.
              const steps = stepsFor(form.value.provider);
              const configureIdx = steps.findIndex((s) =>
                s.title === "Configure"
              );
              if (configureIdx >= 0) {
                currentStep.value = configureIdx;
              }
              return;
            }
          } catch {
            // detail was not JSON — fall through to generic error display
          }
        }
      }
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      if (seq === previewSeq.current) {
        previewLoading.value = false;
      }
    }
  }

  async function goNext() {
    if (!validateStep(currentStep.value)) return;
    const steps = stepsFor(form.value.provider);
    if (currentStep.value === steps.length - 2) {
      currentStep.value = steps.length - 1;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
    }
  }

  function goBack() {
    if (currentStep.value > 0) currentStep.value = currentStep.value - 1;
  }

  if (!IS_BROWSER) return <div class="p-6">Loading wizard...</div>;

  // Resolve step list once per render; all step-count references use this
  // local rather than re-calling stepsFor(form.value.provider) six times.
  const steps = stepsFor(form.value.provider);

  const heading = scope === "cluster"
    ? "Create ClusterSecretStore"
    : "Create SecretStore";

  const detailBasePath = scope === "cluster"
    ? "/external-secrets/cluster-stores"
    : "/external-secrets/stores";

  // Resolve the Configure-step form component from the registry (or undefined
  // when the selected provider has no guided form yet). This is the single
  // edit point for adding new provider forms — no scattered if/else branches.
  const ProviderForm = form.value.provider
    ? PROVIDER_FORMS[form.value.provider]
    : undefined;

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">{heading}</h1>
        <a
          href={detailBasePath}
          class="text-sm text-text-muted hover:text-text-primary"
        >
          Cancel
        </a>
      </div>

      <WizardStepper
        steps={steps}
        currentStep={currentStep.value}
        onStepClick={(step) => {
          if (step < currentStep.value) currentStep.value = step;
        }}
      />

      <div class="mt-6">
        {currentStep.value === 0 && (
          <div class="space-y-5">
            <div class="grid grid-cols-2 gap-4">
              <Input
                id="store-name"
                label="Name"
                required
                value={form.value.name}
                onInput={(e) =>
                  update("name", (e.target as HTMLInputElement).value)}
                placeholder={scope === "cluster"
                  ? "shared-vault-store"
                  : "vault-store"}
                error={errors.value.name}
              />
              {scope === "namespaced" && (
                <NamespaceSelect
                  value={form.value.namespace}
                  namespaces={namespaces.value}
                  error={errors.value.namespace}
                  onChange={(ns) => update("namespace", ns)}
                />
              )}
            </div>
            <Input
              id="store-refresh"
              label="Refresh interval"
              value={form.value.refreshInterval}
              onInput={(e) =>
                update(
                  "refreshInterval",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="1h"
              description="Go duration. Leave blank to use ESO's default. Use `0` to disable polling."
              error={errors.value.refreshInterval}
            />
          </div>
        )}

        {currentStep.value === 1 && (
          <div class="space-y-3">
            <SecretStoreProviderPickerStep
              selected={form.value.provider}
              onSelect={(p) => update("provider", p)}
            />
            {errors.value.provider && (
              <p class="text-sm text-danger">{errors.value.provider}</p>
            )}
          </div>
        )}

        {currentStep.value === 2 && ProviderForm && (
          <ProviderForm
            spec={form.value.providerSpec}
            errors={errors.value}
            onUpdateSpec={(spec) => update("providerSpec", spec)}
          />
        )}

        {currentStep.value === steps.length - 1 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath={detailBasePath}
          />
        )}
      </div>

      {currentStep.value < steps.length - 1 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === steps.length - 2 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === steps.length - 1 &&
        !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>Back</Button>
        </div>
      )}
    </div>
  );
}
