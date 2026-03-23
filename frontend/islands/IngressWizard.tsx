import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface IngressPathState {
  path: string;
  pathType: "Prefix" | "Exact" | "ImplementationSpecific";
  serviceName: string;
  servicePort: number;
}

interface IngressRuleState {
  host: string;
  paths: IngressPathState[];
}

interface IngressTLSState {
  hosts: string[];
  secretName: string;
}

interface IngressFormState {
  name: string;
  namespace: string;
  ingressClassName: string;
  rules: IngressRuleState[];
  enableTLS: boolean;
  tls: IngressTLSState[];
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

const PATH_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "Prefix", label: "Prefix" },
  { value: "Exact", label: "Exact" },
  { value: "ImplementationSpecific", label: "ImplementationSpecific" },
];

function newPath(): IngressPathState {
  return { path: "/", pathType: "Prefix", serviceName: "", servicePort: 80 };
}

function newRule(): IngressRuleState {
  return { host: "", paths: [newPath()] };
}

function initialState(): IngressFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    ingressClassName: "",
    rules: [newRule()],
    enableTLS: false,
    tls: [],
  };
}

export default function IngressWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<IngressFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  useDirtyGuard(dirty);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  const markDirty = useCallback(() => {
    dirty.value = true;
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const updateRule = useCallback(
    (ruleIdx: number, field: string, value: unknown) => {
      dirty.value = true;
      const rules = [...form.value.rules];
      rules[ruleIdx] = { ...rules[ruleIdx], [field]: value };
      form.value = { ...form.value, rules };
    },
    [],
  );

  const updatePath = useCallback(
    (ruleIdx: number, pathIdx: number, field: string, value: unknown) => {
      dirty.value = true;
      const rules = [...form.value.rules];
      const paths = [...rules[ruleIdx].paths];
      paths[pathIdx] = { ...paths[pathIdx], [field]: value };
      rules[ruleIdx] = { ...rules[ruleIdx], paths };
      form.value = { ...form.value, rules };
    },
    [],
  );

  const addRule = useCallback(() => {
    markDirty();
    form.value = { ...form.value, rules: [...form.value.rules, newRule()] };
  }, []);

  const removeRule = useCallback((ruleIdx: number) => {
    markDirty();
    const rules = form.value.rules.filter((_, i) => i !== ruleIdx);
    form.value = {
      ...form.value,
      rules: rules.length > 0 ? rules : [newRule()],
    };
  }, []);

  const addPath = useCallback((ruleIdx: number) => {
    markDirty();
    const rules = [...form.value.rules];
    rules[ruleIdx] = {
      ...rules[ruleIdx],
      paths: [...rules[ruleIdx].paths, newPath()],
    };
    form.value = { ...form.value, rules };
  }, []);

  const removePath = useCallback((ruleIdx: number, pathIdx: number) => {
    markDirty();
    const rules = [...form.value.rules];
    const paths = rules[ruleIdx].paths.filter((_, i) => i !== pathIdx);
    rules[ruleIdx] = {
      ...rules[ruleIdx],
      paths: paths.length > 0 ? paths : [newPath()],
    };
    form.value = { ...form.value, rules };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.namespace) errs.namespace = "Required";

      if (f.rules.length === 0) {
        errs.rules = "At least one rule is required";
      }

      f.rules.forEach((rule, i) => {
        if (rule.paths.length === 0) {
          errs[`rules[${i}].paths`] = "At least one path is required";
        }
        rule.paths.forEach((p, j) => {
          if (!p.path.startsWith("/")) {
            errs[`rules[${i}].paths[${j}].path`] = "Must start with /";
          }
          if (!p.serviceName) {
            errs[`rules[${i}].paths[${j}].serviceName`] = "Required";
          } else if (!DNS_LABEL_REGEX.test(p.serviceName)) {
            errs[`rules[${i}].paths[${j}].serviceName`] =
              "Must be a valid DNS label";
          }
          if (p.servicePort < 1 || p.servicePort > MAX_PORT) {
            errs[`rules[${i}].paths[${j}].servicePort`] =
              `Must be 1-${MAX_PORT}`;
          }
        });
      });

      if (f.enableTLS) {
        f.tls.forEach((t, i) => {
          if (t.hosts.length === 0) {
            errs[`tls[${i}].hosts`] = "At least one host is required";
          }
          if (!t.secretName) {
            errs[`tls[${i}].secretName`] = "Required";
          } else if (!DNS_LABEL_REGEX.test(t.secretName)) {
            errs[`tls[${i}].secretName`] = "Must be a valid DNS label";
          }
        });
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
      name: f.name,
      namespace: f.namespace,
      rules: f.rules.map((r) => ({
        host: r.host || undefined,
        paths: r.paths.map((p) => ({
          path: p.path,
          pathType: p.pathType,
          serviceName: p.serviceName,
          servicePort: p.servicePort,
        })),
      })),
    };

    if (f.ingressClassName) {
      payload.ingressClassName = f.ingressClassName;
    }

    if (f.enableTLS && f.tls.length > 0) {
      payload.tls = f.tls.map((t) => ({
        hosts: t.hosts,
        secretName: t.secretName,
      }));
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/ingress/preview",
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

    if (currentStep.value === 0) {
      currentStep.value = 1;
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

  // Sync TLS hosts from rules when TLS is toggled on
  const toggleTLS = useCallback((enabled: boolean) => {
    dirty.value = true;
    const f = form.value;
    if (enabled) {
      // Collect unique hosts from rules
      const hosts = f.rules
        .map((r) => r.host)
        .filter((h) => h !== "");
      const uniqueHosts = [...new Set(hosts)];
      form.value = {
        ...f,
        enableTLS: true,
        tls: uniqueHosts.length > 0
          ? [{ hosts: uniqueHosts, secretName: "" }]
          : [{ hosts: [], secretName: "" }],
      };
    } else {
      form.value = { ...f, enableTLS: false, tls: [] };
    }
  }, []);

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create Ingress
        </h1>
        <a
          href="/networking/ingresses"
          class="text-sm text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
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
          <ConfigureStep
            form={form.value}
            namespaces={namespaces.value}
            errors={errors.value}
            onFieldChange={updateField}
            onRuleChange={updateRule}
            onPathChange={updatePath}
            onAddRule={addRule}
            onRemoveRule={removeRule}
            onAddPath={addPath}
            onRemovePath={removePath}
            onToggleTLS={toggleTLS}
          />
        )}

        {currentStep.value === 1 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/networking/ingresses"
          />
        )}
      </div>

      {currentStep.value === 0 && (
        <div class="flex justify-end mt-8">
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
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

// --- Configure Step ---

interface ConfigureStepProps {
  form: IngressFormState;
  namespaces: string[];
  errors: Record<string, string>;
  onFieldChange: (field: string, value: unknown) => void;
  onRuleChange: (ruleIdx: number, field: string, value: unknown) => void;
  onPathChange: (
    ruleIdx: number,
    pathIdx: number,
    field: string,
    value: unknown,
  ) => void;
  onAddRule: () => void;
  onRemoveRule: (ruleIdx: number) => void;
  onAddPath: (ruleIdx: number) => void;
  onRemovePath: (ruleIdx: number, pathIdx: number) => void;
  onToggleTLS: (enabled: boolean) => void;
}

function ConfigureStep({
  form,
  namespaces,
  errors,
  onFieldChange,
  onRuleChange,
  onPathChange,
  onAddRule,
  onRemoveRule,
  onAddPath,
  onRemovePath,
  onToggleTLS,
}: ConfigureStepProps) {
  return (
    <div class="space-y-6">
      {/* Name & Namespace */}
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div>
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
            Name <span class="text-danger">*</span>
          </label>
          <input
            type="text"
            value={form.name}
            onInput={(e) =>
              onFieldChange("name", (e.target as HTMLInputElement).value)}
            placeholder="my-ingress"
            class={WIZARD_INPUT_CLASS}
          />
          {errors.name && <p class="mt-1 text-xs text-danger">{errors.name}</p>}
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
            Namespace <span class="text-danger">*</span>
          </label>
          <select
            value={form.namespace}
            onChange={(e) =>
              onFieldChange("namespace", (e.target as HTMLSelectElement).value)}
            class={WIZARD_INPUT_CLASS}
          >
            {namespaces.map((ns) => <option key={ns} value={ns}>{ns}</option>)}
          </select>
          {errors.namespace && (
            <p class="mt-1 text-xs text-danger">{errors.namespace}</p>
          )}
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
            Ingress Class Name
          </label>
          <input
            type="text"
            value={form.ingressClassName}
            onInput={(e) =>
              onFieldChange(
                "ingressClassName",
                (e.target as HTMLInputElement).value,
              )}
            placeholder="nginx (optional)"
            class={WIZARD_INPUT_CLASS}
          />
        </div>
      </div>

      {/* Rules */}
      <div>
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm font-medium text-slate-700 dark:text-slate-300">
            Rules <span class="text-danger">*</span>
          </h3>
          <Button variant="ghost" onClick={onAddRule}>
            + Add Rule
          </Button>
        </div>
        {errors.rules && <p class="mb-2 text-xs text-danger">{errors.rules}</p>}

        <div class="space-y-4">
          {form.rules.map((rule, ruleIdx) => (
            <div
              key={ruleIdx}
              class="border border-slate-200 dark:border-slate-700 rounded-lg p-4"
            >
              <div class="flex items-center justify-between mb-3">
                <span class="text-sm font-medium text-slate-600 dark:text-slate-400">
                  Rule {ruleIdx + 1}
                </span>
                {form.rules.length > 1 && (
                  <button
                    type="button"
                    onClick={() => onRemoveRule(ruleIdx)}
                    class="text-xs text-danger hover:text-danger/80"
                  >
                    Remove Rule
                  </button>
                )}
              </div>

              <div class="mb-3">
                <label class="block text-xs font-medium text-slate-600 dark:text-slate-400">
                  Host (leave empty for wildcard)
                </label>
                <input
                  type="text"
                  value={rule.host}
                  onInput={(e) =>
                    onRuleChange(
                      ruleIdx,
                      "host",
                      (e.target as HTMLInputElement).value,
                    )}
                  placeholder="example.com"
                  class={WIZARD_INPUT_CLASS}
                />
                {errors[`rules[${ruleIdx}].host`] && (
                  <p class="mt-1 text-xs text-danger">
                    {errors[`rules[${ruleIdx}].host`]}
                  </p>
                )}
              </div>

              {/* Paths sub-table */}
              <div>
                <div class="flex items-center justify-between mb-2">
                  <label class="text-xs font-medium text-slate-600 dark:text-slate-400">
                    Paths
                  </label>
                  <button
                    type="button"
                    onClick={() => onAddPath(ruleIdx)}
                    class="text-xs text-brand hover:text-brand/80"
                  >
                    + Add Path
                  </button>
                </div>
                {errors[`rules[${ruleIdx}].paths`] && (
                  <p class="mb-2 text-xs text-danger">
                    {errors[`rules[${ruleIdx}].paths`]}
                  </p>
                )}

                <div class="space-y-2">
                  {rule.paths.map((p, pathIdx) => (
                    <div
                      key={pathIdx}
                      class="grid grid-cols-12 gap-2 items-end bg-slate-50 dark:bg-slate-800 rounded-md p-2"
                    >
                      <div class="col-span-3">
                        <label class="block text-xs text-slate-500">Path</label>
                        <input
                          type="text"
                          value={p.path}
                          onInput={(e) =>
                            onPathChange(
                              ruleIdx,
                              pathIdx,
                              "path",
                              (e.target as HTMLInputElement).value,
                            )}
                          placeholder="/"
                          class={WIZARD_INPUT_CLASS}
                        />
                        {errors[`rules[${ruleIdx}].paths[${pathIdx}].path`] && (
                          <p class="mt-1 text-xs text-danger">
                            {errors[`rules[${ruleIdx}].paths[${pathIdx}].path`]}
                          </p>
                        )}
                      </div>
                      <div class="col-span-2">
                        <label class="block text-xs text-slate-500">
                          Path Type
                        </label>
                        <select
                          value={p.pathType}
                          onChange={(e) =>
                            onPathChange(
                              ruleIdx,
                              pathIdx,
                              "pathType",
                              (e.target as HTMLSelectElement).value,
                            )}
                          class={WIZARD_INPUT_CLASS}
                        >
                          {PATH_TYPE_OPTIONS.map((opt) => (
                            <option key={opt.value} value={opt.value}>
                              {opt.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div class="col-span-3">
                        <label class="block text-xs text-slate-500">
                          Service Name
                        </label>
                        <input
                          type="text"
                          value={p.serviceName}
                          onInput={(e) =>
                            onPathChange(
                              ruleIdx,
                              pathIdx,
                              "serviceName",
                              (e.target as HTMLInputElement).value,
                            )}
                          placeholder="my-service"
                          class={WIZARD_INPUT_CLASS}
                        />
                        {errors[
                          `rules[${ruleIdx}].paths[${pathIdx}].serviceName`
                        ] && (
                          <p class="mt-1 text-xs text-danger">
                            {errors[
                              `rules[${ruleIdx}].paths[${pathIdx}].serviceName`
                            ]}
                          </p>
                        )}
                      </div>
                      <div class="col-span-2">
                        <label class="block text-xs text-slate-500">
                          Service Port
                        </label>
                        <input
                          type="number"
                          value={p.servicePort}
                          onInput={(e) =>
                            onPathChange(
                              ruleIdx,
                              pathIdx,
                              "servicePort",
                              parseInt(
                                (e.target as HTMLInputElement).value,
                                10,
                              ) || 0,
                            )}
                          min={1}
                          max={65535}
                          class={WIZARD_INPUT_CLASS}
                        />
                        {errors[
                          `rules[${ruleIdx}].paths[${pathIdx}].servicePort`
                        ] && (
                          <p class="mt-1 text-xs text-danger">
                            {errors[
                              `rules[${ruleIdx}].paths[${pathIdx}].servicePort`
                            ]}
                          </p>
                        )}
                      </div>
                      <div class="col-span-2 flex justify-end">
                        {rule.paths.length > 1 && (
                          <button
                            type="button"
                            onClick={() => onRemovePath(ruleIdx, pathIdx)}
                            class="text-xs text-danger hover:text-danger/80 mt-5"
                          >
                            Remove
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* TLS Section */}
      <div class="border border-slate-200 dark:border-slate-700 rounded-lg p-4">
        <div class="flex items-center gap-3 mb-3">
          <input
            type="checkbox"
            id="enableTLS"
            checked={form.enableTLS}
            onChange={(e) =>
              onToggleTLS((e.target as HTMLInputElement).checked)}
            class="rounded border-slate-300 text-brand focus:ring-brand"
          />
          <label
            for="enableTLS"
            class="text-sm font-medium text-slate-700 dark:text-slate-300"
          >
            Enable TLS
          </label>
        </div>

        {form.enableTLS &&
          form.tls.map((tlsEntry, tlsIdx) => (
            <div key={tlsIdx} class="space-y-3 mt-3">
              <div>
                <label class="block text-xs font-medium text-slate-600 dark:text-slate-400">
                  TLS Hosts (auto-filled from rules)
                </label>
                <input
                  type="text"
                  value={tlsEntry.hosts.join(", ")}
                  onInput={(e) => {
                    const val = (e.target as HTMLInputElement).value;
                    const hosts = val
                      .split(",")
                      .map((h) =>
                        h.trim()
                      )
                      .filter((h) => h !== "");
                    const tls = [...form.tls];
                    tls[tlsIdx] = { ...tls[tlsIdx], hosts };
                    onFieldChange("tls", tls);
                  }}
                  placeholder="example.com, api.example.com"
                  class={WIZARD_INPUT_CLASS}
                />
                {errors[`tls[${tlsIdx}].hosts`] && (
                  <p class="mt-1 text-xs text-danger">
                    {errors[`tls[${tlsIdx}].hosts`]}
                  </p>
                )}
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-600 dark:text-slate-400">
                  Secret Name <span class="text-danger">*</span>
                </label>
                <input
                  type="text"
                  value={tlsEntry.secretName}
                  onInput={(e) => {
                    const tls = [...form.tls];
                    tls[tlsIdx] = {
                      ...tls[tlsIdx],
                      secretName: (e.target as HTMLInputElement).value,
                    };
                    onFieldChange("tls", tls);
                  }}
                  placeholder="tls-secret"
                  class={WIZARD_INPUT_CLASS}
                />
                {errors[`tls[${tlsIdx}].secretName`] && (
                  <p class="mt-1 text-xs text-danger">
                    {errors[`tls[${tlsIdx}].secretName`]}
                  </p>
                )}
              </div>
            </div>
          ))}
      </div>
    </div>
  );
}
