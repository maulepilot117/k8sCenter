import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { SecretStoreProviderPickerStep } from "@/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { useRef } from "preact/hooks";

/**
 * The 12 SecretStore provider keys the wizard recognizes. Mirrors the Go
 * `SecretStoreProvider` enum in `backend/internal/wizard/secretstore.go`.
 *
 * Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud
 * Vault, Alibaba KMS, custom webhook) ship as YAML templates only (Phase H
 * Unit 20) and are not in this set.
 */
export type SecretStoreProvider =
  | "vault"
  | "aws"
  | "awsps"
  | "azurekv"
  | "gcpsm"
  | "kubernetes"
  | "akeyless"
  | "doppler"
  | "onepasswordsdk"
  | "bitwardensecretsmanager"
  | "conjur"
  | "infisical";

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

const STEPS = [
  { title: "Identity" },
  { title: "Provider" },
  { title: "Configure" },
  { title: "Review" },
];

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
    if (currentStep.value === STEPS.length - 2) {
      currentStep.value = STEPS.length - 1;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
    }
  }

  function goBack() {
    if (currentStep.value > 0) currentStep.value = currentStep.value - 1;
  }

  if (!IS_BROWSER) return <div class="p-6">Loading wizard...</div>;

  const heading = scope === "cluster"
    ? "Create ClusterSecretStore"
    : "Create SecretStore";

  const cancelHref = scope === "cluster"
    ? "/external-secrets/cluster-stores"
    : "/external-secrets/stores";

  const detailBasePath = scope === "cluster"
    ? "/external-secrets/cluster-stores"
    : "/external-secrets/stores";

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">{heading}</h1>
        <a
          href={cancelHref}
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

        {currentStep.value === 2 && (
          <div class="rounded-md border border-border-primary bg-surface/50 p-6 text-sm text-text-muted">
            <p class="font-medium text-text-primary mb-2">
              Provider configuration
            </p>
            <p>
              Per-provider configuration lands in a follow-up. Click{" "}
              <span class="font-medium text-text-primary">Preview YAML</span>
              {" "}
              to see the scaffold the backend will produce — the wizard rejects
              the preview until a provider validator is registered for{" "}
              <code class="rounded bg-base px-1 py-0.5 text-xs">
                {form.value.provider}
              </code>, surfacing what the YAML editor still needs you to fill
              in.
            </p>
          </div>
        )}

        {currentStep.value === STEPS.length - 1 && (
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

      {currentStep.value < STEPS.length - 1 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === STEPS.length - 2 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === STEPS.length - 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>Back</Button>
        </div>
      )}
    </div>
  );
}
