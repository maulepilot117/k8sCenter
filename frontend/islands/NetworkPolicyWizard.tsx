import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import type { LabelEntry } from "@/lib/wizard-types.ts";
import {
  DNS_LABEL_REGEX,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

interface NPPortState {
  port: number;
  protocol: "TCP" | "UDP" | "SCTP";
}

interface NPPeerState {
  type: "podSelector" | "namespaceSelector" | "ipBlock";
  labels: LabelEntry[];
  cidr: string;
  except: string[];
}

interface NPRuleState {
  peers: NPPeerState[];
  ports: NPPortState[];
}

interface NetworkPolicyFormState {
  name: string;
  namespace: string;
  podSelectorLabels: LabelEntry[];
  policyTypes: string[];
  ingressRules: NPRuleState[];
  egressRules: NPRuleState[];
}

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name & policy types" },
  { label: "Rules", sub: "Ingress & egress peers" },
  { label: "Review", sub: "Preview & apply" },
];

const PROTOCOL_OPTIONS = ["TCP", "UDP", "SCTP"] as const;
const PEER_TYPE_OPTIONS = [
  { value: "podSelector", label: "Pod Selector" },
  { value: "namespaceSelector", label: "Namespace Selector" },
  { value: "ipBlock", label: "IP Block (CIDR)" },
];

function newLabelEntry(): LabelEntry {
  return { key: "", value: "" };
}

function newPort(): NPPortState {
  return { port: 0, protocol: "TCP" };
}

function newPeer(): NPPeerState {
  return {
    type: "podSelector",
    labels: [newLabelEntry()],
    cidr: "",
    except: [],
  };
}

function newRule(): NPRuleState {
  return { peers: [], ports: [] };
}

function initialState(): NetworkPolicyFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    podSelectorLabels: [],
    policyTypes: ["Ingress"],
    ingressRules: [],
    egressRules: [],
  };
}

function buildPeer(peer: NPPeerState): Record<string, unknown> {
  if (peer.type === "ipBlock") {
    return {
      ipBlock: {
        cidr: peer.cidr,
        except: peer.except.filter((e) => e.trim() !== ""),
      },
    };
  }
  const labels = Object.fromEntries(
    peer.labels.filter((l) => l.key.trim() !== "").map((l) => [l.key, l.value]),
  );
  return peer.type === "podSelector"
    ? { podSelector: labels }
    : { namespaceSelector: labels };
}

function buildManifest(f: NetworkPolicyFormState): string {
  const selectorLabels = f.podSelectorLabels
    .filter((l) => l.key.trim())
    .map((l) => `      ${l.key}: "${l.value}"`)
    .join("\n");

  let y =
    `apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: ${
      f.name || "<name>"
    }\n  namespace: ${f.namespace}\nspec:\n  podSelector:`;
  y += selectorLabels
    ? `\n    matchLabels:\n${selectorLabels}`
    : `\n    matchLabels: {}`;
  y += `\n  policyTypes:\n${f.policyTypes.map((t) => `    - ${t}`).join("\n")}`;

  if (f.policyTypes.includes("Ingress") && f.ingressRules.length > 0) {
    y += `\n  ingress:\n    - {}`;
  }
  if (f.policyTypes.includes("Egress") && f.egressRules.length > 0) {
    y += `\n  egress:\n    - {}`;
  }
  return y;
}

export default function NetworkPolicyWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<NetworkPolicyFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  useDirtyGuard(dirty);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  // Pod selector label helpers
  const addPodSelectorLabel = useCallback(() => {
    dirty.value = true;
    form.value = {
      ...form.value,
      podSelectorLabels: [...form.value.podSelectorLabels, newLabelEntry()],
    };
  }, []);

  const removePodSelectorLabel = useCallback((idx: number) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      podSelectorLabels: form.value.podSelectorLabels.filter((_, i) =>
        i !== idx
      ),
    };
  }, []);

  const updatePodSelectorLabel = useCallback(
    (idx: number, field: "key" | "value", value: string) => {
      dirty.value = true;
      const labels = [...form.value.podSelectorLabels];
      labels[idx] = { ...labels[idx], [field]: value };
      form.value = { ...form.value, podSelectorLabels: labels };
    },
    [],
  );

  // Policy type toggle
  const togglePolicyType = useCallback((type: string, checked: boolean) => {
    dirty.value = true;
    const current = form.value.policyTypes;
    const next = checked
      ? [...current, type]
      : current.filter((t) => t !== type);
    form.value = { ...form.value, policyTypes: next };
  }, []);

  // Rule helpers (parameterized by rule type)
  const addRule = useCallback((ruleType: "ingressRules" | "egressRules") => {
    dirty.value = true;
    form.value = {
      ...form.value,
      [ruleType]: [...form.value[ruleType], newRule()],
    };
  }, []);

  const removeRule = useCallback(
    (ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
      dirty.value = true;
      form.value = {
        ...form.value,
        [ruleType]: form.value[ruleType].filter((_, i) => i !== ruleIdx),
      };
    },
    [],
  );

  // Generic rule update helper
  const updateRuleArray = useCallback(
    (
      ruleType: "ingressRules" | "egressRules",
      ruleIdx: number,
      updater: (rule: NPRuleState) => NPRuleState,
    ) => {
      dirty.value = true;
      const rules = [...form.value[ruleType]];
      rules[ruleIdx] = updater(rules[ruleIdx]);
      form.value = { ...form.value, [ruleType]: rules };
    },
    [],
  );

  // Peer helpers
  const addPeer = useCallback(
    (ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => ({
        ...rule,
        peers: [...rule.peers, newPeer()],
      }));
    },
    [],
  );

  const removePeer = useCallback(
    (
      ruleType: "ingressRules" | "egressRules",
      ruleIdx: number,
      peerIdx: number,
    ) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => ({
        ...rule,
        peers: rule.peers.filter((_, i) => i !== peerIdx),
      }));
    },
    [],
  );

  const updatePeer = useCallback(
    (
      ruleType: "ingressRules" | "egressRules",
      ruleIdx: number,
      peerIdx: number,
      updater: (peer: NPPeerState) => NPPeerState,
    ) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => {
        const peers = [...rule.peers];
        peers[peerIdx] = updater(peers[peerIdx]);
        return { ...rule, peers };
      });
    },
    [],
  );

  // Port helpers
  const addPort = useCallback(
    (ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => ({
        ...rule,
        ports: [...rule.ports, newPort()],
      }));
    },
    [],
  );

  const removePort = useCallback(
    (
      ruleType: "ingressRules" | "egressRules",
      ruleIdx: number,
      portIdx: number,
    ) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => ({
        ...rule,
        ports: rule.ports.filter((_, i) => i !== portIdx),
      }));
    },
    [],
  );

  const updatePort = useCallback(
    (
      ruleType: "ingressRules" | "egressRules",
      ruleIdx: number,
      portIdx: number,
      field: keyof NPPortState,
      value: unknown,
    ) => {
      updateRuleArray(ruleType, ruleIdx, (rule) => {
        const ports = [...rule.ports];
        ports[portIdx] = { ...ports[portIdx], [field]: value };
        return { ...rule, ports };
      });
    },
    [],
  );

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.namespace) errs.namespace = "Required";
      if (f.policyTypes.length === 0) {
        errs.policyTypes = "At least one policy type is required";
      }
    }

    if (step === 1) {
      const CIDR_REGEX = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;

      const validateRules = (
        rules: NPRuleState[],
        direction: "ingress" | "egress",
      ) => {
        rules.forEach((rule, ruleIdx) => {
          rule.ports.forEach((p, portIdx) => {
            if (p.port !== 0 && (p.port < 1 || p.port > MAX_PORT)) {
              errs[`${direction}[${ruleIdx}].ports[${portIdx}].port`] =
                `Must be 1-${MAX_PORT}`;
            }
          });
          // CRITICAL: reject half-filled peer rows per NetworkPolicy peer invariant
          rule.peers.forEach((peer, peerIdx) => {
            if (
              peer.type === "ipBlock" &&
              (!peer.cidr || !CIDR_REGEX.test(peer.cidr))
            ) {
              errs[`${direction}[${ruleIdx}].peers[${peerIdx}].cidr`] =
                "Must be a valid CIDR (e.g. 10.0.0.0/8)";
            }
            peer.except.forEach((exc, excIdx) => {
              if (exc && !CIDR_REGEX.test(exc)) {
                errs[
                  `${direction}Rules[${ruleIdx}].peers[${peerIdx}].except[${excIdx}]`
                ] = "Must be a valid CIDR";
              }
            });
          });
        });
      };

      if (f.policyTypes.includes("Ingress")) {
        validateRules(f.ingressRules, "ingress");
      }

      if (f.policyTypes.includes("Egress")) {
        validateRules(f.egressRules, "egress");
      }
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;

    const payload = {
      name: f.name,
      namespace: f.namespace,
      podSelector: Object.fromEntries(
        f.podSelectorLabels
          .filter((l) => l.key.trim() !== "")
          .map((l) => [l.key, l.value]),
      ),
      policyTypes: f.policyTypes,
      ingress: f.ingressRules.map((rule) => ({
        from: rule.peers.map(buildPeer),
        ports: rule.ports
          .filter((p) => p.port > 0)
          .map((p) => ({ port: p.port, protocol: p.protocol })),
      })),
      egress: f.egressRules.map((rule) => ({
        to: rule.peers.map(buildPeer),
        ports: rule.ports
          .filter((p) => p.port > 0)
          .map((p) => ({ port: p.port, protocol: p.protocol })),
      })),
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/networkpolicy/preview",
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

  const f = form.value;

  return (
    <WizardShell
      title="Create Network Policy"
      subtitle={`Step ${currentStep.value + 1} of 3 · namespace ${f.namespace}`}
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
          <rect x="2" y="2" width="7" height="7" rx="1.5" />
          <rect x="11" y="2" width="7" height="7" rx="1.5" />
          <rect x="2" y="11" width="7" height="7" rx="1.5" />
          <rect x="11" y="11" width="7" height="7" rx="1.5" />
          <path d="M9 5.5h2M5.5 9v2M14.5 9v2M9 14.5h2" />
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
      nextLabel={currentStep.value === 1
        ? "Preview YAML"
        : currentStep.value === 2
        ? "Close"
        : "Next"}
      yaml={currentStep.value < 2 ? buildManifest(f) : undefined}
    >
      {/* Step 0: Basics */}
      {currentStep.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "460px",
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
              Name <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <input
              type="text"
              value={f.name}
              onInput={(e) =>
                updateField("name", (e.target as HTMLInputElement).value)}
              placeholder="my-network-policy"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.value.name && (
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.name}
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
              Namespace <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <select
              value={f.namespace}
              onChange={(e) =>
                updateField(
                  "namespace",
                  (e.target as HTMLSelectElement).value,
                )}
              class={WIZARD_INPUT_CLASS}
            >
              {namespaces.value.map((ns) => (
                <option key={ns} value={ns}>{ns}</option>
              ))}
            </select>
            {errors.value.namespace && (
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.namespace}
              </p>
            )}
          </div>

          {/* Pod Selector Labels */}
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
                  fontSize: "12.5px",
                  fontWeight: 600,
                  color: "var(--text-secondary)",
                }}
              >
                Pod Selector Labels{" "}
                <span
                  style={{
                    fontSize: "11px",
                    fontWeight: 400,
                    color: "var(--text-muted)",
                  }}
                >
                  (empty = all pods)
                </span>
              </label>
              <button
                type="button"
                onClick={addPodSelectorLabel}
                style={{
                  fontSize: "12px",
                  color: "var(--accent)",
                  background: "transparent",
                  border: "none",
                  cursor: "pointer",
                  padding: 0,
                }}
              >
                + Add Label
              </button>
            </div>
            {f.podSelectorLabels.length === 0 && (
              <p
                style={{
                  fontSize: "11px",
                  color: "var(--text-muted)",
                  fontStyle: "italic",
                }}
              >
                No labels — policy applies to all pods in the namespace.
              </p>
            )}
            <div
              style={{ display: "flex", flexDirection: "column", gap: "6px" }}
            >
              {f.podSelectorLabels.map((label, idx) => (
                <div
                  key={idx}
                  style={{ display: "flex", gap: "8px", alignItems: "center" }}
                >
                  <input
                    type="text"
                    value={label.key}
                    onInput={(e) =>
                      updatePodSelectorLabel(
                        idx,
                        "key",
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="key"
                    class={WIZARD_INPUT_CLASS}
                    style={{ flex: 1 }}
                  />
                  <span
                    style={{ color: "var(--text-muted)", fontSize: "12px" }}
                  >
                    =
                  </span>
                  <input
                    type="text"
                    value={label.value}
                    onInput={(e) =>
                      updatePodSelectorLabel(
                        idx,
                        "value",
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="value"
                    class={WIZARD_INPUT_CLASS}
                    style={{ flex: 1 }}
                  />
                  <button
                    type="button"
                    onClick={() => removePodSelectorLabel(idx)}
                    style={{
                      fontSize: "13px",
                      color: "var(--danger)",
                      background: "transparent",
                      border: "none",
                      cursor: "pointer",
                      flexShrink: 0,
                    }}
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          </div>

          {/* Policy Types */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "8px",
              }}
            >
              Policy Types <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <div style={{ display: "flex", gap: "20px" }}>
              {["Ingress", "Egress"].map((type) => (
                <label
                  key={type}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    cursor: "pointer",
                  }}
                >
                  <input
                    type="checkbox"
                    checked={f.policyTypes.includes(type)}
                    onChange={(e) =>
                      togglePolicyType(
                        type,
                        (e.target as HTMLInputElement).checked,
                      )}
                    style={{ width: "15px", height: "15px" }}
                  />
                  <span
                    style={{
                      fontSize: "13px",
                      color: "var(--text-secondary)",
                    }}
                  >
                    {type}
                  </span>
                </label>
              ))}
            </div>
            {errors.value.policyTypes && (
              <p
                style={{
                  marginTop: "6px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.policyTypes}
              </p>
            )}
          </div>
        </div>
      )}

      {/* Step 1: Rules */}
      {currentStep.value === 1 && (
        <div style={{ display: "flex", flexDirection: "column", gap: "24px" }}>
          {/* Ingress Rules */}
          {f.policyTypes.includes("Ingress") && (
            <div>
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  marginBottom: "10px",
                }}
              >
                <h3
                  style={{
                    margin: 0,
                    fontSize: "14px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  Ingress Rules
                </h3>
                <button
                  type="button"
                  onClick={() => addRule("ingressRules")}
                  style={{
                    fontSize: "12px",
                    color: "var(--accent)",
                    background: "transparent",
                    border: "none",
                    cursor: "pointer",
                    padding: 0,
                  }}
                >
                  + Add Ingress Rule
                </button>
              </div>
              {f.ingressRules.length === 0 && (
                <p
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    fontStyle: "italic",
                  }}
                >
                  No rules — all ingress traffic is denied by default.
                </p>
              )}
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                {f.ingressRules.map((rule, ruleIdx) => (
                  <RuleEditor
                    key={ruleIdx}
                    ruleIdx={ruleIdx}
                    rule={rule}
                    direction="Ingress"
                    errors={errors.value}
                    errorPrefix={`ingress[${ruleIdx}]`}
                    onRemove={() => removeRule("ingressRules", ruleIdx)}
                    onAddPeer={() =>
                      addPeer("ingressRules", ruleIdx)}
                    onRemovePeer={(peerIdx) =>
                      removePeer("ingressRules", ruleIdx, peerIdx)}
                    onUpdatePeer={(peerIdx, updater) =>
                      updatePeer("ingressRules", ruleIdx, peerIdx, updater)}
                    onAddPort={() => addPort("ingressRules", ruleIdx)}
                    onRemovePort={(portIdx) =>
                      removePort("ingressRules", ruleIdx, portIdx)}
                    onUpdatePort={(portIdx, field, value) =>
                      updatePort(
                        "ingressRules",
                        ruleIdx,
                        portIdx,
                        field,
                        value,
                      )}
                  />
                ))}
              </div>
            </div>
          )}

          {/* Egress Rules */}
          {f.policyTypes.includes("Egress") && (
            <div>
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  marginBottom: "10px",
                }}
              >
                <h3
                  style={{
                    margin: 0,
                    fontSize: "14px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  Egress Rules
                </h3>
                <button
                  type="button"
                  onClick={() => addRule("egressRules")}
                  style={{
                    fontSize: "12px",
                    color: "var(--accent)",
                    background: "transparent",
                    border: "none",
                    cursor: "pointer",
                    padding: 0,
                  }}
                >
                  + Add Egress Rule
                </button>
              </div>
              {f.egressRules.length === 0 && (
                <p
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    fontStyle: "italic",
                  }}
                >
                  No rules — all egress traffic is denied by default.
                </p>
              )}
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                {f.egressRules.map((rule, ruleIdx) => (
                  <RuleEditor
                    key={ruleIdx}
                    ruleIdx={ruleIdx}
                    rule={rule}
                    direction="Egress"
                    errors={errors.value}
                    errorPrefix={`egress[${ruleIdx}]`}
                    onRemove={() => removeRule("egressRules", ruleIdx)}
                    onAddPeer={() => addPeer("egressRules", ruleIdx)}
                    onRemovePeer={(peerIdx) =>
                      removePeer("egressRules", ruleIdx, peerIdx)}
                    onUpdatePeer={(peerIdx, updater) =>
                      updatePeer("egressRules", ruleIdx, peerIdx, updater)}
                    onAddPort={() => addPort("egressRules", ruleIdx)}
                    onRemovePort={(portIdx) =>
                      removePort("egressRules", ruleIdx, portIdx)}
                    onUpdatePort={(portIdx, field, value) =>
                      updatePort(
                        "egressRules",
                        ruleIdx,
                        portIdx,
                        field,
                        value,
                      )}
                  />
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Step 2: Review */}
      {currentStep.value === 2 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => {
            previewYaml.value = v;
          }}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath="/networking/networkpolicies"
        />
      )}
    </WizardShell>
  );
}

// --- RuleEditor sub-component ---

interface RuleEditorProps {
  ruleIdx: number;
  rule: NPRuleState;
  direction: "Ingress" | "Egress";
  errors: Record<string, string>;
  errorPrefix: string;
  onRemove: () => void;
  onAddPeer: () => void;
  onRemovePeer: (peerIdx: number) => void;
  onUpdatePeer: (
    peerIdx: number,
    updater: (peer: NPPeerState) => NPPeerState,
  ) => void;
  onAddPort: () => void;
  onRemovePort: (portIdx: number) => void;
  onUpdatePort: (
    portIdx: number,
    field: keyof NPPortState,
    value: unknown,
  ) => void;
}

function RuleEditor({
  ruleIdx,
  rule,
  direction,
  errors,
  errorPrefix,
  onRemove,
  onAddPeer,
  onRemovePeer,
  onUpdatePeer,
  onAddPort,
  onRemovePort,
  onUpdatePort,
}: RuleEditorProps) {
  const peerLabel = direction === "Ingress" ? "From" : "To";

  return (
    <div
      style={{
        border: "1px solid var(--border-subtle)",
        borderRadius: "10px",
        padding: "14px",
        display: "flex",
        flexDirection: "column",
        gap: "14px",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <span
          style={{
            fontSize: "12.5px",
            fontWeight: 600,
            color: "var(--text-secondary)",
          }}
        >
          {direction} Rule {ruleIdx + 1}
        </span>
        <button
          type="button"
          onClick={onRemove}
          style={{
            fontSize: "11px",
            color: "var(--danger)",
            background: "transparent",
            border: "none",
            cursor: "pointer",
            padding: 0,
          }}
        >
          Remove Rule
        </button>
      </div>

      {/* Peers */}
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
            {peerLabel} (Peers)
          </label>
          <button
            type="button"
            onClick={onAddPeer}
            style={{
              fontSize: "11px",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
            }}
          >
            + Add Peer
          </button>
        </div>
        {rule.peers.length === 0 && (
          <p
            style={{
              fontSize: "11px",
              color: "var(--text-muted)",
              fontStyle: "italic",
            }}
          >
            No peers — matches all{" "}
            {direction === "Ingress" ? "sources" : "destinations"}.
          </p>
        )}
        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          {rule.peers.map((peer, peerIdx) => (
            <PeerEditor
              key={peerIdx}
              peer={peer}
              errors={errors}
              errorPrefix={`${errorPrefix}.peers[${peerIdx}]`}
              onRemove={() => onRemovePeer(peerIdx)}
              onUpdate={(updater) => onUpdatePeer(peerIdx, updater)}
            />
          ))}
        </div>
      </div>

      {/* Ports */}
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
            Ports
          </label>
          <button
            type="button"
            onClick={onAddPort}
            style={{
              fontSize: "11px",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
            }}
          >
            + Add Port
          </button>
        </div>
        {rule.ports.length === 0 && (
          <p
            style={{
              fontSize: "11px",
              color: "var(--text-muted)",
              fontStyle: "italic",
            }}
          >
            No ports — all ports allowed.
          </p>
        )}
        <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
          {rule.ports.map((p, portIdx) => (
            <div
              key={portIdx}
              style={{ display: "flex", gap: "8px", alignItems: "center" }}
            >
              <input
                type="number"
                value={p.port === 0 ? "" : p.port}
                onInput={(e) =>
                  onUpdatePort(
                    portIdx,
                    "port",
                    parseInt((e.target as HTMLInputElement).value, 10) || 0,
                  )}
                placeholder="Port (e.g. 80)"
                min={1}
                max={65535}
                class={WIZARD_INPUT_CLASS}
                style={{ width: "130px" }}
              />
              <select
                value={p.protocol}
                onChange={(e) =>
                  onUpdatePort(
                    portIdx,
                    "protocol",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
                style={{ width: "90px" }}
              >
                {PROTOCOL_OPTIONS.map((proto) => (
                  <option key={proto} value={proto}>{proto}</option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => onRemovePort(portIdx)}
                style={{
                  fontSize: "13px",
                  color: "var(--danger)",
                  background: "transparent",
                  border: "none",
                  cursor: "pointer",
                  flexShrink: 0,
                }}
              >
                ×
              </button>
              {errors[`${errorPrefix}.ports[${portIdx}].port`] && (
                <p
                  style={{ fontSize: "10px", color: "var(--danger)" }}
                >
                  {errors[`${errorPrefix}.ports[${portIdx}].port`]}
                </p>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// --- PeerEditor sub-component ---

interface PeerEditorProps {
  peer: NPPeerState;
  errors: Record<string, string>;
  errorPrefix: string;
  onRemove: () => void;
  onUpdate: (updater: (peer: NPPeerState) => NPPeerState) => void;
}

function PeerEditor({
  peer,
  errors,
  errorPrefix,
  onRemove,
  onUpdate,
}: PeerEditorProps) {
  const addLabel = useCallback(() => {
    onUpdate((p) => ({ ...p, labels: [...p.labels, newLabelEntry()] }));
  }, [onUpdate]);

  const removeLabel = useCallback(
    (idx: number) => {
      onUpdate((p) => ({ ...p, labels: p.labels.filter((_, i) => i !== idx) }));
    },
    [onUpdate],
  );

  const updateLabel = useCallback(
    (idx: number, field: "key" | "value", value: string) => {
      onUpdate((p) => {
        const labels = [...p.labels];
        labels[idx] = { ...labels[idx], [field]: value };
        return { ...p, labels };
      });
    },
    [onUpdate],
  );

  const addExcept = useCallback(() => {
    onUpdate((p) => ({ ...p, except: [...p.except, ""] }));
  }, [onUpdate]);

  const removeExcept = useCallback(
    (idx: number) => {
      onUpdate((p) => ({ ...p, except: p.except.filter((_, i) => i !== idx) }));
    },
    [onUpdate],
  );

  const updateExcept = useCallback(
    (idx: number, value: string) => {
      onUpdate((p) => {
        const except = [...p.except];
        except[idx] = value;
        return { ...p, except };
      });
    },
    [onUpdate],
  );

  return (
    <div
      style={{
        background: "var(--bg-elevated)",
        borderRadius: "8px",
        padding: "10px",
        display: "flex",
        flexDirection: "column",
        gap: "8px",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <select
          value={peer.type}
          onChange={(e) =>
            onUpdate((p) => ({
              ...p,
              type: (e.target as HTMLSelectElement)
                .value as NPPeerState["type"],
              labels: [newLabelEntry()],
              cidr: "",
              except: [],
            }))}
          class={WIZARD_INPUT_CLASS}
          style={{ width: "180px" }}
        >
          {PEER_TYPE_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
        <button
          type="button"
          onClick={onRemove}
          style={{
            fontSize: "11px",
            color: "var(--danger)",
            background: "transparent",
            border: "none",
            cursor: "pointer",
            padding: 0,
          }}
        >
          Remove
        </button>
      </div>

      {/* Label selectors */}
      {(peer.type === "podSelector" || peer.type === "namespaceSelector") && (
        <div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              marginBottom: "5px",
            }}
          >
            <span
              style={{ fontSize: "10px", color: "var(--text-muted)" }}
            >
              {peer.type === "podSelector" ? "Pod" : "Namespace"} Labels
            </span>
            <button
              type="button"
              onClick={addLabel}
              style={{
                fontSize: "10px",
                color: "var(--accent)",
                background: "transparent",
                border: "none",
                cursor: "pointer",
                padding: 0,
              }}
            >
              + Add Label
            </button>
          </div>
          {peer.labels.length === 0 && (
            <p
              style={{
                fontSize: "10px",
                color: "var(--text-muted)",
                fontStyle: "italic",
              }}
            >
              No labels — matches all.
            </p>
          )}
          <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
            {peer.labels.map((label, idx) => (
              <div
                key={idx}
                style={{ display: "flex", gap: "6px", alignItems: "center" }}
              >
                <input
                  type="text"
                  value={label.key}
                  onInput={(e) =>
                    updateLabel(
                      idx,
                      "key",
                      (e.target as HTMLInputElement).value,
                    )}
                  placeholder="key"
                  class={WIZARD_INPUT_CLASS}
                  style={{ flex: 1 }}
                />
                <span
                  style={{ fontSize: "11px", color: "var(--text-muted)" }}
                >
                  =
                </span>
                <input
                  type="text"
                  value={label.value}
                  onInput={(e) =>
                    updateLabel(
                      idx,
                      "value",
                      (e.target as HTMLInputElement).value,
                    )}
                  placeholder="value"
                  class={WIZARD_INPUT_CLASS}
                  style={{ flex: 1 }}
                />
                <button
                  type="button"
                  onClick={() => removeLabel(idx)}
                  style={{
                    fontSize: "13px",
                    color: "var(--danger)",
                    background: "transparent",
                    border: "none",
                    cursor: "pointer",
                    flexShrink: 0,
                  }}
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* IP Block */}
      {peer.type === "ipBlock" && (
        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          <div>
            <label
              style={{
                display: "block",
                fontSize: "10px",
                color: "var(--text-muted)",
                marginBottom: "3px",
              }}
            >
              CIDR
            </label>
            <input
              type="text"
              value={peer.cidr}
              onInput={(e) =>
                onUpdate((p) => ({
                  ...p,
                  cidr: (e.target as HTMLInputElement).value,
                }))}
              placeholder="10.0.0.0/8"
              class={WIZARD_INPUT_CLASS}
            />
            {errors[`${errorPrefix}.cidr`] && (
              <p
                style={{
                  marginTop: "3px",
                  fontSize: "10px",
                  color: "var(--danger)",
                }}
              >
                {errors[`${errorPrefix}.cidr`]}
              </p>
            )}
          </div>
          <div>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                marginBottom: "4px",
              }}
            >
              <label
                style={{ fontSize: "10px", color: "var(--text-muted)" }}
              >
                Except CIDRs
              </label>
              <button
                type="button"
                onClick={addExcept}
                style={{
                  fontSize: "10px",
                  color: "var(--accent)",
                  background: "transparent",
                  border: "none",
                  cursor: "pointer",
                  padding: 0,
                }}
              >
                + Add Except
              </button>
            </div>
            <div
              style={{ display: "flex", flexDirection: "column", gap: "4px" }}
            >
              {peer.except.map((ex, idx) => (
                <div
                  key={idx}
                  style={{ display: "flex", gap: "6px", alignItems: "center" }}
                >
                  <input
                    type="text"
                    value={ex}
                    onInput={(e) =>
                      updateExcept(
                        idx,
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="192.168.0.0/24"
                    class={WIZARD_INPUT_CLASS}
                    style={{ flex: 1 }}
                  />
                  <button
                    type="button"
                    onClick={() => removeExcept(idx)}
                    style={{
                      fontSize: "13px",
                      color: "var(--danger)",
                      background: "transparent",
                      border: "none",
                      cursor: "pointer",
                      flexShrink: 0,
                    }}
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
