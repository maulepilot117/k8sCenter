// Mobile-side wrapper over the backend diagnostics API
// (`/v1/diagnostics/{namespace}/{kind}/{name}` and
// `/v1/diagnostics/{namespace}/summary`).
//
// Wire shape (mirrors `backend/internal/diagnostics/handler.go`):
//
//   {
//     "data": {
//       "target":      {kind, name, namespace},
//       "results":     [{ruleName, status, severity, message,
//                        detail?, remediation?, links?}],
//       "blastRadius": {directlyAffected:    [{kind, name, health, impact}],
//                       potentiallyAffected: [{kind, name, health, impact}]}
//     }
//   }
//
// The backend enforces a 15-second context timeout on the whole
// diagnostics + topology build, RBAC-gates on `list` for the target
// kind, and rejects kinds outside `kindToResource` with HTTP 400.
//
// Mobile-side responsibilities here:
//   * Decode the envelope into typed records.
//   * Surface ApiError on 4xx/5xx so the controller can humanise.
//   * Honour `clusterIdOverride` so cluster pinning matches the rest of
//     the per-domain repo stack (PR-3c discipline).
//   * Pass `CancelToken` through so disposal cancels the in-flight HTTP
//     cleanly — the 15s server-side timeout means a stranded request
//     blocks an entire backend worker until it fires.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api_error.dart';
import 'dio_client.dart';

/// Kinds the backend's diagnostics + blast-radius pipeline supports.
/// Mirrors `kindToResource` in `backend/internal/diagnostics/handler.go`
/// — extending mobile's set without a matching backend change yields
/// HTTP 400 "unsupported resource kind", which renders as an error card
/// rather than the Diagnose entry simply hiding. Keep this list in lock
/// step with the backend map.
const Set<String> kDiagnosticsKinds = {
  'Deployment',
  'StatefulSet',
  'DaemonSet',
  'Pod',
  'Service',
  'PersistentVolumeClaim',
};

/// Composite key for the diagnostics controller family. Carries the
/// cluster id so the controller's autoDispose cache slot can't reuse
/// the prior cluster's response after a cluster switch.
class DiagnosticTarget {
  const DiagnosticTarget({
    required this.clusterId,
    required this.namespace,
    required this.kind,
    required this.name,
  });

  final String clusterId;
  final String namespace;

  /// Canonical Kubernetes Kind (e.g. `Pod`, `PersistentVolumeClaim`).
  /// Matches `ResourceDetailScaffold.kindLabel` exactly.
  final String kind;

  final String name;

  @override
  bool operator ==(Object other) =>
      other is DiagnosticTarget &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.kind == kind &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, kind, name);
}

/// One row in the rules-engine output. `status` is one of
/// `pass | warn | fail`; `severity` is one of `critical | warning | info`.
class DiagnosticResult {
  const DiagnosticResult({
    required this.ruleName,
    required this.status,
    required this.severity,
    required this.message,
    this.detail,
    this.remediation,
    this.links = const <DiagnosticLink>[],
  });

  final String ruleName;
  final String status;
  final String severity;
  final String message;
  final String? detail;
  final String? remediation;
  final List<DiagnosticLink> links;

  bool get isFailed => status == 'fail' || status == 'warn';
  bool get isPassed => status == 'pass';

  factory DiagnosticResult.fromJson(Map<String, dynamic> json) {
    final rawLinks = json['links'];
    return DiagnosticResult(
      ruleName: json['ruleName'] as String? ?? '',
      status: json['status'] as String? ?? '',
      severity: json['severity'] as String? ?? 'info',
      message: json['message'] as String? ?? '',
      detail: json['detail'] as String?,
      remediation: json['remediation'] as String?,
      links: rawLinks is List
          ? rawLinks
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => DiagnosticLink.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <DiagnosticLink>[],
    );
  }
}

/// Linked-resource chip on a failed diagnostic row. `kind` is the
/// canonical k8s Kind so `kindDetailPath` can route correctly.
class DiagnosticLink {
  const DiagnosticLink({
    required this.label,
    required this.kind,
    required this.name,
  });

  final String label;
  final String kind;
  final String name;

  factory DiagnosticLink.fromJson(Map<String, dynamic> json) {
    return DiagnosticLink(
      label: json['label'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
    );
  }
}

/// One row in either blast-radius section.
class AffectedResource {
  const AffectedResource({
    required this.kind,
    required this.name,
    required this.health,
    required this.impact,
  });

  final String kind;
  final String name;

  /// One of `healthy | degraded | failing | unknown` (backend uses the
  /// topology graph's `Health` enum and stringifies it lower-case). Any
  /// unrecognized value falls through to the muted text colour.
  final String health;

  /// Human-readable impact string (e.g. "Selected resource — traffic may
  /// be affected"). Sourced from `impactDescription` in
  /// `backend/internal/diagnostics/blast.go`.
  final String impact;

  factory AffectedResource.fromJson(Map<String, dynamic> json) {
    return AffectedResource(
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      health: json['health'] as String? ?? 'unknown',
      impact: json['impact'] as String? ?? '',
    );
  }
}

/// Backend's BlastResult envelope. Both fields are always present —
/// empty lists rather than null — when the topology build succeeds.
/// When the build itself fails, the backend substitutes an empty pair
/// rather than omitting the field, so callers never need to null-check.
class BlastRadius {
  const BlastRadius({
    required this.directlyAffected,
    required this.potentiallyAffected,
  });

  final List<AffectedResource> directlyAffected;
  final List<AffectedResource> potentiallyAffected;

  bool get isEmpty =>
      directlyAffected.isEmpty && potentiallyAffected.isEmpty;

  factory BlastRadius.fromJson(Map<String, dynamic> json) {
    List<AffectedResource> decode(Object? raw) {
      if (raw is! List) return const <AffectedResource>[];
      return raw
          .whereType<Map<dynamic, dynamic>>()
          .map((m) => AffectedResource.fromJson(Map<String, dynamic>.from(m)))
          .toList();
    }

    return BlastRadius(
      directlyAffected: decode(json['directlyAffected']),
      potentiallyAffected: decode(json['potentiallyAffected']),
    );
  }

  static const empty = BlastRadius(
    directlyAffected: <AffectedResource>[],
    potentiallyAffected: <AffectedResource>[],
  );
}

/// Combined diagnostics + blast radius response. The target tuple is
/// echoed back from the request path; mobile uses it to defend against
/// any future re-issue / redirect surprise (response target should
/// match the requested key).
class DiagnosticResponse {
  const DiagnosticResponse({
    required this.target,
    required this.results,
    required this.blastRadius,
  });

  final DiagnosticTargetEcho target;
  final List<DiagnosticResult> results;
  final BlastRadius blastRadius;

  /// Failed rules are surfaced expanded; passed rules are collapsed
  /// behind a counter. Splitting once at parse time so widget code
  /// stays free of `.where(...)` calls in every render.
  List<DiagnosticResult> get failedResults =>
      results.where((r) => r.isFailed).toList();

  List<DiagnosticResult> get passedResults =>
      results.where((r) => r.isPassed).toList();

  factory DiagnosticResponse.fromJson(Map<String, dynamic> json) {
    final rawResults = json['results'];
    final blast = json['blastRadius'];
    return DiagnosticResponse(
      target: DiagnosticTargetEcho.fromJson(
        json['target'] is Map
            ? Map<String, dynamic>.from(json['target'] as Map)
            : const <String, dynamic>{},
      ),
      results: rawResults is List
          ? rawResults
              .whereType<Map<dynamic, dynamic>>()
              .map((m) =>
                  DiagnosticResult.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <DiagnosticResult>[],
      blastRadius: blast is Map
          ? BlastRadius.fromJson(Map<String, dynamic>.from(blast))
          : BlastRadius.empty,
    );
  }
}

/// Plain-data echo of the target tuple from the response envelope.
/// Kept separate from [DiagnosticTarget] so the controller's family key
/// doesn't accidentally include `clusterId` in the wire response shape.
class DiagnosticTargetEcho {
  const DiagnosticTargetEcho({
    required this.kind,
    required this.name,
    required this.namespace,
  });

  final String kind;
  final String name;
  final String namespace;

  factory DiagnosticTargetEcho.fromJson(Map<String, dynamic> json) {
    return DiagnosticTargetEcho(
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
    );
  }
}

/// One row in the namespace-summary response. Backend currently only
/// surfaces pods (per `handler.go::HandleNamespaceSummary`) so `kind`
/// is always `"Pod"`. Kept polymorphic in the type so a future backend
/// expansion (Deployments stuck rolling out, PVCs pending bind) doesn't
/// require a wire-format break.
class FailingResource {
  const FailingResource({
    required this.kind,
    required this.name,
    required this.reason,
  });

  final String kind;
  final String name;
  final String reason;

  factory FailingResource.fromJson(Map<String, dynamic> json) {
    return FailingResource(
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      reason: json['reason'] as String? ?? '',
    );
  }
}

/// Namespace-summary envelope. `total` is the total pod count in the
/// namespace (not the failing count) — `failing.length / total` is the
/// expected denominator for any "X of Y pods failing" header.
class NamespaceSummary {
  const NamespaceSummary({
    required this.failing,
    required this.total,
  });

  final List<FailingResource> failing;
  final int total;

  bool get isHealthy => failing.isEmpty;

  factory NamespaceSummary.fromJson(Map<String, dynamic> json) {
    final rawFailing = json['failing'];
    return NamespaceSummary(
      failing: rawFailing is List
          ? rawFailing
              .whereType<Map<dynamic, dynamic>>()
              .map((m) =>
                  FailingResource.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <FailingResource>[],
      total: (json['total'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Mobile wrapper over the backend diagnostics API surface. Stateless;
/// cluster pinning threads through explicit `clusterIdOverride` so the
/// `X-Cluster-ID` header on the wire always matches the family-key
/// slot the controller writes back into.
class DiagnosticsRepository {
  DiagnosticsRepository(this._dio);

  final Dio _dio;

  /// Loads the rules-engine + blast-radius response for a single
  /// target. Mobile clamps the client-side receive timeout to 30s so
  /// the 15s server-side context timeout always fires first — any
  /// receive-timeout here would imply a network stall outside the
  /// backend's control.
  Future<DiagnosticResponse> loadDiagnostics({
    required String namespace,
    required String kind,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    if (!kDiagnosticsKinds.contains(kind)) {
      // Defensive — the entry-point button gates on this set already.
      // Surfacing as an ApiError keeps the controller's error path
      // unified with the backend's HTTP 400 for the same condition.
      throw ApiError(
        statusCode: 400,
        code: 400,
        message: 'Diagnostics is not supported for $kind.',
      );
    }
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/diagnostics/${Uri.encodeComponent(namespace)}/'
        '${Uri.encodeComponent(kind)}/${Uri.encodeComponent(name)}',
        options: Options(
          receiveTimeout: const Duration(seconds: 30),
          headers: clusterIdOverride == null
              ? null
              : {'X-Cluster-ID': clusterIdOverride},
        ),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return DiagnosticResponse.fromJson(Map<String, dynamic>.from(data));
      }
      // Empty envelope — surfaces as "all checks passed, empty blast
      // radius" which is the same shape the backend emits for a
      // perfectly healthy target.
      return DiagnosticResponse(
        target: DiagnosticTargetEcho(
          kind: kind,
          name: name,
          namespace: namespace,
        ),
        results: const <DiagnosticResult>[],
        blastRadius: BlastRadius.empty,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Loads the namespace-level "failing pods" summary.
  Future<NamespaceSummary> namespaceSummary({
    required String namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/diagnostics/${Uri.encodeComponent(namespace)}/summary',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return NamespaceSummary.fromJson(Map<String, dynamic>.from(data));
      }
      return const NamespaceSummary(failing: <FailingResource>[], total: 0);
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final diagnosticsRepositoryProvider = Provider<DiagnosticsRepository>((ref) {
  return DiagnosticsRepository(ref.watch(dioProvider));
});
