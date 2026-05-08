// Engine auto-detect for the Policy wizard. Wraps `GET /v1/policies/status`.
// Mirrors `frontend/islands/PolicyWizard.tsx`'s engine bootstrap.
//
// Response shape (from backend/internal/policy/types.go EngineStatus):
//   {
//     detected: "kyverno" | "gatekeeper" | "both" | "",
//     kyverno?:    { available, namespace?, webhooks },
//     gatekeeper?: { available, namespace?, webhooks },
//     lastChecked: <RFC3339>,
//   }
//
// The wizard renders an EmptyState when neither engine is detected
// and no fallback list — the registry is server-driven.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/api_error.dart';
import '../../../api/dio_client.dart';

class PolicyEngineStatus {
  const PolicyEngineStatus({
    required this.detected,
    required this.kyvernoAvailable,
    required this.gatekeeperAvailable,
  });

  /// Raw `detected` value: "kyverno" | "gatekeeper" | "both" | "".
  final String detected;

  /// Convenience: was Kyverno discovered (either alone or as part of
  /// `both`)?
  final bool kyvernoAvailable;

  /// Convenience: was Gatekeeper discovered (either alone or as part
  /// of `both`)?
  final bool gatekeeperAvailable;

  /// Engines available to pick from in the wizard. Empty list means
  /// neither engine is installed — wizard renders an EmptyState.
  List<String> get availableEngines {
    final out = <String>[];
    if (kyvernoAvailable) out.add('kyverno');
    if (gatekeeperAvailable) out.add('gatekeeper');
    return out;
  }

  /// Convenience: the engine to default to. When both are present,
  /// pick Kyverno (matches web behavior — see PolicyWizard.tsx).
  String get defaultEngine {
    if (kyvernoAvailable) return 'kyverno';
    if (gatekeeperAvailable) return 'gatekeeper';
    return '';
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

final policyEngineStatusProvider = FutureProvider.autoDispose
    .family<PolicyEngineStatus, PolicyEngineStatusKey>((ref, key) async {
  final dio = ref.watch(dioProvider);
  try {
    final res = await dio.get<Map<String, dynamic>>(
      '/api/v1/policies/status',
      options: Options(headers: {'X-Cluster-ID': key.clusterId}),
    );
    final data = res.data?['data'];
    if (data is! Map<String, dynamic>) {
      return const PolicyEngineStatus(
        detected: '',
        kyvernoAvailable: false,
        gatekeeperAvailable: false,
      );
    }
    final detected = (data['detected'] as String?) ?? '';
    final kyvernoBlock = data['kyverno'] as Map<String, dynamic>?;
    final gkBlock = data['gatekeeper'] as Map<String, dynamic>?;
    final kyvernoAvail = kyvernoBlock != null &&
        kyvernoBlock['available'] == true;
    final gkAvail = gkBlock != null && gkBlock['available'] == true;
    // The `detected` field is informational; rely on per-engine
    // `available: true` for the actual decision so a future server
    // change to `detected` semantics doesn't cascade.
    return PolicyEngineStatus(
      detected: detected,
      kyvernoAvailable: kyvernoAvail,
      gatekeeperAvailable: gkAvail,
    );
  } on DioException catch (e) {
    final err = e.error;
    throw err is ApiError ? err : ApiError.fromDio(e);
  }
});
