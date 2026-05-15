// Mobile-side wrapper over the backend policy API at
// `/v1/policies/{status,(list),violations,compliance,compliance/history}`.
// Wire types mirror `backend/internal/policy/types.go` exactly.
// Web parallel: `frontend/islands/PolicyDashboard.tsx`,
// `frontend/components/k8s/ComplianceDashboard.tsx`,
// `frontend/components/k8s/ComplianceTrendChart.tsx`.
//
// Engine field semantics (open enum on the wire). Treated as a raw string
// so a future engine name (e.g., Kyverno-Constraint) doesn't crash the
// parser. UI rendering branches on the [PolicyEngine] enum, with
// [PolicyEngine.unknown] for unrecognised values.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards it
// as an explicit `X-Cluster-ID` header. The PR-3c interceptor invariant
// (only injects when absent) is the live contract — so the wire header
// always matches whichever family-key slot the caller writes back into.
//
// 503 distinguished-error path for `complianceHistory`: when the backend
// reports `compliance history requires a database` (PostgreSQL not
// configured), this is a permanent "feature not configured" state — UI
// must render a non-retryable empty state. Other 503s (network blips,
// rolling restarts) stay retry-able. The discriminator is the substring
// "requires a database" anywhere in the error message; see
// `backend/internal/policy/handler.go::HandleComplianceHistory` for the
// canonical wording.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

/// Policy engine. Open enum on the wire — older / future policy responses
/// may add engines, so [unknown] is the explicit fallback rather than
/// crashing the parser.
enum PolicyEngine { kyverno, gatekeeper, unknown }

PolicyEngine _engineFromJson(Object? v) {
  if (v is! String) return PolicyEngine.unknown;
  switch (v) {
    case 'kyverno':
      return PolicyEngine.kyverno;
    case 'gatekeeper':
      return PolicyEngine.gatekeeper;
    default:
      return PolicyEngine.unknown;
  }
}

String policyEngineLabel(PolicyEngine e) => switch (e) {
      PolicyEngine.kyverno => 'Kyverno',
      PolicyEngine.gatekeeper => 'Gatekeeper',
      PolicyEngine.unknown => 'Unknown',
    };

/// Severity weights mirror `backend/internal/policy/types.go::severityWeights`.
/// Used for client-side severity ordering when the list endpoint is consumed
/// without a re-sort (the backend sorts by severity weight on its side, but
/// filtered subsets re-sort on the client).
const Map<String, int> kPolicySeverityWeights = {
  'critical': 10,
  'high': 5,
  'medium': 2,
  'low': 1,
};

/// Default severity applied when a policy declares no severity. Mirrors
/// `defaultSeverity` in `backend/internal/policy/types.go`.
const String kPolicyDefaultSeverity = 'medium';

// ---------------------------------------------------------------------------
// Wire-format value types
// ---------------------------------------------------------------------------

/// Decoded `/v1/policies/status` response. Mirrors `EngineStatus` in
/// `backend/internal/policy/types.go`. Distinct from the wizard-side
/// [PolicyEngineStatus] in `mobile/lib/wizards/types/policy/` — that
/// shape is the wizard's bootstrap view; this is the observatory view
/// (same payload, slightly different downstream usage).
class PolicyDiscoveryStatus {
  const PolicyDiscoveryStatus({
    required this.detected,
    required this.kyvernoAvailable,
    required this.gatekeeperAvailable,
    this.kyvernoNamespace,
    this.gatekeeperNamespace,
    this.kyvernoWebhooks = 0,
    this.gatekeeperWebhooks = 0,
    this.lastChecked = '',
    this.serviceUnavailable = false,
  });

  /// True when at least one engine is detected. Drives
  /// `FeatureUnavailableState.policy()` rendering on every policy surface.
  final bool detected;

  final bool kyvernoAvailable;
  final bool gatekeeperAvailable;

  /// Namespace where Kyverno is installed. Admin-only — stripped to null
  /// for non-admin users by the backend handler. Used for "installed in
  /// kube-system" hint copy.
  final String? kyvernoNamespace;

  /// Namespace where Gatekeeper is installed. Same admin gating as
  /// [kyvernoNamespace].
  final String? gatekeeperNamespace;

  final int kyvernoWebhooks;
  final int gatekeeperWebhooks;

  /// RFC-3339 timestamp; empty when the first probe hasn't completed.
  final String lastChecked;

  /// True when the backend status endpoint returned 5xx — distinguishes
  /// "backend was unreachable" from "backend says no engine installed".
  /// Both flow through `detected: false` so the install-guidance UI
  /// renders; consumers that want to nudge toward retry can branch on
  /// this flag.
  final bool serviceUnavailable;

  factory PolicyDiscoveryStatus.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    int i(Object? v) => v is num ? v.toInt() : 0;
    final kyvernoBlock = json['kyverno'] as Map<String, dynamic>?;
    final gkBlock = json['gatekeeper'] as Map<String, dynamic>?;
    final kyvernoAvail = kyvernoBlock?['available'] == true;
    final gkAvail = gkBlock?['available'] == true;
    final detected = kyvernoAvail || gkAvail;
    return PolicyDiscoveryStatus(
      detected: detected,
      kyvernoAvailable: kyvernoAvail,
      gatekeeperAvailable: gkAvail,
      kyvernoNamespace: s(kyvernoBlock?['namespace']),
      gatekeeperNamespace: s(gkBlock?['namespace']),
      kyvernoWebhooks: i(kyvernoBlock?['webhooks']),
      gatekeeperWebhooks: i(gkBlock?['webhooks']),
      lastChecked: json['lastChecked'] as String? ?? '',
    );
  }

  static const empty = PolicyDiscoveryStatus(
    detected: false,
    kyvernoAvailable: false,
    gatekeeperAvailable: false,
  );

  static const unreachable = PolicyDiscoveryStatus(
    detected: false,
    kyvernoAvailable: false,
    gatekeeperAvailable: false,
    serviceUnavailable: true,
  );
}

/// One normalized policy row. Mirrors `NormalizedPolicy` in
/// `backend/internal/policy/types.go`.
///
/// Composite ID: `engine:namespace:kind:name` per the Key Conventions
/// section of CLAUDE.md. The backend's [id] field already encodes this,
/// but mobile derives + validates it via [PolicyId] so callers can drill
/// into the canonical pieces without re-parsing the wire format.
class PolicyItem {
  const PolicyItem({
    required this.id,
    required this.name,
    required this.namespace,
    required this.kind,
    required this.action,
    required this.severity,
    required this.engine,
    required this.blocking,
    required this.ready,
    required this.ruleCount,
    required this.violationCount,
    this.category,
    this.description,
    this.nativeAction,
    this.targetKinds = const [],
  });

  final String id;
  final String name;

  /// Namespaced policies have a value; cluster-scoped policies leave this
  /// empty (the backend's `omitempty` field unwinds to "").
  final String namespace;

  final String kind;

  /// Normalized action: typically `audit` / `enforce` / `warn`. Open enum
  /// on the wire — render verbatim, no parsing.
  final String action;

  /// One of `critical|high|medium|low` (lowercase per the backend weight
  /// table). Anything else falls through to the default
  /// [kPolicyDefaultSeverity] when computing weights.
  final String severity;

  final PolicyEngine engine;

  /// True when the policy blocks the admission request on violation
  /// (Kyverno `enforce`, Gatekeeper `deny`). False for audit-only.
  final bool blocking;

  /// True when the policy is currently active. False means the engine has
  /// reported it as not yet reconciled or as failing to compile.
  final bool ready;

  final int ruleCount;
  final int violationCount;

  final String? category;
  final String? description;
  final String? nativeAction;
  final List<String> targetKinds;

  factory PolicyItem.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    return PolicyItem(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      action: json['action'] as String? ?? '',
      severity: (json['severity'] as String? ?? kPolicyDefaultSeverity)
          .toLowerCase(),
      engine: _engineFromJson(json['engine']),
      blocking: json['blocking'] == true,
      ready: json['ready'] == true,
      ruleCount: i(json['ruleCount']),
      violationCount: i(json['violationCount']),
      category: json['category'] as String?,
      description: json['description'] as String?,
      nativeAction: json['nativeAction'] as String?,
      targetKinds: ((json['targetKinds'] as List?) ?? const [])
          .whereType<String>()
          .toList(),
    );
  }
}

/// One normalized violation row. Mirrors `NormalizedViolation` in
/// `backend/internal/policy/types.go`.
///
/// Wire shape has NO server-side `id` field — violations are derived
/// from controller events. Mobile constructs a stable key via
/// [stableKey] for `ListView.builder` and detail-screen lookup, since the
/// list endpoint is the only source of truth (no GET-by-id violation
/// endpoint exists; detail is a client-side filter of the list).
class PolicyViolation {
  const PolicyViolation({
    required this.policy,
    required this.severity,
    required this.action,
    required this.message,
    required this.kind,
    required this.name,
    required this.engine,
    required this.blocking,
    this.rule = '',
    this.namespace = '',
    this.timestamp = '',
  });

  /// MatchKey of the violating policy. Joins to [PolicyItem.id] (well,
  /// to MatchKey, which the backend exposes as `id` post-normalization
  /// when the two match — for Kyverno they always do, for Gatekeeper the
  /// id is `kind/name` and policy joins read MatchKey directly per
  /// `backend/internal/policy/types.go::NormalizedPolicy.MatchKey`).
  final String policy;

  /// Sub-rule name when the engine separates rules within a policy
  /// (Kyverno does; Gatekeeper does not). Empty when absent.
  final String rule;

  final String severity;
  final String action;
  final String message;

  /// Empty for cluster-scoped target resources.
  final String namespace;

  final String kind;
  final String name;

  /// RFC-3339 timestamp from the controller event; empty when the engine
  /// didn't emit one. Used for sort tiebreakers.
  final String timestamp;

  final PolicyEngine engine;
  final bool blocking;

  factory PolicyViolation.fromJson(Map<String, dynamic> json) {
    return PolicyViolation(
      policy: json['policy'] as String? ?? '',
      rule: json['rule'] as String? ?? '',
      severity: (json['severity'] as String? ?? kPolicyDefaultSeverity)
          .toLowerCase(),
      action: json['action'] as String? ?? '',
      message: json['message'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      timestamp: json['timestamp'] as String? ?? '',
      engine: _engineFromJson(json['engine']),
      blocking: json['blocking'] == true,
    );
  }

  /// Stable identifier for `ListView.builder` keys + detail-screen
  /// lookup. The server has no id; this tuple is the unique signature
  /// per CLAUDE.md: `(policy, rule, namespace, kind, name)`.
  String get stableKey => '$policy|$rule|$namespace|$kind|$name';
}

/// Per-severity pass/fail breakdown. Mirrors `SeverityCounts`.
class PolicySeverityCounts {
  const PolicySeverityCounts({
    required this.pass,
    required this.fail,
    required this.total,
  });

  final int pass;
  final int fail;
  final int total;

  factory PolicySeverityCounts.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    return PolicySeverityCounts(
      pass: i(json['pass']),
      fail: i(json['fail']),
      total: i(json['total']),
    );
  }
}

/// Aggregate compliance score. Mirrors `ComplianceScore`.
class ComplianceScore {
  const ComplianceScore({
    required this.scope,
    required this.score,
    required this.pass,
    required this.fail,
    required this.warn,
    required this.total,
    this.bySeverity = const {},
  });

  /// Namespace filter applied; empty string means cluster-wide.
  final String scope;

  /// 0-100 weighted score. `total == 0` yields 100 (no policies = vacuous
  /// compliance) per the backend.
  final double score;

  final int pass;
  final int fail;
  final int warn;
  final int total;

  /// Severity → counts. Key is lowercase severity label.
  final Map<String, PolicySeverityCounts> bySeverity;

  factory ComplianceScore.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    double d(Object? v) {
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v) ?? 0;
      return 0;
    }

    final raw = json['bySeverity'];
    final bs = <String, PolicySeverityCounts>{};
    if (raw is Map) {
      for (final entry in raw.entries) {
        // PolicyItem.severity / PolicyViolation.severity lowercase on
        // parse so `kPolicySeverityWeights` lookups and `kSeverityOrder`
        // comparisons work consistently. Mirror that here — without the
        // lowercase a future engine emitting `Critical` would sort to
        // the end of the by-severity breakdown via the unknown-key
        // alphabetic-tiebreaker branch, contradicting the rest of the
        // pipeline.
        final k = entry.key.toString().toLowerCase();
        final v = entry.value;
        if (v is Map) {
          bs[k] = PolicySeverityCounts.fromJson(Map<String, dynamic>.from(v));
        }
      }
    }

    return ComplianceScore(
      scope: json['scope'] as String? ?? '',
      score: d(json['score']),
      pass: i(json['pass']),
      fail: i(json['fail']),
      warn: i(json['warn']),
      total: i(json['total']),
      bySeverity: bs,
    );
  }

  static const empty = ComplianceScore(
    scope: '',
    score: 100,
    pass: 0,
    fail: 0,
    warn: 0,
    total: 0,
  );
}

/// One historical compliance datapoint. Mirrors the inline
/// `historyPoint` struct returned by `HandleComplianceHistory`. Date is
/// a yyyy-MM-dd string; score is 0-100.
class ComplianceHistoryPoint {
  const ComplianceHistoryPoint({
    required this.date,
    required this.score,
    required this.pass,
    required this.fail,
    required this.warn,
    required this.total,
  });

  /// yyyy-MM-dd, UTC. Mobile parses to [DateTime] lazily for axis
  /// rendering; raw string kept for clean serialization round-trips.
  final String date;

  final double score;
  final int pass;
  final int fail;
  final int warn;
  final int total;

  factory ComplianceHistoryPoint.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    double d(Object? v) {
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v) ?? 0;
      return 0;
    }

    return ComplianceHistoryPoint(
      date: json['date'] as String? ?? '',
      score: d(json['score']),
      pass: i(json['pass']),
      fail: i(json['fail']),
      warn: i(json['warn']),
      total: i(json['total']),
    );
  }
}

// ---------------------------------------------------------------------------
// 503 distinguisher
// ---------------------------------------------------------------------------

/// True when [error] is a 503 carrying the backend's distinguished
/// "compliance history requires a database" message. UI routes this to a
/// non-retryable empty state rather than the generic retry-able 503
/// surface. See `backend/internal/policy/handler.go::HandleComplianceHistory`.
///
/// The detection is by substring match on the message body so a future
/// punctuation tweak ("compliance history requires a database to record
/// snapshots") still routes correctly.
bool isComplianceHistoryNotConfigured(Object error) {
  if (error is! ApiError) return false;
  if (error.statusCode != 503) return false;
  final msg = error.message.toLowerCase();
  return msg.contains('requires a database');
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

/// Stateless wrapper over `/v1/policies/*`. Cluster pinning threads
/// through `clusterIdOverride` so the wire header always matches the
/// family-key slot the caller writes back into.
class PolicyRepository {
  PolicyRepository(this._dio);

  final Dio _dio;

  /// Fetches policy-engine discovery status. Returns
  /// [PolicyDiscoveryStatus.unreachable] on 5xx so the surface keeps
  /// rendering install-guidance copy without flashing an error card;
  /// `serviceUnavailable: true` lets the UI add a transient-error nuance
  /// if it wants (e.g., during rolling restarts).
  Future<PolicyDiscoveryStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/policies/status',
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return PolicyDiscoveryStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return PolicyDiscoveryStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return PolicyDiscoveryStatus.unreachable;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists all normalized policies. Server already RBAC-filters and sorts
  /// by severity weight desc, name asc; no client re-sort needed unless
  /// the caller applies its own filters.
  Future<List<PolicyItem>> listPolicies({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<PolicyItem>(
        path: '/api/v1/policies/',
        parse: PolicyItem.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists violations. RBAC-filtered (user must `list pods` in the
  /// namespace to see its violations). Cluster-scoped violations are
  /// admin-only.
  Future<List<PolicyViolation>> listViolations({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<PolicyViolation>(
        path: '/api/v1/policies/violations',
        parse: PolicyViolation.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Fetches the weighted compliance score. [scopeNamespace] is null /
  /// empty for cluster-wide; otherwise filters violations to that
  /// namespace (RBAC still applies on top).
  Future<ComplianceScore> compliance({
    String? scopeNamespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/policies/compliance',
        queryParameters: (scopeNamespace != null && scopeNamespace.isNotEmpty)
            ? {'namespace': scopeNamespace}
            : null,
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return ComplianceScore.fromJson(Map<String, dynamic>.from(data));
      }
      return ComplianceScore.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Admin-only. Fetches historical compliance snapshots over a sliding
  /// window. Backend clamps [days] to [1, 90]; 30 is the default. The
  /// client-side assertion documents the constraint so a future caller
  /// passing 0 or 365 fails loud rather than getting silently remapped.
  ///
  /// 503 with "requires a database" body indicates the ComplianceStore
  /// PostgreSQL persistence isn't configured — surface via
  /// [isComplianceHistoryNotConfigured] check at the call site, and
  /// render a non-retryable empty state. Other 503s stay retry-able.
  Future<List<ComplianceHistoryPoint>> complianceHistory({
    int days = 30,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) {
    assert(
      days >= 1 && days <= 90,
      'complianceHistory(days:) must be in [1, 90]; backend clamps '
      'silently otherwise. Got $days.',
    );
    return _fetchList<ComplianceHistoryPoint>(
      path: '/api/v1/policies/compliance/history',
      query: {'days': '$days'},
      parse: ComplianceHistoryPoint.fromJson,
      clusterIdOverride: clusterIdOverride,
      cancelToken: cancelToken,
    );
  }

  // -------------------------------------------------------------------------
  // Internals
  // -------------------------------------------------------------------------

  Options? _opts(String? clusterIdOverride) => clusterIdOverride == null
      ? null
      : Options(headers: {'X-Cluster-ID': clusterIdOverride});

  Future<List<T>> _fetchList<T>({
    required String path,
    Map<String, String>? query,
    required T Function(Map<String, dynamic>) parse,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        queryParameters: query,
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is List) {
        return data
            .whereType<Map<dynamic, dynamic>>()
            .map((m) => parse(Map<String, dynamic>.from(m)))
            .toList();
      }
      return <T>[];
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

final policyRepositoryProvider = Provider<PolicyRepository>((ref) {
  return PolicyRepository(ref.watch(dioProvider));
});

/// Per-cluster policy discovery status. Drives
/// `FeatureUnavailableState.policy()` on every observatory surface.
///
/// Distinct from `wizards/types/policy/policy_engine_status.dart` —
/// that provider hits the same endpoint but uses a different family key
/// type for the wizard's bootstrap. Two providers share the cache slot
/// per cluster (one keyed by `String`, one by `PolicyEngineStatusKey`),
/// which is fine because Dio's response body is the same JSON either
/// way — the duplication is one extra HTTP round-trip per surface, which
/// matches what the ESO and cert-manager observatories pay.
final policyStatusProvider = FutureProvider.autoDispose
    .family<PolicyDiscoveryStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('policy status invalidated');
  });
  return ref.read(policyRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

final policiesListProvider = FutureProvider.autoDispose
    .family<List<PolicyItem>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('policies list invalidated');
  });
  return ref.read(policyRepositoryProvider).listPolicies(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

final violationsListProvider = FutureProvider.autoDispose
    .family<List<PolicyViolation>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('violations list invalidated');
  });
  return ref.read(policyRepositoryProvider).listViolations(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Compliance score. Key holds optional namespace scope so two screens
/// open against different scopes don't share state.
class ComplianceScoreKey {
  /// Normalise [scopeNamespace]: empty string is treated as null so two
  /// ostensibly-equal keys collapse to the same slot.
  ComplianceScoreKey({required this.clusterId, String? scopeNamespace})
      : scopeNamespace =
            (scopeNamespace != null && scopeNamespace.isEmpty)
                ? null
                : scopeNamespace;

  final String clusterId;
  final String? scopeNamespace;

  @override
  bool operator ==(Object other) =>
      other is ComplianceScoreKey &&
      other.clusterId == clusterId &&
      other.scopeNamespace == scopeNamespace;

  @override
  int get hashCode => Object.hash(clusterId, scopeNamespace);
}

final complianceScoreProvider = FutureProvider.autoDispose
    .family<ComplianceScore, ComplianceScoreKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('compliance score invalidated');
  });
  return ref.read(policyRepositoryProvider).compliance(
        scopeNamespace: key.scopeNamespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Compliance history. Window is fixed at 30 days for M4; custom ranges
/// are deferred to M5 polish per the plan's Scope Boundaries section.
class ComplianceHistoryKey {
  const ComplianceHistoryKey({required this.clusterId, this.days = 30});

  final String clusterId;
  final int days;

  @override
  bool operator ==(Object other) =>
      other is ComplianceHistoryKey &&
      other.clusterId == clusterId &&
      other.days == days;

  @override
  int get hashCode => Object.hash(clusterId, days);
}

final complianceHistoryProvider = FutureProvider.autoDispose
    .family<List<ComplianceHistoryPoint>, ComplianceHistoryKey>(
        (ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('compliance history invalidated');
  });
  return ref.read(policyRepositoryProvider).complianceHistory(
        days: key.days,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
