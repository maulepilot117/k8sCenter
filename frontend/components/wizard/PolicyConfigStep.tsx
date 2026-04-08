import { useCallback } from "preact/hooks";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { getTemplate } from "@/lib/policy-templates.ts";
import type { ParamField } from "@/lib/policy-templates.ts";
import type { EngineStatus } from "@/lib/policy-types.ts";
import type { PolicyWizardForm } from "@/islands/PolicyWizard.tsx";

function CloseIcon() {
  return (
    <svg
      class="w-4 h-4"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
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

const TARGET_KIND_OPTIONS = [
  "Pod",
  "Deployment",
  "StatefulSet",
  "DaemonSet",
];

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

  const kyvernoAvailable = engineStatus?.detected === "kyverno" ||
    engineStatus?.detected === "both";
  const gatekeeperAvailable = engineStatus?.detected === "gatekeeper" ||
    engineStatus?.detected === "both";
  const singleEngine = engineStatus?.detected === "kyverno" ||
    engineStatus?.detected === "gatekeeper";

  const actionOptions = form.engine === "gatekeeper"
    ? GATEKEEPER_ACTIONS
    : KYVERNO_ACTIONS;

  const toggleTargetKind = useCallback((kind: string) => {
    const current = form.targetKinds;
    if (current.includes(kind)) {
      onUpdate("targetKinds", current.filter((k) => k !== kind));
    } else {
      onUpdate("targetKinds", [...current, kind]);
    }
  }, [form.targetKinds, onUpdate]);

  const addExcludedNamespace = useCallback(() => {
    onUpdate("excludedNamespaces", [...form.excludedNamespaces, ""]);
  }, [form.excludedNamespaces, onUpdate]);

  const removeExcludedNamespace = useCallback((index: number) => {
    onUpdate(
      "excludedNamespaces",
      form.excludedNamespaces.filter((_, i) => i !== index),
    );
  }, [form.excludedNamespaces, onUpdate]);

  const updateExcludedNamespace = useCallback(
    (index: number, value: string) => {
      const updated = [...form.excludedNamespaces];
      updated[index] = value;
      onUpdate("excludedNamespaces", updated);
    },
    [form.excludedNamespaces, onUpdate],
  );

  return (
    <div class="space-y-6 max-w-lg">
      {/* Engine Selector */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-2">
          Policy Engine
        </label>
        {singleEngine
          ? (
            <p class="text-sm text-text-secondary">
              Auto-selected:{" "}
              <span class="font-medium text-text-primary capitalize">
                {engineStatus?.detected}
              </span>
            </p>
          )
          : (
            <div class="flex gap-4">
              {kyvernoAvailable && (
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="radio"
                    name="engine"
                    value="kyverno"
                    checked={form.engine === "kyverno"}
                    onChange={() => onUpdate("engine", "kyverno")}
                    class="accent-brand"
                  />
                  <span class="text-sm text-text-primary">Kyverno</span>
                </label>
              )}
              {gatekeeperAvailable && (
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="radio"
                    name="engine"
                    value="gatekeeper"
                    checked={form.engine === "gatekeeper"}
                    onChange={() => onUpdate("engine", "gatekeeper")}
                    class="accent-brand"
                  />
                  <span class="text-sm text-text-primary">Gatekeeper</span>
                </label>
              )}
            </div>
          )}
        {errors.engine && (
          <p class="text-sm text-danger mt-1">{errors.engine}</p>
        )}
      </div>

      {/* Name */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-1">
          Policy Name
        </label>
        <input
          type="text"
          class={WIZARD_INPUT_CLASS}
          value={form.name}
          onInput={(e) =>
            onUpdate("name", (e.target as HTMLInputElement).value)}
          placeholder="disallow-privileged"
        />
        {errors.name && <p class="text-sm text-danger mt-1">{errors.name}</p>}
      </div>

      {/* Action */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-1">
          Action
        </label>
        <select
          class={WIZARD_INPUT_CLASS}
          value={form.action}
          onChange={(e) =>
            onUpdate("action", (e.target as HTMLSelectElement).value)}
        >
          <option value="">Select action...</option>
          {actionOptions.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
        {errors.action && (
          <p class="text-sm text-danger mt-1">{errors.action}</p>
        )}
      </div>

      {/* Target Kinds */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-2">
          Target Kinds
        </label>
        <div class="flex flex-wrap gap-2">
          {TARGET_KIND_OPTIONS.map((kind) => {
            const selected = form.targetKinds.includes(kind);
            return (
              <button
                key={kind}
                type="button"
                class={`px-3 py-1.5 rounded-md text-sm border transition-colors ${
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
          <p class="text-sm text-danger mt-1">{errors.targetKinds}</p>
        )}
      </div>

      {/* Excluded Namespaces */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-2">
          Excluded Namespaces
        </label>
        <div class="space-y-2">
          {form.excludedNamespaces.map((ns, i) => (
            <div key={i} class="flex items-center gap-2">
              <input
                type="text"
                class={WIZARD_INPUT_CLASS}
                value={ns}
                onInput={(e) =>
                  updateExcludedNamespace(
                    i,
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="namespace"
              />
              <button
                type="button"
                class="text-text-muted hover:text-danger shrink-0"
                onClick={() =>
                  removeExcludedNamespace(i)}
              >
                <CloseIcon />
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          class="mt-2 text-sm text-brand hover:text-brand/80"
          onClick={addExcludedNamespace}
        >
          + Add namespace
        </button>
      </div>

      {/* Description */}
      <div>
        <label class="block text-sm font-medium text-text-primary mb-1">
          Description
        </label>
        <textarea
          class={WIZARD_INPUT_CLASS}
          rows={3}
          value={form.description}
          onInput={(e) =>
            onUpdate("description", (e.target as HTMLTextAreaElement).value)}
          placeholder="Policy description..."
        />
      </div>

      {/* Template-specific Parameters */}
      {template && template.paramFields.length > 0 && (
        <div class="border-t border-border-primary pt-6">
          <h3 class="text-sm font-semibold text-text-primary mb-4">
            Template Parameters
          </h3>
          <div class="space-y-4">
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
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
            class="accent-brand"
          />
          <span class="text-sm text-text-primary">{field.label}</span>
        </label>
        <p class="text-xs text-text-muted mt-1 ml-6">{field.description}</p>
      </div>
    );
  }

  if (field.type === "stringList") {
    const items = Array.isArray(value) ? (value as string[]) : [];
    return (
      <div>
        <label class="block text-sm font-medium text-text-primary mb-1">
          {field.label}
          {field.required && <span class="text-danger ml-0.5">*</span>}
        </label>
        <p class="text-xs text-text-muted mb-2">{field.description}</p>
        <div class="space-y-2">
          {items.map((item, i) => (
            <div key={i} class="flex items-center gap-2">
              <input
                type="text"
                class={WIZARD_INPUT_CLASS}
                value={item}
                onInput={(e) => {
                  const updated = [...items];
                  updated[i] = (e.target as HTMLInputElement).value;
                  onChange(updated);
                }}
                placeholder={`Value ${i + 1}`}
              />
              <button
                type="button"
                class="text-text-muted hover:text-danger shrink-0"
                onClick={() => onChange(items.filter((_, idx) => idx !== i))}
              >
                <CloseIcon />
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          class="mt-2 text-sm text-brand hover:text-brand/80"
          onClick={() => onChange([...items, ""])}
        >
          + Add value
        </button>
        {error && <p class="text-sm text-danger mt-1">{error}</p>}
      </div>
    );
  }

  // string type
  return (
    <div>
      <label class="block text-sm font-medium text-text-primary mb-1">
        {field.label}
        {field.required && <span class="text-danger ml-0.5">*</span>}
      </label>
      <p class="text-xs text-text-muted mb-1">{field.description}</p>
      <input
        type="text"
        class={WIZARD_INPUT_CLASS}
        value={(value as string) ?? ""}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
      />
      {error && <p class="text-sm text-danger mt-1">{error}</p>}
    </div>
  );
}
