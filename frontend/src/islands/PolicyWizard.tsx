/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { PolicyConfigStep } from "@/components/wizard/PolicyConfigStep.tsx";
import { PolicyTemplateStep } from "@/components/wizard/PolicyTemplateStep.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { getTemplate } from "@/lib/policy-templates.ts";
import type { EngineStatus } from "@/lib/policy-types.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { useSignal } from "@/src/lib/signal-hooks.ts";

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

const STEPS: WizardStep[] = [
  { label: "Template", sub: "Choose policy type" },
  { label: "Configure", sub: "Engine & parameters" },
  { label: "Review", sub: "Preview & apply" },
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

export default function PolicyWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<PolicyWizardForm>(initialForm);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = useSignal(false);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const [engineStatus, setEngineStatus] = useState<EngineStatus | null>(null);

  useDirtyGuard(dirty);

  useEffect(() => {
    apiGet<EngineStatus>("/v1/policies/status")
      .then((resp) => {
        const status = resp.data;
        setEngineStatus(status);
        if (status.detected === "kyverno" || status.detected === "gatekeeper") {
          const engine = status.detected;
          setForm((prev) => ({ ...prev, engine }));
        }
      })
      .catch(() => {
        // Engine status unavailable — leave as null
      });
  }, []);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => {
        const f = { ...prev, [field]: value };

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

        return f;
      });
    },
    [dirty],
  );

  const updateParam = useCallback(
    (key: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => ({
        ...prev,
        params: { ...prev.params, [key]: value },
      }));
    },
    [dirty],
  );

  const validateStep = (step: number): boolean => {
    const f = form;
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
              !Array.isArray(list) ||
              list.length === 0 ||
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = form;
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

  const goNext = async () => {
    if (!validateStep(currentStep)) return;

    if (currentStep === 1) {
      setCurrentStep(2);
      await fetchPreview();
    } else {
      setCurrentStep((prev) => prev + 1);
    }
  };

  const goBack = () => {
    setCurrentStep((prev) => Math.max(0, prev - 1));
  };

  return (
    <WizardShell
      title="Create Policy"
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={async () => {
        if (currentStep === 2) {
          close();
        } else {
          await goNext();
        }
      }}
      nextLabel={currentStep === 2 ? "Close" : "Continue"}
      yaml={previewYaml || undefined}
    >
      {engineStatus && engineStatus.detected === "" ? (
        <div className="rounded-lg border border-border-primary bg-surface p-8 text-center">
          <svg
            className="w-12 h-12 mx-auto mb-4 text-text-muted"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="1.5"
              d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
            />
          </svg>
          <h2 className="text-lg font-semibold text-text-primary mb-2">
            No Policy Engine Detected
          </h2>
          <p className="text-text-muted mb-4">
            A policy engine is required to create policies. Install one of the
            following:
          </p>
          <div className="flex justify-center gap-6">
            <a
              href="https://kyverno.io/docs/installation/"
              target="_blank"
              rel="noopener noreferrer"
              className="text-brand hover:text-brand/80 font-medium"
            >
              Install Kyverno
            </a>
            <a
              href="https://open-policy-agent.github.io/gatekeeper/website/docs/install/"
              target="_blank"
              rel="noopener noreferrer"
              className="text-brand hover:text-brand/80 font-medium"
            >
              Install Gatekeeper
            </a>
          </div>
        </div>
      ) : (
        <>
          {currentStep === 0 && (
            <PolicyTemplateStep
              selectedId={form.templateId}
              onSelect={(id) => updateField("templateId", id)}
            />
          )}

          {currentStep === 1 && (
            <PolicyConfigStep
              form={form}
              errors={errors}
              engineStatus={engineStatus}
              onUpdate={updateField}
              onUpdateParam={updateParam}
            />
          )}

          {currentStep === 2 && (
            <WizardReviewStep
              yaml={previewYaml}
              onYamlChange={setPreviewYaml}
              loading={previewLoading}
              error={previewError}
              detailBasePath="/security/policies"
              dirty={dirty}
            />
          )}
        </>
      )}
    </WizardShell>
  );
}
