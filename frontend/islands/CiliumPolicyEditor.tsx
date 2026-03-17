import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Button } from "@/components/ui/Button.tsx";

interface LabelPair {
  key: string;
  value: string;
}

interface RuleRow {
  id: number;
  direction: "Ingress" | "Egress";
  peerType: "endpoints" | "entities" | "cidr";
  labels: LabelPair[];
  entities: string[];
  cidrs: string;
  ports: string;
  protocol: "TCP" | "UDP" | "SCTP" | "ANY";
  action: "allow" | "deny";
}

interface PolicyWarning {
  code: string;
  message: string;
}

const VALID_ENTITIES = [
  "world",
  "cluster",
  "host",
  "remote-node",
  "kube-apiserver",
  "health",
  "init",
  "ingress",
  "all",
];

let nextRuleId = 1;

function newRule(): RuleRow {
  return {
    id: nextRuleId++,
    direction: "Ingress",
    peerType: "endpoints",
    labels: [{ key: "", value: "" }],
    entities: [],
    cidrs: "",
    ports: "",
    protocol: "TCP",
    action: "allow",
  };
}

export default function CiliumPolicyEditor() {
  const name = useSignal("");
  const initNs = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  const namespace = useSignal(initNs);
  const namespaces = useSignal<string[]>(["default"]);
  const endpointSelector = useSignal<LabelPair[]>([{ key: "", value: "" }]);
  const rules = useSignal<RuleRow[]>([newRule()]);
  const yamlPreview = useSignal("");
  const showYaml = useSignal(false);
  const submitting = useSignal(false);
  const submitError = useSignal<string | null>(null);
  const submitSuccess = useSignal(false);
  const warnings = useSignal<PolicyWarning[]>([]);

  // Fetch namespaces
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, []);

  // Generate YAML preview (debounced, skip when hidden)
  useEffect(() => {
    if (!showYaml.value) return;
    const timer = setTimeout(() => {
      yamlPreview.value = buildPolicyYaml(
        name.value,
        namespace.value,
        endpointSelector.value,
        rules.value,
      );
    }, 150);
    return () => clearTimeout(timer);
  }, [
    name.value,
    namespace.value,
    endpointSelector.value,
    rules.value,
    showYaml.value,
  ]);

  // Warn on unsaved changes
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (name.value || rules.value.length > 1) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  if (!IS_BROWSER) {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
          Create Cilium Network Policy
        </h1>
        <p class="mt-2 text-gray-500">Loading editor...</p>
      </div>
    );
  }

  const addRule = () => {
    rules.value = [...rules.value, newRule()];
  };

  const removeRule = (id: number) => {
    if (rules.value.length <= 1) return;
    rules.value = rules.value.filter((r) => r.id !== id);
  };

  const updateRule = (id: number, updates: Partial<RuleRow>) => {
    rules.value = rules.value.map((r) =>
      r.id === id ? { ...r, ...updates } : r
    );
  };

  const addSelectorLabel = () => {
    endpointSelector.value = [...endpointSelector.value, {
      key: "",
      value: "",
    }];
  };

  const removeSelectorLabel = (idx: number) => {
    endpointSelector.value = endpointSelector.value.filter((_, i) => i !== idx);
  };

  const updateSelectorLabel = (
    idx: number,
    field: "key" | "value",
    val: string,
  ) => {
    endpointSelector.value = endpointSelector.value.map((l, i) =>
      i === idx ? { ...l, [field]: val } : l
    );
  };

  const validateForm = (): string | null => {
    if (!name.value.trim()) return "Policy name is required";
    if (!/^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$/.test(name.value)) {
      return "Invalid policy name: must be lowercase alphanumeric with dashes";
    }
    for (let i = 0; i < rules.value.length; i++) {
      const r = rules.value[i];
      if (r.peerType === "entities" && r.entities.length === 0) {
        return `Rule ${i + 1}: select at least one entity`;
      }
      if (r.peerType === "cidr" && !r.cidrs.trim()) {
        return `Rule ${i + 1}: enter at least one CIDR`;
      }
      if (r.ports.trim()) {
        for (
          const p of r.ports.split(",").map((s) => s.trim()).filter(Boolean)
        ) {
          const n = parseInt(p, 10);
          if (isNaN(n) || n < 1 || n > 65535) {
            return `Rule ${i + 1}: invalid port "${p}" (must be 1-65535)`;
          }
        }
      }
    }
    return null;
  };

  const handleSubmit = async () => {
    submitError.value = null;
    submitSuccess.value = false;
    warnings.value = [];

    const validationError = validateForm();
    if (validationError) {
      submitError.value = validationError;
      return;
    }

    submitting.value = true;

    try {
      const payload = buildPayload(
        name.value,
        namespace.value,
        endpointSelector.value,
        rules.value,
      );
      const resp = await apiPost<
        { resource: unknown; warnings?: PolicyWarning[] }
      >(
        `/v1/resources/ciliumnetworkpolicies/${namespace.value}`,
        payload,
      );
      if (resp.data && typeof resp.data === "object") {
        const data = resp.data as {
          warnings?: PolicyWarning[];
        };
        if (data.warnings && data.warnings.length > 0) {
          warnings.value = data.warnings;
        }
      }
      submitSuccess.value = true;
    } catch (err: unknown) {
      const msg = err instanceof Error
        ? err.message
        : "Failed to create policy";
      submitError.value = msg;
    } finally {
      submitting.value = false;
    }
  };

  return (
    <div class="p-6 max-w-5xl">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
          Create Cilium Network Policy
        </h1>
        <a
          href="/networking/cilium-policies"
          class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400"
        >
          Back to list
        </a>
      </div>

      {submitSuccess.value && (
        <div class="mb-4 rounded-md bg-green-50 dark:bg-green-900/20 p-4 border border-green-200 dark:border-green-800">
          <p class="text-sm text-green-800 dark:text-green-200">
            Policy created successfully.{" "}
            <a
              href="/networking/cilium-policies"
              class="underline font-medium"
            >
              View all policies
            </a>
          </p>
        </div>
      )}

      {warnings.value.length > 0 && (
        <div class="mb-4 rounded-md bg-amber-50 dark:bg-amber-900/20 p-4 border border-amber-200 dark:border-amber-800">
          <p class="text-sm font-medium text-amber-800 dark:text-amber-200 mb-1">
            Warnings
          </p>
          {warnings.value.map((w) => (
            <p
              key={w.code}
              class="text-sm text-amber-700 dark:text-amber-300"
            >
              {w.message}
            </p>
          ))}
        </div>
      )}

      {submitError.value && (
        <div class="mb-4 rounded-md bg-red-50 dark:bg-red-900/20 p-4 border border-red-200 dark:border-red-800">
          <p class="text-sm text-red-800 dark:text-red-200">
            {submitError.value}
          </p>
        </div>
      )}

      {/* Policy Name & Namespace */}
      <section class="mb-6 bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-4">
        <h2 class="text-lg font-medium text-gray-900 dark:text-white mb-3">
          Policy Details
        </h2>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Name
            </label>
            <input
              type="text"
              value={name.value}
              onInput={(e) => name.value = (e.target as HTMLInputElement).value}
              placeholder="my-policy"
              class="w-full rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-white"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Namespace
            </label>
            <select
              value={namespace.value}
              onChange={(e) =>
                namespace.value = (e.target as HTMLSelectElement).value}
              class="w-full rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-white"
            >
              {namespaces.value.map((ns) => (
                <option key={ns} value={ns}>{ns}</option>
              ))}
            </select>
          </div>
        </div>
      </section>

      {/* Endpoint Selector */}
      <section class="mb-6 bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-4">
        <div class="flex items-center justify-between mb-3">
          <h2 class="text-lg font-medium text-gray-900 dark:text-white">
            Applied To (Endpoint Selector)
          </h2>
          <Button variant="ghost" onClick={addSelectorLabel}>
            + Add Label
          </Button>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">
          Leave empty to match all pods in the namespace.
        </p>
        {endpointSelector.value.map((label, idx) => (
          <div key={idx} class="flex items-center gap-2 mb-2">
            <input
              type="text"
              value={label.key}
              onInput={(e) =>
                updateSelectorLabel(
                  idx,
                  "key",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="key"
              class="flex-1 rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-1.5 text-sm text-gray-900 dark:text-white"
            />
            <span class="text-gray-400">=</span>
            <input
              type="text"
              value={label.value}
              onInput={(e) =>
                updateSelectorLabel(
                  idx,
                  "value",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="value"
              class="flex-1 rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-1.5 text-sm text-gray-900 dark:text-white"
            />
            <button
              type="button"
              onClick={() =>
                removeSelectorLabel(idx)}
              class="text-red-500 hover:text-red-700 text-sm px-2"
            >
              Remove
            </button>
          </div>
        ))}
      </section>

      {/* Rules Table */}
      <section class="mb-6 bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-4">
        <div class="flex items-center justify-between mb-3">
          <h2 class="text-lg font-medium text-gray-900 dark:text-white">
            Rules
          </h2>
          <Button variant="ghost" onClick={addRule}>
            + Add Rule
          </Button>
        </div>

        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 dark:border-gray-700 text-left text-gray-600 dark:text-gray-400">
                <th class="py-2 px-2 font-medium">#</th>
                <th class="py-2 px-2 font-medium">Direction</th>
                <th class="py-2 px-2 font-medium">Peer Type</th>
                <th class="py-2 px-2 font-medium">Peers</th>
                <th class="py-2 px-2 font-medium">Ports</th>
                <th class="py-2 px-2 font-medium">Protocol</th>
                <th class="py-2 px-2 font-medium">Action</th>
                <th class="py-2 px-2 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {rules.value.map((rule, idx) => (
                <RuleRowEditor
                  key={rule.id}
                  rule={rule}
                  index={idx}
                  onUpdate={(updates) => updateRule(rule.id, updates)}
                  onRemove={() => removeRule(rule.id)}
                  canRemove={rules.value.length > 1}
                />
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* YAML Preview */}
      <section class="mb-6 bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700">
        <button
          type="button"
          onClick={() => showYaml.value = !showYaml.value}
          class="w-full flex items-center justify-between p-4 text-left"
        >
          <h2 class="text-lg font-medium text-gray-900 dark:text-white">
            YAML Preview
          </h2>
          <span class="text-gray-500 text-sm">
            {showYaml.value ? "Hide" : "Show"}
          </span>
        </button>
        {showYaml.value && (
          <div class="px-4 pb-4">
            <pre class="bg-gray-900 text-green-400 text-xs font-mono p-4 rounded-md overflow-x-auto whitespace-pre max-h-96 overflow-y-auto">
              {yamlPreview.value}
            </pre>
          </div>
        )}
      </section>

      {/* Submit */}
      <div class="flex items-center gap-4">
        <Button
          variant="primary"
          onClick={handleSubmit}
          disabled={submitting.value || !name.value}
        >
          {submitting.value ? "Creating..." : "Create Policy"}
        </Button>
        <a
          href="/networking/cilium-policies"
          class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400"
        >
          Cancel
        </a>
      </div>
    </div>
  );
}

interface RuleRowEditorProps {
  rule: RuleRow;
  index: number;
  onUpdate: (updates: Partial<RuleRow>) => void;
  onRemove: () => void;
  canRemove: boolean;
}

function RuleRowEditor(
  { rule, index, onUpdate, onRemove, canRemove }: RuleRowEditorProps,
) {
  return (
    <tr class="border-b border-gray-100 dark:border-gray-700/50 align-top">
      <td class="py-2 px-2 text-gray-500">{index + 1}</td>
      <td class="py-2 px-2">
        <select
          value={rule.direction}
          onChange={(e) =>
            onUpdate({
              direction: (e.target as HTMLSelectElement)
                .value as RuleRow["direction"],
            })}
          class="rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-white"
        >
          <option value="Ingress">Ingress</option>
          <option value="Egress">Egress</option>
        </select>
      </td>
      <td class="py-2 px-2">
        <select
          value={rule.peerType}
          onChange={(e) =>
            onUpdate({
              peerType: (e.target as HTMLSelectElement)
                .value as RuleRow["peerType"],
            })}
          class="rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-white"
        >
          <option value="endpoints">Endpoints</option>
          <option value="entities">Entities</option>
          <option value="cidr">CIDR</option>
        </select>
      </td>
      <td class="py-2 px-2 min-w-[200px]">
        <PeerInput rule={rule} onUpdate={onUpdate} />
      </td>
      <td class="py-2 px-2">
        <input
          type="text"
          value={rule.ports}
          onInput={(e) =>
            onUpdate({ ports: (e.target as HTMLInputElement).value })}
          placeholder="80,443"
          class="w-24 rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-white"
        />
      </td>
      <td class="py-2 px-2">
        <select
          value={rule.protocol}
          onChange={(e) =>
            onUpdate({
              protocol: (e.target as HTMLSelectElement)
                .value as RuleRow["protocol"],
            })}
          class="rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-white"
        >
          <option value="TCP">TCP</option>
          <option value="UDP">UDP</option>
          <option value="SCTP">SCTP</option>
          <option value="ANY">ANY</option>
        </select>
      </td>
      <td class="py-2 px-2">
        <select
          value={rule.action}
          onChange={(e) =>
            onUpdate({
              action: (e.target as HTMLSelectElement)
                .value as RuleRow["action"],
            })}
          class={`rounded border px-2 py-1 text-sm font-medium ${
            rule.action === "deny"
              ? "border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300"
              : "border-green-300 bg-green-50 text-green-700 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300"
          }`}
        >
          <option value="allow">Allow</option>
          <option value="deny">Deny</option>
        </select>
      </td>
      <td class="py-2 px-2">
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
            class="text-red-500 hover:text-red-700 text-sm"
            title="Remove rule"
          >
            X
          </button>
        )}
      </td>
    </tr>
  );
}

function PeerInput(
  { rule, onUpdate }: {
    rule: RuleRow;
    onUpdate: (u: Partial<RuleRow>) => void;
  },
) {
  if (rule.peerType === "endpoints") {
    return (
      <div class="space-y-1">
        {rule.labels.map((label, idx) => (
          <div key={idx} class="flex items-center gap-1">
            <input
              type="text"
              value={label.key}
              onInput={(e) => {
                const labels = [...rule.labels];
                labels[idx] = {
                  ...labels[idx],
                  key: (e.target as HTMLInputElement).value,
                };
                onUpdate({ labels });
              }}
              placeholder="key"
              class="w-20 rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-1.5 py-1 text-xs text-gray-900 dark:text-white"
            />
            <span class="text-gray-400 text-xs">=</span>
            <input
              type="text"
              value={label.value}
              onInput={(e) => {
                const labels = [...rule.labels];
                labels[idx] = {
                  ...labels[idx],
                  value: (e.target as HTMLInputElement).value,
                };
                onUpdate({ labels });
              }}
              placeholder="value"
              class="w-20 rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-1.5 py-1 text-xs text-gray-900 dark:text-white"
            />
            {rule.labels.length > 1 && (
              <button
                type="button"
                onClick={() => {
                  onUpdate({
                    labels: rule.labels.filter((_, i) => i !== idx),
                  });
                }}
                class="text-red-400 text-xs"
              >
                x
              </button>
            )}
          </div>
        ))}
        <button
          type="button"
          onClick={() =>
            onUpdate({ labels: [...rule.labels, { key: "", value: "" }] })}
          class="text-xs text-blue-500 hover:text-blue-700"
        >
          + label
        </button>
      </div>
    );
  }

  if (rule.peerType === "entities") {
    return (
      <div class="space-y-1">
        {VALID_ENTITIES.map((entity) => (
          <label key={entity} class="flex items-center gap-1.5 text-xs">
            <input
              type="checkbox"
              checked={rule.entities.includes(entity)}
              onChange={(e) => {
                const checked = (e.target as HTMLInputElement).checked;
                const entities = checked
                  ? [...rule.entities, entity]
                  : rule.entities.filter((e) =>
                    e !== entity
                  );
                onUpdate({ entities });
              }}
              class="rounded border-gray-300 dark:border-gray-600"
            />
            <span class="text-gray-700 dark:text-gray-300">{entity}</span>
          </label>
        ))}
      </div>
    );
  }

  // CIDR
  return (
    <input
      type="text"
      value={rule.cidrs}
      onInput={(e) => onUpdate({ cidrs: (e.target as HTMLInputElement).value })}
      placeholder="10.0.0.0/8, 192.168.0.0/16"
      class="w-full rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-white"
    />
  );
}

// Build the API payload from the form state.
function buildPayload(
  name: string,
  namespace: string,
  selectorLabels: LabelPair[],
  ruleRows: RuleRow[],
) {
  const endpointSelector: Record<string, string> = {};
  for (const l of selectorLabels) {
    if (l.key.trim()) {
      endpointSelector[l.key.trim()] = l.value.trim();
    }
  }

  const ingressRules: unknown[] = [];
  const egressRules: unknown[] = [];

  for (const row of ruleRows) {
    const rule: Record<string, unknown> = {
      peerType: row.peerType,
      action: row.action,
    };

    if (row.peerType === "endpoints") {
      const labels: Record<string, string> = {};
      for (const l of row.labels) {
        if (l.key.trim()) labels[l.key.trim()] = l.value.trim();
      }
      rule.labels = labels;
    } else if (row.peerType === "entities") {
      rule.entities = row.entities;
    } else if (row.peerType === "cidr") {
      rule.cidrs = row.cidrs
        .split(",")
        .map((c) => c.trim())
        .filter(Boolean);
    }

    // Parse ports
    if (row.ports.trim()) {
      rule.ports = row.ports
        .split(",")
        .map((p) => p.trim())
        .filter(Boolean)
        .map((p) => ({
          port: parseInt(p, 10),
          protocol: row.protocol,
        }));
    }

    if (row.direction === "Ingress") {
      ingressRules.push(rule);
    } else {
      egressRules.push(rule);
    }
  }

  return { name, namespace, endpointSelector, ingressRules, egressRules };
}

// Escape a string for safe inclusion in YAML output.
function yamlEscape(s: string): string {
  if (
    /[":{}[\],&*?|<>=!%@`#\n\r\t\\]/.test(s) || s.trim() !== s || s === ""
  ) {
    return '"' + s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(
      /\n/g,
      "\\n",
    ) + '"';
  }
  return s;
}

// Generate a YAML preview string from the form state.
function buildPolicyYaml(
  name: string,
  namespace: string,
  selectorLabels: LabelPair[],
  ruleRows: RuleRow[],
): string {
  const lines: string[] = [
    "apiVersion: cilium.io/v2",
    "kind: CiliumNetworkPolicy",
    "metadata:",
    `  name: ${name || "<name>"}`,
    `  namespace: ${namespace}`,
    "spec:",
  ];

  // Endpoint selector
  const activeLabels = selectorLabels.filter((l) => l.key.trim());
  if (activeLabels.length > 0) {
    lines.push("  endpointSelector:");
    lines.push("    matchLabels:");
    for (const l of activeLabels) {
      lines.push(`      ${yamlEscape(l.key)}: ${yamlEscape(l.value)}`);
    }
  } else {
    lines.push("  endpointSelector: {}");
  }

  const ingress = ruleRows.filter((r) => r.direction === "Ingress");
  const egress = ruleRows.filter((r) => r.direction === "Egress");

  const ingressAllow = ingress.filter((r) => r.action === "allow");
  const ingressDeny = ingress.filter((r) => r.action === "deny");
  const egressAllow = egress.filter((r) => r.action === "allow");
  const egressDeny = egress.filter((r) => r.action === "deny");

  const renderRules = (
    rls: RuleRow[],
    key: string,
    direction: string,
  ) => {
    if (rls.length === 0) return;
    lines.push(`  ${key}:`);
    for (const r of rls) {
      const prefix = direction === "Ingress" ? "from" : "to";
      lines.push("  - " + peerYaml(r, prefix));
      if (r.ports.trim()) {
        lines.push("    toPorts:");
        lines.push("    - ports:");
        for (
          const p of r.ports.split(",").map((s) => s.trim()).filter(Boolean)
        ) {
          lines.push(`      - port: "${p}"`);
          lines.push(`        protocol: ${r.protocol}`);
        }
      }
    }
  };

  renderRules(ingressAllow, "ingress", "Ingress");
  renderRules(ingressDeny, "ingressDeny", "Ingress");
  renderRules(egressAllow, "egress", "Egress");
  renderRules(egressDeny, "egressDeny", "Egress");

  return lines.join("\n");
}

function peerYaml(rule: RuleRow, prefix: string): string {
  if (rule.peerType === "endpoints") {
    const activeLabels = rule.labels.filter((l) => l.key.trim());
    if (activeLabels.length === 0) return `${prefix}Endpoints:\n    - {}`;
    const labelLines = activeLabels.map((l) =>
      `          ${yamlEscape(l.key)}: ${yamlEscape(l.value)}`
    )
      .join("\n");
    return `${prefix}Endpoints:\n    - matchLabels:\n${labelLines}`;
  }
  if (rule.peerType === "entities") {
    if (rule.entities.length === 0) return `${prefix}Entities: []`;
    const entityLines = rule.entities.map((e) => `    - ${e}`).join("\n");
    return `${prefix}Entities:\n${entityLines}`;
  }
  // cidr
  const cidrs = rule.cidrs.split(",").map((c) => c.trim()).filter(Boolean);
  if (cidrs.length === 0) return `${prefix}CIDR: []`;
  const cidrLines = cidrs.map((c) => `    - ${c}`).join("\n");
  return `${prefix}CIDR:\n${cidrLines}`;
}
