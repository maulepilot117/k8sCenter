// Engine auto-detect for the Policy wizard. Wraps the M4 observatory's
// `policyStatusProvider` rather than re-fetching `/v1/policies/status`
// independently — one HTTP round-trip per cluster serves both the
// observatory dashboard and the wizard. Mirrors
// `frontend/islands/PolicyWizard.tsx`'s engine bootstrap.
//
// The wizard renders an EmptyState when neither engine is detected and
// no fallback list — the registry is server-driven.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/policy_repository.dart';

/// Wizard-facing projection over [PolicyDiscoveryStatus]. The wizard
/// only consumes `availableEngines` to intersect against template
/// engines; the wider observatory fields (webhooks, namespace,
/// serviceUnavailable) are not relevant here, so the projection keeps
/// the wizard call sites unchanged while sharing the underlying fetch.
class PolicyEngineStatus {
  const PolicyEngineStatus({
    required this.detected,
    required this.kyvernoAvailable,
    required this.gatekeeperAvailable,
  });

  /// Backwards-compat raw `detected` value: "kyverno" | "gatekeeper" |
  /// "both" | "". Computed from the per-engine availability rather than
  /// surfaced from the backend's `detected` field — keeps the wizard's
  /// existing semantics intact even though the observatory's
  /// [PolicyDiscoveryStatus] models `detected` as a bool.
  final String detected;

  final bool kyvernoAvailable;
  final bool gatekeeperAvailable;

  /// Engines available to pick from in the wizard. Empty list means
  /// neither engine is installed — wizard renders an EmptyState. Order
  /// is informational only; engine resolution within
  /// `PolicyWizardController.pickTemplate` intersects this list with
  /// the template's supported engines and respects the operator's
  /// already-picked engine when applicable.
  List<String> get availableEngines {
    final out = <String>[];
    if (kyvernoAvailable) out.add('kyverno');
    if (gatekeeperAvailable) out.add('gatekeeper');
    return out;
  }

  factory PolicyEngineStatus.fromDiscovery(PolicyDiscoveryStatus s) {
    final String detected;
    if (s.kyvernoAvailable && s.gatekeeperAvailable) {
      detected = 'both';
    } else if (s.kyvernoAvailable) {
      detected = 'kyverno';
    } else if (s.gatekeeperAvailable) {
      detected = 'gatekeeper';
    } else {
      detected = '';
    }
    return PolicyEngineStatus(
      detected: detected,
      kyvernoAvailable: s.kyvernoAvailable,
      gatekeeperAvailable: s.gatekeeperAvailable,
    );
  }
}

/// Family key — clusterId pins the cache slot so a mid-flight cluster
/// switch can't redirect the read.
class PolicyEngineStatusKey {
  const PolicyEngineStatusKey({required this.clusterId});
  final String clusterId;

  @override
  bool operator ==(Object other) =>
      other is PolicyEngineStatusKey && other.clusterId == clusterId;

  @override
  int get hashCode => clusterId.hashCode;
}

/// Wizard-facing provider. Watches the observatory's
/// [policyStatusProvider] so both surfaces share the same fetch slot
/// per cluster — adding a second wizard surface no longer doubles the
/// HTTP cost.
final policyEngineStatusProvider = FutureProvider.autoDispose
    .family<PolicyEngineStatus, PolicyEngineStatusKey>((ref, key) async {
  final status = await ref.watch(policyStatusProvider(key.clusterId).future);
  return PolicyEngineStatus.fromDiscovery(status);
});
