import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Rules & TLS" },
  { label: "Review", sub: "Preview & apply" },
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
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    ingressClassName: "",
    rules: [newRule()],
    enableTLS: false,
    tls: [],
  };
}

function buildManifest(f: IngressFormState): string {
  const firstRule = f.rules[0];
  const firstPath = firstRule?.paths[0];
  const host = firstRule?.host || "<host>";
  const path = firstPath?.path || "/";
  const svc = firstPath?.serviceName || "<service>";
  const port = firstPath?.servicePort ?? 80;
  let y =
    `apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: ${
      f.name || "<name>"
    }\n  namespace: ${f.namespace}`;
  if (f.ingressClassName) {
    y +=
      `\n  annotations:\n    kubernetes.io/ingress.class: "${f.ingressClassName}"`;
  }
  y +=
    `\nspec:\n  rules:\n    - host: ${host}\n      http:\n        paths:\n          - path: ${path}\n            pathType: ${
      firstPath?.pathType ?? "Prefix"
    }\n            backend:\n              service:\n                name: ${svc}\n                port:\n                  number: ${port}`;
  if (f.enableTLS && f.tls.length > 0) {
    y += `\n  tls:\n    - hosts:\n        - ${
      f.tls[0].hosts[0] ?? host
    }\n      secretName: ${f.tls[0].secretName || "<tls-secret>"}`;
  }
  return y;
}

export default function IngressWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
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

  const f = form.value;

  return (
    <WizardShell
      title="Create Ingress"
      subtitle={`Step ${currentStep.value + 1} of 2 · namespace ${f.namespace}`}
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
          <path d="M3 10h14M10 3l7 7-7 7" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={goNext}
      nextLabel={currentStep.value === 0 ? "Continue" : "Close"}
      yaml={currentStep.value === 0 ? buildManifest(f) : undefined}
    >
      {currentStep.value === 0 && (
        <ConfigureStep
          form={f}
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
    </WizardShell>
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
    <div style={{ display: "flex", flexDirection: "column", gap: "20px" }}>
      {/* Name & Namespace */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr 1fr",
          gap: "12px",
        }}
      >
        <div>
          <label
            style={{
              display: "block",
              fontSize: "12.5px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              marginBottom: "5px",
            }}
          >
            Name <span style={{ color: "var(--error)" }}>*</span>
          </label>
          <input
            type="text"
            value={form.name}
            onInput={(e) =>
              onFieldChange("name", (e.target as HTMLInputElement).value)}
            placeholder="my-ingress"
            class={WIZARD_INPUT_CLASS}
          />
          {errors.name && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.name}
            </p>
          )}
        </div>
        <div>
          <label
            style={{
              display: "block",
              fontSize: "12.5px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              marginBottom: "5px",
            }}
          >
            Namespace <span style={{ color: "var(--error)" }}>*</span>
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
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.namespace}
            </p>
          )}
        </div>
        <div>
          <label
            style={{
              display: "block",
              fontSize: "12.5px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              marginBottom: "5px",
            }}
          >
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
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            marginBottom: "10px",
          }}
        >
          <span
            style={{
              fontSize: "12.5px",
              fontWeight: 600,
              color: "var(--text-secondary)",
            }}
          >
            Rules <span style={{ color: "var(--error)" }}>*</span>
          </span>
          <button
            type="button"
            onClick={onAddRule}
            style={{
              fontSize: "12px",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
            }}
          >
            + Add Rule
          </button>
        </div>
        {errors.rules && (
          <p
            style={{
              marginBottom: "8px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.rules}
          </p>
        )}

        <div style={{ display: "flex", flexDirection: "column", gap: "12px" }}>
          {form.rules.map((rule, ruleIdx) => (
            <div
              key={ruleIdx}
              style={{
                border: "1px solid var(--border-subtle)",
                borderRadius: "10px",
                padding: "14px",
              }}
            >
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  marginBottom: "10px",
                }}
              >
                <span
                  style={{
                    fontSize: "12.5px",
                    fontWeight: 600,
                    color: "var(--text-secondary)",
                  }}
                >
                  Rule {ruleIdx + 1}
                </span>
                {form.rules.length > 1 && (
                  <button
                    type="button"
                    onClick={() => onRemoveRule(ruleIdx)}
                    style={{
                      fontSize: "11px",
                      color: "var(--error)",
                      background: "transparent",
                      border: "none",
                      cursor: "pointer",
                      padding: 0,
                    }}
                  >
                    Remove Rule
                  </button>
                )}
              </div>

              <div style={{ marginBottom: "10px" }}>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                    marginBottom: "4px",
                  }}
                >
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
              </div>

              {/* Paths sub-table */}
              <div>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    marginBottom: "6px",
                  }}
                >
                  <label
                    style={{
                      fontSize: "11px",
                      fontWeight: 600,
                      color: "var(--text-muted)",
                    }}
                  >
                    Paths
                  </label>
                  <button
                    type="button"
                    onClick={() => onAddPath(ruleIdx)}
                    style={{
                      fontSize: "11px",
                      color: "var(--accent)",
                      background: "transparent",
                      border: "none",
                      cursor: "pointer",
                      padding: 0,
                    }}
                  >
                    + Add Path
                  </button>
                </div>

                <div
                  style={{
                    display: "flex",
                    flexDirection: "column",
                    gap: "6px",
                  }}
                >
                  {rule.paths.map((p, pathIdx) => (
                    <div
                      key={pathIdx}
                      style={{
                        display: "grid",
                        gridTemplateColumns: "2fr 2fr 3fr 2fr auto",
                        gap: "6px",
                        alignItems: "end",
                        background: "var(--bg-elevated)",
                        borderRadius: "7px",
                        padding: "8px",
                      }}
                    >
                      <div>
                        <label
                          style={{
                            display: "block",
                            fontSize: "10px",
                            color: "var(--text-muted)",
                            marginBottom: "3px",
                          }}
                        >
                          Path
                        </label>
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
                          <p
                            style={{
                              fontSize: "10px",
                              color: "var(--error)",
                              marginTop: "2px",
                            }}
                          >
                            {errors[`rules[${ruleIdx}].paths[${pathIdx}].path`]}
                          </p>
                        )}
                      </div>
                      <div>
                        <label
                          style={{
                            display: "block",
                            fontSize: "10px",
                            color: "var(--text-muted)",
                            marginBottom: "3px",
                          }}
                        >
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
                      <div>
                        <label
                          style={{
                            display: "block",
                            fontSize: "10px",
                            color: "var(--text-muted)",
                            marginBottom: "3px",
                          }}
                        >
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
                          <p
                            style={{
                              fontSize: "10px",
                              color: "var(--error)",
                              marginTop: "2px",
                            }}
                          >
                            {errors[
                              `rules[${ruleIdx}].paths[${pathIdx}].serviceName`
                            ]}
                          </p>
                        )}
                      </div>
                      <div>
                        <label
                          style={{
                            display: "block",
                            fontSize: "10px",
                            color: "var(--text-muted)",
                            marginBottom: "3px",
                          }}
                        >
                          Port
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
                          <p
                            style={{
                              fontSize: "10px",
                              color: "var(--error)",
                              marginTop: "2px",
                            }}
                          >
                            {errors[
                              `rules[${ruleIdx}].paths[${pathIdx}].servicePort`
                            ]}
                          </p>
                        )}
                      </div>
                      <div
                        style={{ display: "flex", justifyContent: "flex-end" }}
                      >
                        {rule.paths.length > 1 && (
                          <button
                            type="button"
                            onClick={() => onRemovePath(ruleIdx, pathIdx)}
                            style={{
                              fontSize: "11px",
                              color: "var(--error)",
                              background: "transparent",
                              border: "none",
                              cursor: "pointer",
                              padding: 0,
                            }}
                          >
                            ×
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
      <div
        style={{
          border: "1px solid var(--border-subtle)",
          borderRadius: "10px",
          padding: "14px",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "10px",
            marginBottom: "10px",
          }}
        >
          <input
            type="checkbox"
            id="enableTLS"
            checked={form.enableTLS}
            onChange={(e) =>
              onToggleTLS((e.target as HTMLInputElement).checked)}
            style={{ width: "15px", height: "15px" }}
          />
          <label
            for="enableTLS"
            style={{
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              cursor: "pointer",
            }}
          >
            Enable TLS
          </label>
        </div>

        {form.enableTLS &&
          form.tls.map((tlsEntry, tlsIdx) => (
            <div
              key={tlsIdx}
              style={{ display: "flex", flexDirection: "column", gap: "10px" }}
            >
              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    fontWeight: 600,
                    color: "var(--text-secondary)",
                    marginBottom: "4px",
                  }}
                >
                  TLS Hosts (auto-filled from rules)
                </label>
                <input
                  type="text"
                  value={tlsEntry.hosts.join(",")}
                  onInput={(e) => {
                    const val = (e.target as HTMLInputElement).value;
                    const hosts = val
                      .split(",")
                      .map((h) => h.trim())
                      .filter((h) => h !== "");
                    const tls = [...form.tls];
                    tls[tlsIdx] = { ...tls[tlsIdx], hosts };
                    onFieldChange("tls", tls);
                  }}
                  placeholder="example.com, api.example.com"
                  class={WIZARD_INPUT_CLASS}
                />
                {errors[`tls[${tlsIdx}].hosts`] && (
                  <p
                    style={{
                      marginTop: "4px",
                      fontSize: "11px",
                      color: "var(--error)",
                    }}
                  >
                    {errors[`tls[${tlsIdx}].hosts`]}
                  </p>
                )}
              </div>
              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    fontWeight: 600,
                    color: "var(--text-secondary)",
                    marginBottom: "4px",
                  }}
                >
                  Secret Name <span style={{ color: "var(--error)" }}>*</span>
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
                  <p
                    style={{
                      marginTop: "4px",
                      fontSize: "11px",
                      color: "var(--error)",
                    }}
                  >
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
