/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import type { LabelEntry } from "@/lib/wizard-types.ts";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface RuleRow {
  id: number;
  direction: "Ingress" | "Egress";
  peerType: "endpoints" | "entities" | "cidr";
  labels: LabelEntry[];
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

/**
 * The page root's classes, shared by the SSR placeholder and the hydrated root
 * so the two cannot diverge. Preact applies the server-rendered root's class
 * once at hydration and never re-diffs that node, so a class present on one
 * root and missing from the other sticks for the life of the page.
 */
const EDITOR_PAGE_CLASS = "p-6 max-w-5xl";

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

interface CiliumPolicyEditorProps {
  onClose?: () => void;
}

export default function CiliumPolicyEditor({
  onClose,
}: CiliumPolicyEditorProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const isClient = useIsClient();
  const [name, setName] = useState("");
  const [namespace, setNamespace] = useState(() => initialNamespace());
  const [namespaceList] = useNamespaces();
  const [endpointSelector, setEndpointSelector] = useState<LabelEntry[]>([
    { key: "", value: "" },
  ]);
  const [rules, setRules] = useState<RuleRow[]>(() => [newRule()]);
  const [yamlPreview, setYamlPreview] = useState("");
  const [showYaml, setShowYaml] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitSuccess, setSubmitSuccess] = useState(false);
  const [warnings, setWarnings] = useState<PolicyWarning[]>([]);

  // Generate YAML preview (debounced, skip when hidden)
  useEffect(() => {
    if (!showYaml) return;
    const timer = setTimeout(() => {
      setYamlPreview(buildPolicyYaml(name, namespace, endpointSelector, rules));
    }, 150);
    return () => clearTimeout(timer);
  }, [name, namespace, endpointSelector, rules, showYaml]);

  useDirtyGuard(Boolean(name || rules.length > 1));

  if (!isClient) {
    // Must carry EDITOR_PAGE_CLASS, identical to the hydrated root below.
    // This island is mounted with client:load from
    // src/pages/networking/cilium-policies/new.astro, so it is server-rendered
    // and hydrated in place — and Preact keeps the server-rendered root's
    // class, only recursing into children. When this root omitted
    // `max-w-5xl`, the form lost its width constraint for the life of the
    // page with nothing in the build or the E2E suite to catch it.
    return (
      <div className={EDITOR_PAGE_CLASS}>
        <h1 className="text-2xl font-semibold text-text-primary">
          Create Cilium Network Policy
        </h1>
        <p className="mt-2 text-text-muted">Loading editor...</p>
      </div>
    );
  }

  const addRule = () => {
    setRules((prev) => [...prev, newRule()]);
  };

  const removeRule = (id: number) => {
    setRules((prev) =>
      prev.length <= 1 ? prev : prev.filter((r) => r.id !== id),
    );
  };

  const updateRule = (id: number, updates: Partial<RuleRow>) => {
    setRules((prev) =>
      prev.map((r) => (r.id === id ? { ...r, ...updates } : r)),
    );
  };

  const addSelectorLabel = () => {
    setEndpointSelector((prev) => [
      ...prev,
      {
        key: "",
        value: "",
      },
    ]);
  };

  const removeSelectorLabel = (idx: number) => {
    setEndpointSelector((prev) => prev.filter((_, i) => i !== idx));
  };

  const updateSelectorLabel = (
    idx: number,
    field: "key" | "value",
    val: string,
  ) => {
    setEndpointSelector((prev) =>
      prev.map((l, i) => (i === idx ? { ...l, [field]: val } : l)),
    );
  };

  const validateForm = (): string | null => {
    if (!name.trim()) return "Policy name is required";
    if (!/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(name)) {
      return "Invalid policy name: must be lowercase alphanumeric with dashes";
    }
    for (let i = 0; i < rules.length; i++) {
      const r = rules[i];
      if (r.peerType === "entities" && r.entities.length === 0) {
        return `Rule ${i + 1}: select at least one entity`;
      }
      if (r.peerType === "cidr" && !r.cidrs.trim()) {
        return `Rule ${i + 1}: enter at least one CIDR`;
      }
      if (r.ports.trim()) {
        for (const p of r.ports
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)) {
          const n = parseInt(p, 10);
          if (Number.isNaN(n) || n < 1 || n > 65535) {
            return `Rule ${i + 1}: invalid port"${p}" (must be 1-65535)`;
          }
        }
      }
    }
    return null;
  };

  const handleSubmit = async () => {
    setSubmitError(null);
    setSubmitSuccess(false);
    setWarnings([]);

    const validationError = validateForm();
    if (validationError) {
      setSubmitError(validationError);
      return;
    }

    setSubmitting(true);

    try {
      const payload = buildPayload(name, namespace, endpointSelector, rules);
      const resp = await apiPost<{
        resource: unknown;
        warnings?: PolicyWarning[];
      }>(`/v1/resources/ciliumnetworkpolicies/${namespace}`, payload);
      if (resp.data && typeof resp.data === "object") {
        const data = resp.data as {
          warnings?: PolicyWarning[];
        };
        if (data.warnings && data.warnings.length > 0) {
          setWarnings(data.warnings);
        }
      }
      setSubmitSuccess(true);
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to create policy";
      setSubmitError(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className={EDITOR_PAGE_CLASS}>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-text-primary">
          Create Cilium Network Policy
        </h1>
        <button
          type="button"
          onClick={close}
          className="text-sm text-text-muted hover:text-text-secondary"
        >
          Back to list
        </button>
      </div>

      {submitSuccess && (
        <div className="mb-4 rounded-md bg-success-dim p-4 border border-success">
          <p className="text-sm text-success">
            Policy created successfully.{""}
            <a
              href="/networking/cilium-policies"
              className="underline font-medium"
            >
              View all policies
            </a>
          </p>
        </div>
      )}

      {warnings.length > 0 && (
        <div className="mb-4 rounded-md bg-warning-dim p-4 border border-warning">
          <p className="text-sm font-medium text-warning mb-1">Warnings</p>
          {warnings.map((w) => (
            <p key={w.code} className="text-sm text-warning">
              {w.message}
            </p>
          ))}
        </div>
      )}

      {submitError && (
        <div className="mb-4 rounded-md bg-danger-dim p-4 border border-danger">
          <p className="text-sm text-danger">{submitError}</p>
        </div>
      )}

      {/* Policy Name & Namespace */}
      <section className="mb-6 bg-surface rounded-lg border border-border-primary p-4">
        <h2 className="text-lg font-medium text-text-primary mb-3">
          Policy Details
        </h2>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              placeholder="my-policy"
              className="w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Namespace
            </label>
            <select
              value={namespace}
              onChange={(e) => setNamespace(e.currentTarget.value)}
              className="w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
            >
              {namespaceList.map((ns) => (
                <option key={ns} value={ns}>
                  {ns}
                </option>
              ))}
            </select>
          </div>
        </div>
      </section>

      {/* Endpoint Selector */}
      <section className="mb-6 bg-surface rounded-lg border border-border-primary p-4">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-lg font-medium text-text-primary">
            Applied To (Endpoint Selector)
          </h2>
          <Button variant="ghost" onClick={addSelectorLabel}>
            + Add Label
          </Button>
        </div>
        <p className="text-xs text-text-muted mb-3">
          Leave empty to match all pods in the namespace.
        </p>
        {endpointSelector.map((label, idx) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: label rows are plain key/value pairs with no id, and the inputs are controlled
          <div key={idx} className="flex items-center gap-2 mb-2">
            <input
              type="text"
              value={label.key}
              onChange={(e) =>
                updateSelectorLabel(idx, "key", e.currentTarget.value)
              }
              placeholder="key"
              className="flex-1 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary"
            />
            <span className="text-text-muted">=</span>
            <input
              type="text"
              value={label.value}
              onChange={(e) =>
                updateSelectorLabel(idx, "value", e.currentTarget.value)
              }
              placeholder="value"
              className="flex-1 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary"
            />
            <button
              type="button"
              onClick={() => removeSelectorLabel(idx)}
              className="text-error hover:text-error text-sm px-2"
            >
              Remove
            </button>
          </div>
        ))}
      </section>

      {/* Rules Table */}
      <section className="mb-6 bg-surface rounded-lg border border-border-primary p-4">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-lg font-medium text-text-primary">Rules</h2>
          <Button variant="ghost" onClick={addRule}>
            + Add Rule
          </Button>
        </div>

        <div className="overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="border-b border-border-primary text-left text-text-secondary">
                <th className="py-2 px-2 font-medium">#</th>
                <th className="py-2 px-2 font-medium">Direction</th>
                <th className="py-2 px-2 font-medium">Peer Type</th>
                <th className="py-2 px-2 font-medium">Peers</th>
                <th className="py-2 px-2 font-medium">Ports</th>
                <th className="py-2 px-2 font-medium">Protocol</th>
                <th className="py-2 px-2 font-medium">Action</th>
                <th className="py-2 px-2 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule, idx) => (
                <RuleRowEditor
                  key={rule.id}
                  rule={rule}
                  index={idx}
                  onUpdate={(updates) => updateRule(rule.id, updates)}
                  onRemove={() => removeRule(rule.id)}
                  canRemove={rules.length > 1}
                />
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* YAML Preview */}
      <section className="mb-6 bg-surface rounded-lg border border-border-primary">
        <button
          type="button"
          onClick={() => setShowYaml((prev) => !prev)}
          className="w-full flex items-center justify-between p-4 text-left"
        >
          <h2 className="text-lg font-medium text-text-primary">
            YAML Preview
          </h2>
          <span className="text-text-muted text-sm">
            {showYaml ? "Hide" : "Show"}
          </span>
        </button>
        {showYaml && (
          <div className="px-4 pb-4">
            <pre className="bg-base text-success text-xs font-mono p-4 rounded-md overflow-x-auto whitespace-pre max-h-96 overflow-y-auto">
              {yamlPreview}
            </pre>
          </div>
        )}
      </section>

      {/* Submit */}
      <div className="flex items-center gap-4">
        <Button
          variant="primary"
          onClick={handleSubmit}
          disabled={submitting || !name}
        >
          {submitting ? "Creating..." : "Create Policy"}
        </Button>
        <button
          type="button"
          onClick={close}
          className="text-sm text-text-muted hover:text-text-secondary"
        >
          Cancel
        </button>
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

function RuleRowEditor({
  rule,
  index,
  onUpdate,
  onRemove,
  canRemove,
}: RuleRowEditorProps) {
  return (
    <tr className="border-b border-border-subtle align-top">
      <td className="py-2 px-2 text-text-muted">{index + 1}</td>
      <td className="py-2 px-2">
        <select
          value={rule.direction}
          onChange={(e) =>
            onUpdate({
              direction: e.currentTarget.value as RuleRow["direction"],
            })
          }
          className="rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
        >
          <option value="Ingress">Ingress</option>
          <option value="Egress">Egress</option>
        </select>
      </td>
      <td className="py-2 px-2">
        <select
          value={rule.peerType}
          onChange={(e) =>
            onUpdate({
              peerType: e.currentTarget.value as RuleRow["peerType"],
            })
          }
          className="rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
        >
          <option value="endpoints">Endpoints</option>
          <option value="entities">Entities</option>
          <option value="cidr">CIDR</option>
        </select>
      </td>
      <td className="py-2 px-2 min-w-[200px]">
        <PeerInput rule={rule} onUpdate={onUpdate} />
      </td>
      <td className="py-2 px-2">
        <input
          type="text"
          value={rule.ports}
          onChange={(e) => onUpdate({ ports: e.currentTarget.value })}
          placeholder="80,443"
          className="w-24 rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
        />
      </td>
      <td className="py-2 px-2">
        <select
          value={rule.protocol}
          onChange={(e) =>
            onUpdate({
              protocol: e.currentTarget.value as RuleRow["protocol"],
            })
          }
          className="rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
        >
          <option value="TCP">TCP</option>
          <option value="UDP">UDP</option>
          <option value="SCTP">SCTP</option>
          <option value="ANY">ANY</option>
        </select>
      </td>
      <td className="py-2 px-2">
        <select
          value={rule.action}
          onChange={(e) =>
            onUpdate({
              action: e.currentTarget.value as RuleRow["action"],
            })
          }
          className={`rounded border px-2 py-1 text-sm font-medium ${
            rule.action === "deny"
              ? "border-danger bg-danger-dim text-danger"
              : "border-success bg-success-dim text-success"
          }`}
        >
          <option value="allow">Allow</option>
          <option value="deny">Deny</option>
        </select>
      </td>
      <td className="py-2 px-2">
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
            className="text-error hover:text-error text-sm"
            title="Remove rule"
          >
            X
          </button>
        )}
      </td>
    </tr>
  );
}

function PeerInput({
  rule,
  onUpdate,
}: {
  rule: RuleRow;
  onUpdate: (u: Partial<RuleRow>) => void;
}) {
  if (rule.peerType === "endpoints") {
    return (
      <div className="space-y-1">
        {rule.labels.map((label, idx) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: label rows are plain key/value pairs with no id, and the inputs are controlled
          <div key={idx} className="flex items-center gap-1">
            <input
              type="text"
              value={label.key}
              onChange={(e) => {
                const labels = [...rule.labels];
                labels[idx] = {
                  ...labels[idx],
                  key: e.currentTarget.value,
                };
                onUpdate({ labels });
              }}
              placeholder="key"
              className="w-20 rounded border border-border-primary bg-surface px-1.5 py-1 text-xs text-text-primary"
            />
            <span className="text-text-muted text-xs">=</span>
            <input
              type="text"
              value={label.value}
              onChange={(e) => {
                const labels = [...rule.labels];
                labels[idx] = {
                  ...labels[idx],
                  value: e.currentTarget.value,
                };
                onUpdate({ labels });
              }}
              placeholder="value"
              className="w-20 rounded border border-border-primary bg-surface px-1.5 py-1 text-xs text-text-primary"
            />
            {rule.labels.length > 1 && (
              <button
                type="button"
                onClick={() => {
                  onUpdate({
                    labels: rule.labels.filter((_, i) => i !== idx),
                  });
                }}
                className="text-error text-xs"
              >
                x
              </button>
            )}
          </div>
        ))}
        <button
          type="button"
          onClick={() =>
            onUpdate({ labels: [...rule.labels, { key: "", value: "" }] })
          }
          className="text-xs text-accent hover:text-accent"
        >
          + label
        </button>
      </div>
    );
  }

  if (rule.peerType === "entities") {
    return (
      <div className="space-y-1">
        {VALID_ENTITIES.map((entity) => (
          <label key={entity} className="flex items-center gap-1.5 text-xs">
            <input
              type="checkbox"
              checked={rule.entities.includes(entity)}
              onChange={(e) => {
                const checked = e.currentTarget.checked;
                const entities = checked
                  ? [...rule.entities, entity]
                  : rule.entities.filter((e) => e !== entity);
                onUpdate({ entities });
              }}
              className="rounded border-border-primary"
            />
            <span className="text-text-secondary">{entity}</span>
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
      onChange={(e) => onUpdate({ cidrs: e.currentTarget.value })}
      placeholder="10.0.0.0/8, 192.168.0.0/16"
      className="w-full rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
    />
  );
}

// Build the API payload from the form state.
function buildPayload(
  name: string,
  namespace: string,
  selectorLabels: LabelEntry[],
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
  if (/[":{}[\],&*?|<>=!%@`#\n\r\t\\]/.test(s) || s.trim() !== s || s === "") {
    return (
      '"' +
      s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n") +
      '"'
    );
  }
  return s;
}

// Generate a YAML preview string from the form state.
function buildPolicyYaml(
  name: string,
  namespace: string,
  selectorLabels: LabelEntry[],
  ruleRows: RuleRow[],
): string {
  const lines: string[] = [
    "apiVersion: cilium.io/v2",
    "kind: CiliumNetworkPolicy",
    "metadata:",
    ` name: ${name || "<name>"}`,
    ` namespace: ${namespace}`,
    "spec:",
  ];

  // Endpoint selector
  const activeLabels = selectorLabels.filter((l) => l.key.trim());
  if (activeLabels.length > 0) {
    lines.push(" endpointSelector:");
    lines.push(" matchLabels:");
    for (const l of activeLabels) {
      lines.push(` ${yamlEscape(l.key)}: ${yamlEscape(l.value)}`);
    }
  } else {
    lines.push(" endpointSelector: {}");
  }

  const ingress = ruleRows.filter((r) => r.direction === "Ingress");
  const egress = ruleRows.filter((r) => r.direction === "Egress");

  const ingressAllow = ingress.filter((r) => r.action === "allow");
  const ingressDeny = ingress.filter((r) => r.action === "deny");
  const egressAllow = egress.filter((r) => r.action === "allow");
  const egressDeny = egress.filter((r) => r.action === "deny");

  const renderRules = (rls: RuleRow[], key: string, direction: string) => {
    if (rls.length === 0) return;
    lines.push(` ${key}:`);
    for (const r of rls) {
      const prefix = direction === "Ingress" ? "from" : "to";
      lines.push(` -${peerYaml(r, prefix)}`);
      if (r.ports.trim()) {
        lines.push(" toPorts:");
        lines.push(" - ports:");
        for (const p of r.ports
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)) {
          lines.push(` - port:"${p}"`);
          lines.push(` protocol: ${r.protocol}`);
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
    if (activeLabels.length === 0) return `${prefix}Endpoints:\n - {}`;
    const labelLines = activeLabels
      .map((l) => ` ${yamlEscape(l.key)}: ${yamlEscape(l.value)}`)
      .join("\n");
    return `${prefix}Endpoints:\n - matchLabels:\n${labelLines}`;
  }
  if (rule.peerType === "entities") {
    if (rule.entities.length === 0) return `${prefix}Entities: []`;
    const entityLines = rule.entities.map((e) => ` - ${e}`).join("\n");
    return `${prefix}Entities:\n${entityLines}`;
  }
  // cidr
  const cidrs = rule.cidrs
    .split(",")
    .map((c) => c.trim())
    .filter(Boolean);
  if (cidrs.length === 0) return `${prefix}CIDR: []`;
  const cidrLines = cidrs.map((c) => ` - ${c}`).join("\n");
  return `${prefix}CIDR:\n${cidrLines}`;
}
