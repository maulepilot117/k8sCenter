/** @jsxImportSource react */
import { useCallback } from "react";
import type { ParamField } from "@/lib/policy-templates.ts";
import { getTemplate } from "@/lib/policy-templates.ts";
import type { EngineStatus } from "@/lib/policy-types.ts";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { PolicyWizardForm } from "@/src/islands/PolicyWizard.tsx";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

function CloseIcon() {
  return (
    <svg
      className="w-4 h-4"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="2"
        d="M6 18L18 6M6 6l12 12"
      />
    </svg>
  );
}

interface PolicyConfigStepProps {
  form: PolicyWizardForm;
  errors: Record<string, string>;
  engineStatus: EngineStatus | null;
  onUpdate: (field: string, value: unknown) => void;
  onUpdateParam: (key: string, value: unknown) => void;
}

const TARGET_KIND_OPTIONS = ["Pod", "Deployment", "StatefulSet", "DaemonSet"];

const KYVERNO_ACTIONS = [
  { value: "Enforce", label: "Enforce" },
  { value: "Audit", label: "Audit" },
];

const GATEKEEPER_ACTIONS = [
  { value: "deny", label: "Deny" },
  { value: "dryrun", label: "Dry Run" },
  { value: "warn", label: "Warn" },
];

export function PolicyConfigStep({
  form,
  errors,
  engineStatus,
  onUpdate,
  onUpdateParam,
}: PolicyConfigStepProps) {
  const template = getTemplate(form.templateId);

  const kyvernoAvailable =
    engineStatus?.detected === "kyverno" || engineStatus?.detected === "both";
  const gatekeeperAvailable =
    engineStatus?.detected === "gatekeeper" ||
    engineStatus?.detected === "both";
  const singleEngine =
    engineStatus?.detected === "kyverno" ||
    engineStatus?.detected === "gatekeeper";

  const actionOptions =
    form.engine === "gatekeeper" ? GATEKEEPER_ACTIONS : KYVERNO_ACTIONS;

  const toggleTargetKind = useCallback(
    (kind: string) => {
      const current = form.targetKinds;
      if (current.includes(kind)) {
        onUpdate(
          "targetKinds",
          current.filter((k) => k !== kind),
        );
      } else {
        onUpdate("targetKinds", [...current, kind]);
      }
    },
    [form.targetKinds, onUpdate],
  );

  const addExcludedNamespace = useCallback(() => {
    onUpdate("excludedNamespaces", [...form.excludedNamespaces, ""]);
  }, [form.excludedNamespaces, onUpdate]);

  const nsRows = useRowKeys(form.excludedNamespaces.length);

  const removeExcludedNamespace = useCallback(
    (index: number) => {
      nsRows.removeAt(index);
      onUpdate(
        "excludedNamespaces",
        form.excludedNamespaces.filter((_, i) => i !== index),
      );
    },
    [form.excludedNamespaces, onUpdate, nsRows],
  );

  const updateExcludedNamespace = useCallback(
    (index: number, value: string) => {
      const updated = [...form.excludedNamespaces];
      updated[index] = value;
      onUpdate("excludedNamespaces", updated);
    },
    [form.excludedNamespaces, onUpdate],
  );

  return (
    <div className="space-y-6 max-w-lg">
      {/* Engine Selector */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-2">
          Policy Engine
        </label>
        {singleEngine ? (
          <p className="text-sm text-text-secondary">
            Auto-selected:{" "}
            <span className="font-medium text-text-primary capitalize">
              {engineStatus?.detected}
            </span>
          </p>
        ) : (
          <div className="flex gap-4">
            {kyvernoAvailable && (
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="engine"
                  value="kyverno"
                  checked={form.engine === "kyverno"}
                  onChange={() => onUpdate("engine", "kyverno")}
                  className="accent-brand"
                />
                <span className="text-sm text-text-primary">Kyverno</span>
              </label>
            )}
            {gatekeeperAvailable && (
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="engine"
                  value="gatekeeper"
                  checked={form.engine === "gatekeeper"}
                  onChange={() => onUpdate("engine", "gatekeeper")}
                  className="accent-brand"
                />
                <span className="text-sm text-text-primary">Gatekeeper</span>
              </label>
            )}
          </div>
        )}
        {errors.engine && (
          <p className="text-sm text-danger mt-1">{errors.engine}</p>
        )}
      </div>

      {/* Name */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-1">
          Policy Name
        </label>
        <input
          type="text"
          className={WIZARD_INPUT_CLASS}
          value={form.name}
          onChange={(e) =>
            onUpdate("name", (e.currentTarget as HTMLInputElement).value)
          }
          placeholder="disallow-privileged"
        />
        {errors.name && (
          <p className="text-sm text-danger mt-1">{errors.name}</p>
        )}
      </div>

      {/* Action */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-1">
          Action
        </label>
        <select
          className={WIZARD_INPUT_CLASS}
          value={form.action}
          onChange={(e) =>
            onUpdate("action", (e.currentTarget as HTMLSelectElement).value)
          }
        >
          <option value="">Select action...</option>
          {actionOptions.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
        {errors.action && (
          <p className="text-sm text-danger mt-1">{errors.action}</p>
        )}
      </div>

      {/* Target Kinds */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-2">
          Target Kinds
        </label>
        <div className="flex flex-wrap gap-2">
          {TARGET_KIND_OPTIONS.map((kind) => {
            const selected = form.targetKinds.includes(kind);
            return (
              <button
                key={kind}
                type="button"
                className={`px-3 py-1.5 rounded-md text-sm border transition-colors ${
                  selected
                    ? "border-brand bg-brand/10 text-brand"
                    : "border-border-primary bg-surface text-text-secondary hover:border-brand/50"
                }`}
                onClick={() => toggleTargetKind(kind)}
              >
                {kind}
              </button>
            );
          })}
        </div>
        {errors.targetKinds && (
          <p className="text-sm text-danger mt-1">{errors.targetKinds}</p>
        )}
      </div>

      {/* Excluded Namespaces */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-2">
          Excluded Namespaces
        </label>
        <div className="space-y-2">
          {form.excludedNamespaces.map((ns, i) => (
            <div key={nsRows.keyAt(i)} className="flex items-center gap-2">
              <input
                type="text"
                className={WIZARD_INPUT_CLASS}
                value={ns}
                onChange={(e) =>
                  updateExcludedNamespace(
                    i,
                    (e.currentTarget as HTMLInputElement).value,
                  )
                }
                placeholder="namespace"
              />
              <button
                type="button"
                className="text-text-muted hover:text-danger shrink-0"
                onClick={() => removeExcludedNamespace(i)}
              >
                <CloseIcon />
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          className="mt-2 text-sm text-brand hover:text-brand/80"
          onClick={addExcludedNamespace}
        >
          + Add namespace
        </button>
      </div>

      {/* Description */}
      <div>
        <label className="block text-sm font-medium text-text-primary mb-1">
          Description
        </label>
        <textarea
          className={WIZARD_INPUT_CLASS}
          rows={3}
          value={form.description}
          onChange={(e) =>
            onUpdate(
              "description",
              (e.currentTarget as HTMLTextAreaElement).value,
            )
          }
          placeholder="Policy description..."
        />
      </div>

      {/* Template-specific Parameters */}
      {template && template.paramFields.length > 0 && (
        <div className="border-t border-border-primary pt-6">
          <h3 className="text-sm font-semibold text-text-primary mb-4">
            Template Parameters
          </h3>
          <div className="space-y-4">
            {template.paramFields.map((field: ParamField) => (
              <TemplateParamField
                key={field.key}
                field={field}
                value={form.params[field.key]}
                error={errors[`param.${field.key}`]}
                onChange={(v) => onUpdateParam(field.key, v)}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function TemplateParamField({
  field,
  value,
  error,
  onChange,
}: {
  field: ParamField;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  if (field.type === "boolean") {
    return (
      <div>
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) =>
              onChange((e.currentTarget as HTMLInputElement).checked)
            }
            className="accent-brand"
          />
          <span className="text-sm text-text-primary">{field.label}</span>
        </label>
        <p className="text-xs text-text-muted mt-1 ml-6">{field.description}</p>
      </div>
    );
  }

  if (field.type === "stringList") {
    return (
      <StringListParamField
        field={field}
        value={value}
        error={error}
        onChange={onChange}
      />
    );
  }

  // string type
  return (
    <div>
      <label className="block text-sm font-medium text-text-primary mb-1">
        {field.label}
        {field.required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <p className="text-xs text-text-muted mb-1">{field.description}</p>
      <input
        type="text"
        className={WIZARD_INPUT_CLASS}
        value={(value as string) ?? ""}
        onChange={(e) => onChange((e.currentTarget as HTMLInputElement).value)}
      />
      {error && <p className="text-sm text-danger mt-1">{error}</p>}
    </div>
  );
}

function StringListParamField({
  field,
  value,
  error,
  onChange,
}: {
  field: ParamField;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  const items = Array.isArray(value) ? (value as string[]) : [];
  const rows = useRowKeys(items.length);
  return (
    <div>
      <label className="block text-sm font-medium text-text-primary mb-1">
        {field.label}
        {field.required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <p className="text-xs text-text-muted mb-2">{field.description}</p>
      <div className="space-y-2">
        {items.map((item, i) => (
          <div key={rows.keyAt(i)} className="flex items-center gap-2">
            <input
              type="text"
              className={WIZARD_INPUT_CLASS}
              value={item}
              onChange={(e) => {
                const updated = [...items];
                updated[i] = (e.currentTarget as HTMLInputElement).value;
                onChange(updated);
              }}
              placeholder={`Value ${i + 1}`}
            />
            <button
              type="button"
              className="text-text-muted hover:text-danger shrink-0"
              onClick={() => {
                rows.removeAt(i);
                onChange(items.filter((_, idx) => idx !== i));
              }}
            >
              <CloseIcon />
            </button>
          </div>
        ))}
      </div>
      <button
        type="button"
        className="mt-2 text-sm text-brand hover:text-brand/80"
        onClick={() => onChange([...items, ""])}
      >
        + Add value
      </button>
      {error && <p className="text-sm text-danger mt-1">{error}</p>}
    </div>
  );
}
