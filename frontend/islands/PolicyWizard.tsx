import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { PolicyTemplateStep } from "@/components/wizard/PolicyTemplateStep.tsx";
import { PolicyConfigStep } from "@/components/wizard/PolicyConfigStep.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { getTemplate } from "@/lib/policy-templates.ts";
import type { EngineStatus } from "@/lib/policy-types.ts";

export interface PolicyWizardForm {
  templateId: string;
  engine: "kyverno" | "gatekeeper" | "";
  name: string;
  action: string;
  targetKinds: string[];
  excludedNamespaces: string[];
  description: string;
  params: Record<string, unknown>;
}

const STEPS = [
  { title: "Template" },
  { title: "Configure" },
  { title: "Review" },
];

function initialForm(): PolicyWizardForm {
  return {
    templateId: "",
    engine: "",
    name: "",
    action: "",
    targetKinds: [],
    excludedNamespaces: ["kube-system", "kube-public", "kube-node-lease"],
    description: "",
    params: {},
  };
}

export default function PolicyWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<PolicyWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const engineStatus = useSignal<EngineStatus | null>(null);

  useDirtyGuard(dirty);

  useEffect(() => {
    apiGet<EngineStatus>("/v1/policies/status").then((resp) => {
      const status = resp.data;
      engineStatus.value = status;
      if (status.detected === "kyverno" || status.detected === "gatekeeper") {
        form.value = { ...form.value, engine: status.detected };
      }
    }).catch(() => {
      // Engine status unavailable — leave as null
    });
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value };

    if (field === "templateId") {
      const tmpl = getTemplate(value as string);
      if (tmpl) {
        f.name = f.name || tmpl.id;
        f.targetKinds = tmpl.targetKinds;
        f.description = tmpl.description;
        const defaults: Record<string, unknown> = {};
        for (const pf of tmpl.paramFields) {
          defaults[pf.key] = pf.defaultValue ?? "";
        }
        f.params = defaults;
        if (f.engine === "kyverno") {
          f.action = tmpl.defaultKyvernoAction;
        } else if (f.engine === "gatekeeper") {
          f.action = tmpl.defaultGatekeeperAction;
        }
      }
    }

    if (field === "engine") {
      const tmpl = getTemplate(f.templateId);
      if (tmpl) {
        if (value === "kyverno") {
          f.action = tmpl.defaultKyvernoAction;
        } else if (value === "gatekeeper") {
          f.action = tmpl.defaultGatekeeperAction;
        }
      }
    }

    form.value = f;
  }, []);

  const updateParam = useCallback((key: string, value: unknown) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      params: { ...form.value.params, [key]: value },
    };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.templateId) {
        errs.templateId = "Select a policy template";
      }
    }

    if (step === 1) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.engine) {
        errs.engine = "Select a policy engine";
      }
      if (!f.action) {
        errs.action = "Select an action";
      }
      if (f.targetKinds.length === 0) {
        errs.targetKinds = "Select at least one target kind";
      }

      const tmpl = getTemplate(f.templateId);
      if (tmpl) {
        for (const pf of tmpl.paramFields) {
          if (pf.required && pf.type === "stringList") {
            const list = f.params[pf.key];
            if (
              !Array.isArray(list) || list.length === 0 ||
              list.every((v) => !(v as string).trim())
            ) {
              errs[`param.${pf.key}`] = `${pf.label} is required`;
            }
          }
          if (pf.required && pf.type === "string") {
            const val = f.params[pf.key];
            if (!val || !(val as string).trim()) {
              errs[`param.${pf.key}`] = `${pf.label} is required`;
            }
          }
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
      templateId: f.templateId,
      engine: f.engine,
      name: f.name,
      action: f.action,
      targetKinds: f.targetKinds,
      description: f.description,
    };

    if (f.excludedNamespaces.length > 0) {
      payload.excludedNamespaces = f.excludedNamespaces;
    }

    if (Object.keys(f.params).length > 0) {
      payload.params = f.params;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/policy/preview",
        payload,
      );
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
    if (currentStep.value > 0) {
      currentStep.value = currentStep.value - 1;
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  if (engineStatus.value && engineStatus.value.detected === "") {
    return (
      <div class="p-6 max-w-4xl mx-auto">
        <h1 class="text-2xl font-bold text-text-primary mb-6">
          Create Policy
        </h1>
        <div class="rounded-lg border border-border-primary bg-surface p-8 text-center">
          <svg
            class="w-12 h-12 mx-auto mb-4 text-text-muted"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
            />
          </svg>
          <h2 class="text-lg font-semibold text-text-primary mb-2">
            No Policy Engine Detected
          </h2>
          <p class="text-text-muted mb-4">
            A policy engine is required to create policies. Install one of the
            following:
          </p>
          <div class="flex justify-center gap-6">
            <a
              href="https://kyverno.io/docs/installation/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-brand hover:text-brand/80 font-medium"
            >
              Install Kyverno
            </a>
            <a
              href="https://open-policy-agent.github.io/gatekeeper/website/docs/install/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-brand hover:text-brand/80 font-medium"
            >
              Install Gatekeeper
            </a>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">Create Policy</h1>
        <a
          href="/security/policies"
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
          <PolicyTemplateStep
            selectedId={form.value.templateId}
            onSelect={(id) => updateField("templateId", id)}
          />
        )}

        {currentStep.value === 1 && (
          <PolicyConfigStep
            form={form.value}
            errors={errors.value}
            engineStatus={engineStatus.value}
            onUpdate={updateField}
            onUpdateParam={updateParam}
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
            detailBasePath="/security/policies"
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
          <Button variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
