import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  type LabelEntry,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

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

const STEPS = [
  { title: "Basics" },
  { title: "Rules" },
  { title: "Review" },
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
  return { type: "podSelector", labels: [newLabelEntry()], cidr: "", except: [] };
}

function newRule(): NPRuleState {
  return { peers: [], ports: [] };
}

function initialState(): NetworkPolicyFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
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

export default function NetworkPolicyWizard() {
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
      podSelectorLabels: form.value.podSelectorLabels.filter((_, i) => i !== idx),
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

  const removeRule = useCallback((ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
    dirty.value = true;
    form.value = {
      ...form.value,
      [ruleType]: form.value[ruleType].filter((_, i) => i !== ruleIdx),
    };
  }, []);

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

  // Peer helpers (parameterized by rule type)
  const addPeer = useCallback((ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
    updateRuleArray(ruleType, ruleIdx, (rule) => ({
      ...rule,
      peers: [...rule.peers, newPeer()],
    }));
  }, []);

  const removePeer = useCallback(
    (ruleType: "ingressRules" | "egressRules", ruleIdx: number, peerIdx: number) => {
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

  // Port helpers (parameterized by rule type)
  const addPort = useCallback((ruleType: "ingressRules" | "egressRules", ruleIdx: number) => {
    updateRuleArray(ruleType, ruleIdx, (rule) => ({
      ...rule,
      ports: [...rule.ports, newPort()],
    }));
  }, []);

  const removePort = useCallback(
    (ruleType: "ingressRules" | "egressRules", ruleIdx: number, portIdx: number) => {
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
        errs.name = "Must be lowercase alphanumeric with hyphens, 1-63 characters";
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
          rule.peers.forEach((peer, peerIdx) => {
            if (peer.type === "ipBlock" && (!peer.cidr || !CIDR_REGEX.test(peer.cidr))) {
              errs[`${direction}[${ruleIdx}].peers[${peerIdx}].cidr`] =
                "Must be a valid CIDR (e.g. 10.0.0.0/8)";
            }
            peer.except.forEach((exc, excIdx) => {
              if (exc && !CIDR_REGEX.test(exc)) {
                errs[`${direction}Rules[${ruleIdx}].peers[${peerIdx}].except[${excIdx}]`] =
                  "Must be a valid CIDR";
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

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  const f = form.value;

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create Network Policy
        </h1>
        <a
          href="/networking/networkpolicies"
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
        {/* Step 0: Basics */}
        {currentStep.value === 0 && (
          <div class="space-y-6 max-w-2xl">
            {/* Name */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name <span class="text-danger">*</span>
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
                <p class="text-sm text-danger">{errors.value.name}</p>
              )}
            </div>

            {/* Namespace */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Namespace <span class="text-danger">*</span>
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
                <p class="text-sm text-danger">{errors.value.namespace}</p>
              )}
            </div>

            {/* Pod Selector Labels */}
            <div>
              <div class="flex items-center justify-between mb-2">
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Pod Selector Labels
                  <span class="ml-1 text-xs font-normal text-slate-500">
                    (empty = select all pods)
                  </span>
                </label>
                <button
                  type="button"
                  onClick={addPodSelectorLabel}
                  class="text-xs text-brand hover:text-brand/80"
                >
                  + Add Label
                </button>
              </div>
              {f.podSelectorLabels.length === 0 && (
                <p class="text-xs text-slate-500 italic">
                  No labels — policy applies to all pods in the namespace.
                </p>
              )}
              <div class="space-y-2">
                {f.podSelectorLabels.map((label, idx) => (
                  <div key={idx} class="flex gap-2 items-center">
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
                      class={WIZARD_INPUT_CLASS + " flex-1"}
                    />
                    <span class="text-slate-500">=</span>
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
                      class={WIZARD_INPUT_CLASS + " flex-1"}
                    />
                    <button
                      type="button"
                      onClick={() => removePodSelectorLabel(idx)}
                      class="text-xs text-danger hover:text-danger/80 shrink-0"
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>
            </div>

            {/* Policy Types */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                Policy Types <span class="text-danger">*</span>
              </label>
              <div class="flex gap-6">
                {["Ingress", "Egress"].map((type) => (
                  <label key={type} class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={f.policyTypes.includes(type)}
                      onChange={(e) =>
                        togglePolicyType(
                          type,
                          (e.target as HTMLInputElement).checked,
                        )}
                      class="rounded border-slate-300 text-brand focus:ring-brand"
                    />
                    <span class="text-sm text-slate-700 dark:text-slate-300">
                      {type}
                    </span>
                  </label>
                ))}
              </div>
              {errors.value.policyTypes && (
                <p class="mt-1 text-sm text-danger">{errors.value.policyTypes}</p>
              )}
            </div>
          </div>
        )}

        {/* Step 1: Rules */}
        {currentStep.value === 1 && (
          <div class="space-y-8">
            {/* Ingress Rules */}
            {f.policyTypes.includes("Ingress") && (
              <div>
                <div class="flex items-center justify-between mb-3">
                  <h3 class="text-base font-semibold text-slate-800 dark:text-white">
                    Ingress Rules
                  </h3>
                  <Button variant="ghost" onClick={() => addRule("ingressRules")}>
                    + Add Ingress Rule
                  </Button>
                </div>
                {f.ingressRules.length === 0 && (
                  <p class="text-sm text-slate-500 italic">
                    No rules — all ingress traffic is denied by default.
                  </p>
                )}
                <div class="space-y-4">
                  {f.ingressRules.map((rule, ruleIdx) => (
                    <RuleEditor
                      key={ruleIdx}
                      ruleIdx={ruleIdx}
                      rule={rule}
                      direction="Ingress"
                      errors={errors.value}
                      errorPrefix={`ingress[${ruleIdx}]`}
                      onRemove={() => removeRule("ingressRules", ruleIdx)}
                      onAddPeer={() => addPeer("ingressRules", ruleIdx)}
                      onRemovePeer={(peerIdx) => removePeer("ingressRules", ruleIdx, peerIdx)}
                      onUpdatePeer={(peerIdx, updater) =>
                        updatePeer("ingressRules", ruleIdx, peerIdx, updater)}
                      onAddPort={() => addPort("ingressRules", ruleIdx)}
                      onRemovePort={(portIdx) => removePort("ingressRules", ruleIdx, portIdx)}
                      onUpdatePort={(portIdx, field, value) =>
                        updatePort("ingressRules", ruleIdx, portIdx, field, value)}
                    />
                  ))}
                </div>
              </div>
            )}

            {/* Egress Rules */}
            {f.policyTypes.includes("Egress") && (
              <div>
                <div class="flex items-center justify-between mb-3">
                  <h3 class="text-base font-semibold text-slate-800 dark:text-white">
                    Egress Rules
                  </h3>
                  <Button variant="ghost" onClick={() => addRule("egressRules")}>
                    + Add Egress Rule
                  </Button>
                </div>
                {f.egressRules.length === 0 && (
                  <p class="text-sm text-slate-500 italic">
                    No rules — all egress traffic is denied by default.
                  </p>
                )}
                <div class="space-y-4">
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
                      onRemovePeer={(peerIdx) => removePeer("egressRules", ruleIdx, peerIdx)}
                      onUpdatePeer={(peerIdx, updater) =>
                        updatePeer("egressRules", ruleIdx, peerIdx, updater)}
                      onAddPort={() => addPort("egressRules", ruleIdx)}
                      onRemovePort={(portIdx) => removePort("egressRules", ruleIdx, portIdx)}
                      onUpdatePort={(portIdx, field, value) =>
                        updatePort("egressRules", ruleIdx, portIdx, field, value)}
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
      </div>

      {/* Navigation */}
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
  onUpdatePeer: (peerIdx: number, updater: (peer: NPPeerState) => NPPeerState) => void;
  onAddPort: () => void;
  onRemovePort: (portIdx: number) => void;
  onUpdatePort: (portIdx: number, field: keyof NPPortState, value: unknown) => void;
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
    <div class="border border-slate-200 dark:border-slate-700 rounded-lg p-4 space-y-4">
      <div class="flex items-center justify-between">
        <span class="text-sm font-medium text-slate-600 dark:text-slate-400">
          {direction} Rule {ruleIdx + 1}
        </span>
        <button
          type="button"
          onClick={onRemove}
          class="text-xs text-danger hover:text-danger/80"
        >
          Remove Rule
        </button>
      </div>

      {/* Peers */}
      <div>
        <div class="flex items-center justify-between mb-2">
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">
            {peerLabel} (Peers)
          </label>
          <button
            type="button"
            onClick={onAddPeer}
            class="text-xs text-brand hover:text-brand/80"
          >
            + Add Peer
          </button>
        </div>
        {rule.peers.length === 0 && (
          <p class="text-xs text-slate-500 italic">
            No peers — matches all {direction === "Ingress" ? "sources" : "destinations"}.
          </p>
        )}
        <div class="space-y-3">
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
        <div class="flex items-center justify-between mb-2">
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">
            Ports
          </label>
          <button
            type="button"
            onClick={onAddPort}
            class="text-xs text-brand hover:text-brand/80"
          >
            + Add Port
          </button>
        </div>
        {rule.ports.length === 0 && (
          <p class="text-xs text-slate-500 italic">
            No ports — all ports allowed.
          </p>
        )}
        <div class="space-y-2">
          {rule.ports.map((p, portIdx) => (
            <div key={portIdx} class="flex gap-2 items-center">
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
                class={WIZARD_INPUT_CLASS + " w-40"}
              />
              <select
                value={p.protocol}
                onChange={(e) =>
                  onUpdatePort(
                    portIdx,
                    "protocol",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS + " w-28"}
              >
                {PROTOCOL_OPTIONS.map((proto) => (
                  <option key={proto} value={proto}>{proto}</option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => onRemovePort(portIdx)}
                class="text-xs text-danger hover:text-danger/80 shrink-0"
              >
                Remove
              </button>
              {errors[`${errorPrefix}.ports[${portIdx}].port`] && (
                <p class="text-xs text-danger">
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
    <div class="bg-slate-50 dark:bg-slate-800 rounded-md p-3 space-y-2">
      <div class="flex items-center justify-between">
        <select
          value={peer.type}
          onChange={(e) =>
            onUpdate((p) => ({
              ...p,
              type: (e.target as HTMLSelectElement).value as NPPeerState["type"],
              labels: [newLabelEntry()],
              cidr: "",
              except: [],
            }))}
          class={WIZARD_INPUT_CLASS + " w-48"}
        >
          {PEER_TYPE_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
        <button
          type="button"
          onClick={onRemove}
          class="text-xs text-danger hover:text-danger/80"
        >
          Remove
        </button>
      </div>

      {/* Label selectors */}
      {(peer.type === "podSelector" || peer.type === "namespaceSelector") && (
        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="text-xs text-slate-500">
              {peer.type === "podSelector" ? "Pod" : "Namespace"} Labels
            </span>
            <button
              type="button"
              onClick={addLabel}
              class="text-xs text-brand hover:text-brand/80"
            >
              + Add Label
            </button>
          </div>
          {peer.labels.length === 0 && (
            <p class="text-xs text-slate-500 italic">No labels — matches all.</p>
          )}
          <div class="space-y-1">
            {peer.labels.map((label, idx) => (
              <div key={idx} class="flex gap-2 items-center">
                <input
                  type="text"
                  value={label.key}
                  onInput={(e) =>
                    updateLabel(idx, "key", (e.target as HTMLInputElement).value)}
                  placeholder="key"
                  class={WIZARD_INPUT_CLASS + " flex-1"}
                />
                <span class="text-slate-500 text-xs">=</span>
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
                  class={WIZARD_INPUT_CLASS + " flex-1"}
                />
                <button
                  type="button"
                  onClick={() => removeLabel(idx)}
                  class="text-xs text-danger hover:text-danger/80 shrink-0"
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
        <div class="space-y-2">
          <div>
            <label class="block text-xs text-slate-500 mb-1">CIDR</label>
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
              <p class="mt-1 text-xs text-danger">
                {errors[`${errorPrefix}.cidr`]}
              </p>
            )}
          </div>
          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="text-xs text-slate-500">Except CIDRs</label>
              <button
                type="button"
                onClick={addExcept}
                class="text-xs text-brand hover:text-brand/80"
              >
                + Add Except
              </button>
            </div>
            <div class="space-y-1">
              {peer.except.map((ex, idx) => (
                <div key={idx} class="flex gap-2 items-center">
                  <input
                    type="text"
                    value={ex}
                    onInput={(e) =>
                      updateExcept(
                        idx,
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="192.168.0.0/24"
                    class={WIZARD_INPUT_CLASS + " flex-1"}
                  />
                  <button
                    type="button"
                    onClick={() => removeExcept(idx)}
                    class="text-xs text-danger hover:text-danger/80 shrink-0"
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
