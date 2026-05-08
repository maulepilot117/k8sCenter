// NetworkPolicy wizard controller. Mirrors
// `frontend/islands/NetworkPolicyWizard.tsx` and ports the wire
// contract from `backend/internal/wizard/networkpolicy.go:22`.
//
// Wire format (`NetworkPolicyInput`):
//   {
//     name, namespace,
//     podSelector: Map<String,String>,
//     policyTypes: [Ingress?, Egress?],
//     ingress?: [{from?: [peer...], ports?: [{port, protocol?}, ...]}, ...],
//     egress?:  [{to?:   [peer...], ports?: [{port, protocol?}, ...]}, ...]
//   }
//   peer = { podSelector?, namespaceSelector?, ipBlock?: {cidr, except?} }
//
// One Configure step + Review. Field paths surfaced by the backend
// (e.g. `policyTypes[N]`, `ingress[N].from[N].podSelector`,
// `ingress[N].ports[N].port`, `ingress[N].from[N].ipBlock.cidr`,
// `ingress[N].from[N].ipBlock.except[N]`) all route to step 0 — the
// only form step.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// Allowed protocols on a NetworkPolicy port. Backend defaults TCP
/// when empty; we surface the picker explicitly.
const List<String> kNetworkPolicyProtocols = ['TCP', 'UDP', 'SCTP'];

/// Three peer flavours the form supports — exactly one must be active
/// per peer row. The widget stores all three sub-records but only
/// emits the chosen one in `toJson`.
enum PeerKind { pod, namespaceSel, ipBlock }

class NetworkPolicyPort {
  const NetworkPolicyPort({this.port = 0, this.protocol = 'TCP'});

  final int port;
  final String protocol;

  NetworkPolicyPort copyWith({int? port, String? protocol}) =>
      NetworkPolicyPort(
        port: port ?? this.port,
        protocol: protocol ?? this.protocol,
      );

  bool get isEmpty => port == 0;

  Map<String, dynamic> toJson() => {
        'port': port,
        if (protocol.isNotEmpty) 'protocol': protocol,
      };
}

class NetworkPolicyPeer {
  const NetworkPolicyPeer({
    this.kind = PeerKind.pod,
    this.podSelector = const <KeyValuePair>[],
    this.namespaceSelector = const <KeyValuePair>[],
    this.cidr = '',
    this.except = const <String>[],
  });

  final PeerKind kind;
  final List<KeyValuePair> podSelector;
  final List<KeyValuePair> namespaceSelector;
  final String cidr;
  final List<String> except;

  NetworkPolicyPeer copyWith({
    PeerKind? kind,
    List<KeyValuePair>? podSelector,
    List<KeyValuePair>? namespaceSelector,
    String? cidr,
    List<String>? except,
  }) =>
      NetworkPolicyPeer(
        kind: kind ?? this.kind,
        podSelector: podSelector ?? this.podSelector,
        namespaceSelector: namespaceSelector ?? this.namespaceSelector,
        cidr: cidr ?? this.cidr,
        except: except ?? this.except,
      );

  Map<String, String> _asMap(List<KeyValuePair> kv) {
    final out = <String, String>{};
    for (final p in kv) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }

  Map<String, dynamic>? toJson() {
    switch (kind) {
      case PeerKind.pod:
        final m = _asMap(podSelector);
        if (m.isEmpty) return null;
        return {'podSelector': m};
      case PeerKind.namespaceSel:
        final m = _asMap(namespaceSelector);
        if (m.isEmpty) return null;
        return {'namespaceSelector': m};
      case PeerKind.ipBlock:
        if (cidr.isEmpty) return null;
        return {
          'ipBlock': {
            'cidr': cidr,
            if (except.isNotEmpty) 'except': except,
          },
        };
    }
  }
}

class NetworkPolicyRule {
  const NetworkPolicyRule({
    this.peers = const <NetworkPolicyPeer>[],
    this.ports = const <NetworkPolicyPort>[],
  });

  /// Peers — interpreted as `from` for ingress rules, `to` for egress
  /// rules. Same shape, different field name on the wire.
  final List<NetworkPolicyPeer> peers;
  final List<NetworkPolicyPort> ports;

  NetworkPolicyRule copyWith({
    List<NetworkPolicyPeer>? peers,
    List<NetworkPolicyPort>? ports,
  }) =>
      NetworkPolicyRule(
        peers: peers ?? this.peers,
        ports: ports ?? this.ports,
      );

  /// Returns null when the rule contributes nothing (no peers + no
  /// ports). Matching the web wizard's `omit empty rule` behaviour.
  Map<String, dynamic>? toJson({required bool isIngress}) {
    final emittedPeers = <Map<String, dynamic>>[];
    for (final p in peers) {
      final j = p.toJson();
      if (j != null) emittedPeers.add(j);
    }
    final emittedPorts = <Map<String, dynamic>>[];
    for (final p in ports) {
      if (!p.isEmpty) emittedPorts.add(p.toJson());
    }
    if (emittedPeers.isEmpty && emittedPorts.isEmpty) return null;
    return {
      if (emittedPeers.isNotEmpty)
        (isIngress ? 'from' : 'to'): emittedPeers,
      if (emittedPorts.isNotEmpty) 'ports': emittedPorts,
    };
  }
}

class NetworkPolicyForm {
  const NetworkPolicyForm({
    this.name = '',
    this.namespace = '',
    this.podSelector = const <KeyValuePair>[],
    this.includeIngress = true,
    this.includeEgress = false,
    this.ingress = const <NetworkPolicyRule>[],
    this.egress = const <NetworkPolicyRule>[],
  });

  final String name;
  final String namespace;
  final List<KeyValuePair> podSelector;
  final bool includeIngress;
  final bool includeEgress;
  final List<NetworkPolicyRule> ingress;
  final List<NetworkPolicyRule> egress;

  NetworkPolicyForm copyWith({
    String? name,
    String? namespace,
    List<KeyValuePair>? podSelector,
    bool? includeIngress,
    bool? includeEgress,
    List<NetworkPolicyRule>? ingress,
    List<NetworkPolicyRule>? egress,
  }) =>
      NetworkPolicyForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        podSelector: podSelector ?? this.podSelector,
        includeIngress: includeIngress ?? this.includeIngress,
        includeEgress: includeEgress ?? this.includeEgress,
        ingress: ingress ?? this.ingress,
        egress: egress ?? this.egress,
      );

  Map<String, String> selectorMap() {
    final out = <String, String>{};
    for (final p in podSelector) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class NetworkPolicyWizardController
    extends WizardController<NetworkPolicyForm> {
  @override
  String get wizardType => 'networkpolicy';

  @override
  String get resourceListKind => 'networkpolicies';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Selector, policy types, and rules',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  NetworkPolicyForm buildInitialForm() => const NetworkPolicyForm();

  @override
  Map<String, dynamic> toPreviewBody(NetworkPolicyForm form) {
    final policyTypes = <String>[
      if (form.includeIngress) 'Ingress',
      if (form.includeEgress) 'Egress',
    ];
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'podSelector': form.selectorMap(),
      'policyTypes': policyTypes,
    };
    if (form.includeIngress) {
      // Quarantine-style policy: policyTypes contains Ingress but no
      // rules — backend treats this as deny-all-ingress. Emit an empty
      // array so the field is present even when no rules are filled.
      final emitted = <Map<String, dynamic>>[];
      for (final r in form.ingress) {
        final j = r.toJson(isIngress: true);
        if (j != null) emitted.add(j);
      }
      body['ingress'] = emitted;
    }
    if (form.includeEgress) {
      final emitted = <Map<String, dynamic>>[];
      for (final r in form.egress) {
        final j = r.toJson(isIngress: false);
        if (j != null) emitted.add(j);
      }
      body['egress'] = emitted;
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    // All NetworkPolicy validation lives in the single Configure step.
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'podSelector' ||
        fieldPath == 'policyTypes' ||
        fieldPath.startsWith('podSelector') ||
        fieldPath.startsWith('policyTypes[') ||
        fieldPath.startsWith('ingress') ||
        fieldPath.startsWith('egress')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(NetworkPolicyForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (!form.includeIngress && !form.includeEgress) {
      out['policyTypes'] = 'At least one policy type is required';
    }
    return out;
  }
}

final networkPolicyWizardProvider = AutoDisposeNotifierProvider.family<
    NetworkPolicyWizardController,
    WizardState<NetworkPolicyForm>,
    WizardKey>(NetworkPolicyWizardController.new);
