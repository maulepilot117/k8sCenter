import type {
 K8sResource,
 NetworkPolicy,
 NetworkPolicyPeer,
 NetworkPolicyPort,
 NetworkPolicyRule,
} from"@/lib/k8s-types.ts";
import { Field, SectionHeader } from"@/components/ui/Field.tsx";
import { KeyValueTable } from"./KeyValueTable.tsx";

function formatPort(p: NetworkPolicyPort): string {
 const proto = p.protocol ??"TCP";
 if (!p.port) return proto;
 if (p.endPort) return `${p.port}-${p.endPort}/${proto}`;
 return `${p.port}/${proto}`;
}

function formatPorts(ports?: NetworkPolicyPort[]): string {
 if (!ports || ports.length === 0) return"All ports";
 return ports.map(formatPort).join(",");
}

function PeerBadge({ peer }: { peer: NetworkPolicyPeer }) {
 if (peer.ipBlock) {
 return (
 <span class="inline-flex items-center gap-1 rounded bg-purple-100 bg-accent-dim px-2 py-0.5 text-xs font-mono text-purple-800 text-accent-secondary">
 CIDR: {peer.ipBlock.cidr}
 {peer.ipBlock.except && peer.ipBlock.except.length > 0 && (
 <span class="text-purple-500">
 {` (except ${peer.ipBlock.except.join(",")})`}
 </span>
 )}
 </span>
 );
 }

 const parts: string[] = [];

 if (peer.namespaceSelector) {
 const labels = peer.namespaceSelector.matchLabels;
 if (!labels || Object.keys(labels).length === 0) {
 parts.push("all namespaces");
 } else {
 parts.push(
"ns:" +
 Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(","),
 );
 }
 }

 if (peer.podSelector) {
 const labels = peer.podSelector.matchLabels;
 if (!labels || Object.keys(labels).length === 0) {
 parts.push("all pods");
 } else {
 parts.push(
 Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(","),
 );
 }
 }

 if (parts.length === 0) return <span class="text-xs text-text-muted">-</span>;

 return (
 <span class="inline-flex items-center rounded bg-blue-100 bg-accent-dim px-2 py-0.5 text-xs text-accent">
 {parts.join(" /")}
 </span>
 );
}

function RuleRow(
 { rule, direction }: {
 rule: NetworkPolicyRule;
 direction:"ingress" |"egress";
 },
) {
 const peers = direction ==="ingress" ? rule.from : rule.to;
 const hasNoPeers = !peers || peers.length === 0;

 return (
 <tr class="border-b border-border-primary last:border-0">
 <td class="py-2 px-3 text-xs text-text-secondary align-top">
 {hasNoPeers
 ? (
 <span class="inline-flex items-center rounded bg-amber-100 bg-warning-dim px-2 py-0.5 text-xs text-amber-800 text-warning">
 {direction ==="ingress" ?"Any source" :"Any destination"}
 </span>
 )
 : (
 <div class="flex flex-wrap gap-1">
 {peers.map((peer, i) => <PeerBadge key={i} peer={peer} />)}
 </div>
 )}
 </td>
 <td class="py-2 px-3 text-xs font-mono text-text-secondary align-top">
 {formatPorts(rule.ports)}
 </td>
 </tr>
 );
}

function RulesSection(
 { rules, direction }: {
 rules: NetworkPolicyRule[];
 direction:"ingress" |"egress";
 },
) {
 const icon = direction ==="ingress" ?"\u2192" :"\u2190";
 const label = direction ==="ingress" ?"Ingress" :"Egress";
 const color = direction ==="ingress"
 ?"text-success"
 :"text-orange-600 text-warning";

 return (
 <div>
 <SectionHeader>
 <span class={color}>{icon}</span> {label} Rules ({rules.length})
 </SectionHeader>
 {rules.length === 0
 ? (
 <p class="text-xs text-text-muted italic">
 No {direction} rules — all {direction} traffic is blocked
 </p>
 )
 : (
 <div class="overflow-x-auto rounded-md border border-border-primary">
 <table class="w-full text-left">
 <thead>
 <tr class="bg-surface/50">
 <th class="py-1.5 px-3 text-xs font-medium text-text-muted">
 {direction ==="ingress" ?"From" :"To"}
 </th>
 <th class="py-1.5 px-3 text-xs font-medium text-text-muted">
 Ports
 </th>
 </tr>
 </thead>
 <tbody>
 {rules.map((rule, i) => (
 <RuleRow key={i} rule={rule} direction={direction} />
 ))}
 </tbody>
 </table>
 </div>
 )}
 </div>
 );
}

export function NetworkPolicyOverview(
 { resource }: { resource: K8sResource },
) {
 const np = resource as NetworkPolicy;
 const spec = np.spec;
 const policyTypes = spec?.policyTypes ?? ["Ingress"];
 const hasIngress = policyTypes.includes("Ingress");
 const hasEgress = policyTypes.includes("Egress");

 return (
 <div class="space-y-5">
 {/* Summary */}
 <div>
 <SectionHeader>Summary</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
 <Field
 label="Policy Types"
 value={policyTypes.join(",")}
 />
 <Field
 label="Ingress Rules"
 value={String(spec?.ingress?.length ?? 0)}
 />
 <Field
 label="Egress Rules"
 value={String(spec?.egress?.length ?? 0)}
 />
 </div>
 </div>

 {/* Pod Selector */}
 <div>
 <SectionHeader>Applies To</SectionHeader>
 {spec?.podSelector?.matchLabels &&
 Object.keys(spec.podSelector.matchLabels).length > 0
 ? (
 <KeyValueTable
 title="Pod Selector"
 data={spec.podSelector.matchLabels}
 />
 )
 : (
 <p class="text-xs text-text-muted italic">
 All pods in namespace (empty selector)
 </p>
 )}
 </div>

 {/* Ingress Rules */}
 {hasIngress && (
 <RulesSection
 rules={spec?.ingress ?? []}
 direction="ingress"
 />
 )}

 {/* Egress Rules */}
 {hasEgress && (
 <RulesSection
 rules={spec?.egress ?? []}
 direction="egress"
 />
 )}
 </div>
 );
}
