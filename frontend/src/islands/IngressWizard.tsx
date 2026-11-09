/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import {
  DNS_LABEL_REGEX,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
  let y = `apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: ${
    f.name || "<name>"
  }\n  namespace: ${f.namespace}`;
  if (f.ingressClassName) {
    y += `\n  annotations:\n    kubernetes.io/ingress.class: "${f.ingressClassName}"`;
  }
  y += `\nspec:\n  rules:\n    - host: ${host}\n      http:\n        paths:\n          - path: ${path}\n            pathType: ${
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
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<IngressFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const formRef = useLatest(form);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);
  useDirtyGuard(dirty);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  const updateField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, [field]: value }));
  }, []);

  const updateRule = useCallback(
    (ruleIdx: number, field: string, value: unknown) => {
      setDirty(true);
      setForm((prev) => {
        const rules = [...prev.rules];
        rules[ruleIdx] = { ...rules[ruleIdx], [field]: value };
        return { ...prev, rules };
      });
    },
    [],
  );

  const updatePath = useCallback(
    (ruleIdx: number, pathIdx: number, field: string, value: unknown) => {
      setDirty(true);
      setForm((prev) => {
        const rules = [...prev.rules];
        const paths = [...rules[ruleIdx].paths];
        paths[pathIdx] = { ...paths[pathIdx], [field]: value };
        rules[ruleIdx] = { ...rules[ruleIdx], paths };
        return { ...prev, rules };
      });
    },
    [],
  );

  const addRule = useCallback(() => {
    setDirty(true);
    setForm((prev) => ({ ...prev, rules: [...prev.rules, newRule()] }));
  }, []);

  const removeRule = useCallback((ruleIdx: number) => {
    setDirty(true);
    setForm((prev) => {
      const rules = prev.rules.filter((_, i) => i !== ruleIdx);
      return { ...prev, rules: rules.length > 0 ? rules : [newRule()] };
    });
  }, []);

  const addPath = useCallback((ruleIdx: number) => {
    setDirty(true);
    setForm((prev) => {
      const rules = [...prev.rules];
      rules[ruleIdx] = {
        ...rules[ruleIdx],
        paths: [...rules[ruleIdx].paths, newPath()],
      };
      return { ...prev, rules };
    });
  }, []);

  const removePath = useCallback((ruleIdx: number, pathIdx: number) => {
    setDirty(true);
    setForm((prev) => {
      const rules = [...prev.rules];
      const paths = rules[ruleIdx].paths.filter((_, i) => i !== pathIdx);
      rules[ruleIdx] = {
        ...rules[ruleIdx],
        paths: paths.length > 0 ? paths : [newPath()],
      };
      return { ...prev, rules };
    });
  }, []);

  const validateStep = (step: number): boolean => {
    const f = formRef.current;
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = formRef.current;

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

    if (currentStep === 0) {
      setCurrentStep(1);
      await fetchPreview();
    } else {
      setCurrentStep((prev) => prev + 1);
    }
  };

  const goBack = () => {
    if (currentStep > 0) {
      setCurrentStep((prev) => prev - 1);
    }
  };

  // Sync TLS hosts from rules when TLS is toggled on
  const toggleTLS = useCallback((enabled: boolean) => {
    setDirty(true);
    setForm((prev) => {
      if (!enabled) return { ...prev, enableTLS: false, tls: [] };
      const hosts = prev.rules.map((r) => r.host).filter((h) => h !== "");
      return {
        ...prev,
        enableTLS: true,
        tls: [{ hosts: [...new Set(hosts)], secretName: "" }],
      };
    });
  }, []);

  const f = form;

  return (
    <WizardShell
      title="Create Ingress"
      subtitle={`Step ${currentStep + 1} of 2 · namespace ${f.namespace}`}
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
          <path d="M3 10h14M10 3l7 7-7 7" />
        </svg>
      }
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={goNext}
      nextLabel={currentStep === 0 ? "Continue" : "Close"}
      yaml={currentStep === 0 ? buildManifest(f) : undefined}
    >
      {currentStep === 0 && (
        <ConfigureStep
          form={f}
          namespaces={namespaceList}
          errors={errors}
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

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/networking/ingresses"
          onDirtyChange={setDirty}
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
  const ruleKeys = useRowKeys(form.rules.length);
  const tlsKeys = useRowKeys(form.tls.length);
  // The hosts field is parsed into a list, so keep what was typed (a trailing
  // comma, a space) until the entry is rebuilt by toggling TLS.
  const [tlsHostsText, setTlsHostsText] = useState<string | null>(null);
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
            onChange={(e) => onFieldChange("name", e.currentTarget.value)}
            placeholder="my-ingress"
            className={WIZARD_INPUT_CLASS}
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
            onChange={(e) => onFieldChange("namespace", e.currentTarget.value)}
            className={WIZARD_INPUT_CLASS}
          >
            {namespaces.map((ns) => (
              <option key={ns} value={ns}>
                {ns}
              </option>
            ))}
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
            onChange={(e) =>
              onFieldChange("ingressClassName", e.currentTarget.value)
            }
            placeholder="nginx (optional)"
            className={WIZARD_INPUT_CLASS}
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
            <RuleCard
              key={ruleKeys.keyAt(ruleIdx)}
              rule={rule}
              ruleIdx={ruleIdx}
              canRemove={form.rules.length > 1}
              errors={errors}
              onRuleChange={onRuleChange}
              onPathChange={onPathChange}
              onAddPath={onAddPath}
              onRemovePath={onRemovePath}
              onRemove={() => {
                ruleKeys.removeAt(ruleIdx);
                onRemoveRule(ruleIdx);
              }}
            />
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
            onChange={(e) => {
              setTlsHostsText(null);
              onToggleTLS(e.currentTarget.checked);
            }}
            style={{ width: "15px", height: "15px" }}
          />
          <label
            htmlFor="enableTLS"
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
              key={tlsKeys.keyAt(tlsIdx)}
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
                  value={tlsHostsText ?? tlsEntry.hosts.join(",")}
                  onChange={(e) => {
                    const val = e.currentTarget.value;
                    setTlsHostsText(val);
                    const hosts = val
                      .split(",")
                      .map((h) => h.trim())
                      .filter((h) => h !== "");
                    const tls = [...form.tls];
                    tls[tlsIdx] = { ...tls[tlsIdx], hosts };
                    onFieldChange("tls", tls);
                  }}
                  placeholder="example.com, api.example.com"
                  className={WIZARD_INPUT_CLASS}
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
                  onChange={(e) => {
                    const tls = [...form.tls];
                    tls[tlsIdx] = {
                      ...tls[tlsIdx],
                      secretName: e.currentTarget.value,
                    };
                    onFieldChange("tls", tls);
                  }}
                  placeholder="tls-secret"
                  className={WIZARD_INPUT_CLASS}
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

interface RuleCardProps {
  rule: IngressRuleState;
  ruleIdx: number;
  canRemove: boolean;
  errors: Record<string, string>;
  onRuleChange: (ruleIdx: number, field: string, value: unknown) => void;
  onPathChange: (
    ruleIdx: number,
    pathIdx: number,
    field: string,
    value: unknown,
  ) => void;
  onAddPath: (ruleIdx: number) => void;
  onRemovePath: (ruleIdx: number, pathIdx: number) => void;
  onRemove: () => void;
}

function RuleCard({
  rule,
  ruleIdx,
  canRemove,
  errors,
  onRuleChange,
  onPathChange,
  onAddPath,
  onRemovePath,
  onRemove,
}: RuleCardProps) {
  const pathKeys = useRowKeys(rule.paths.length);
  return (
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
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
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
          onChange={(e) => onRuleChange(ruleIdx, "host", e.currentTarget.value)}
          placeholder="example.com"
          className={WIZARD_INPUT_CLASS}
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
              key={pathKeys.keyAt(pathIdx)}
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
                  onChange={(e) =>
                    onPathChange(
                      ruleIdx,
                      pathIdx,
                      "path",
                      e.currentTarget.value,
                    )
                  }
                  placeholder="/"
                  className={WIZARD_INPUT_CLASS}
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
                      e.currentTarget.value,
                    )
                  }
                  className={WIZARD_INPUT_CLASS}
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
                  onChange={(e) =>
                    onPathChange(
                      ruleIdx,
                      pathIdx,
                      "serviceName",
                      e.currentTarget.value,
                    )
                  }
                  placeholder="my-service"
                  className={WIZARD_INPUT_CLASS}
                />
                {errors[`rules[${ruleIdx}].paths[${pathIdx}].serviceName`] && (
                  <p
                    style={{
                      fontSize: "10px",
                      color: "var(--error)",
                      marginTop: "2px",
                    }}
                  >
                    {errors[`rules[${ruleIdx}].paths[${pathIdx}].serviceName`]}
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
                  onChange={(e) =>
                    onPathChange(
                      ruleIdx,
                      pathIdx,
                      "servicePort",
                      parseInt(e.currentTarget.value, 10) || 0,
                    )
                  }
                  min={1}
                  max={65535}
                  className={WIZARD_INPUT_CLASS}
                />
                {errors[`rules[${ruleIdx}].paths[${pathIdx}].servicePort`] && (
                  <p
                    style={{
                      fontSize: "10px",
                      color: "var(--error)",
                      marginTop: "2px",
                    }}
                  >
                    {errors[`rules[${ruleIdx}].paths[${pathIdx}].servicePort`]}
                  </p>
                )}
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                {rule.paths.length > 1 && (
                  <button
                    type="button"
                    onClick={() => {
                      pathKeys.removeAt(pathIdx);
                      onRemovePath(ruleIdx, pathIdx);
                    }}
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
  );
}
