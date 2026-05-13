// Mobile-side wrapper over the backend External Secrets Operator (ESO)
// API at `/v1/externalsecrets/{status,externalsecrets,
// externalsecrets/{ns}/{name},clusterexternalsecrets[/{name}],
// stores[/{ns}/{name}],stores/{ns}/{name}/metrics,
// clusterstores[/{name}],clusterstores/{name}/metrics,
// pushsecrets[/{ns}/{name}]}`. Wire types mirror
// `backend/internal/externalsecrets/types.go` and
// `frontend/lib/eso-types.ts` exactly.
//
// Drift contract (read three times before changing — the asymmetry is
// deliberate):
//   * LIST endpoint omits `driftStatus`; it populates
//     `lastObservedDriftStatus` from the 60s background poller. Stale by
//     up to 90s (60s poller + 30s handler cache).
//   * DETAIL endpoint always populates the live `driftStatus` via an
//     impersonated `get secret` and leaves `lastObservedDriftStatus`
//     empty. This is the source of truth.
// Mobile mirrors this exactly — list screens render from
// `lastObservedDriftStatus`, detail screens render from `driftStatus`.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards
// it as an explicit `X-Cluster-ID` header. The PR-3c interceptor
// invariant (only injects when absent) is the live contract.
//
// Web parallel: `frontend/islands/ESODashboard.tsx`,
// `frontend/islands/ESOExternalSecretsList.tsx`,
// `frontend/islands/ESOExternalSecretDetail.tsx`,
// `frontend/islands/ESOStoreDetail.tsx`,
// `frontend/islands/ESOStoreMetricsPanel.tsx`.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

/// Lifecycle state of an ExternalSecret. Mirrors `Status` in
/// `backend/internal/externalsecrets/types.go`. Open enum on the wire —
/// older / future ESO responses may add states, so [unknown] is the
/// explicit fallback rather than crashing the parser.
enum EsoStatus {
  synced,
  syncFailed,
  refreshing,
  stale,
  drifted,
  unknown,
}

EsoStatus _esoStatusFromJson(Object? v) {
  if (v is! String) return EsoStatus.unknown;
  switch (v) {
    case 'Synced':
      return EsoStatus.synced;
    case 'SyncFailed':
      return EsoStatus.syncFailed;
    case 'Refreshing':
      return EsoStatus.refreshing;
    case 'Stale':
      return EsoStatus.stale;
    case 'Drifted':
      return EsoStatus.drifted;
    default:
      return EsoStatus.unknown;
  }
}

/// Human label used in status pills and filter chips. Capitalised to
/// match the wire enum exactly so screenshots across web + mobile read
/// identically.
String esoStatusLabel(EsoStatus s) => switch (s) {
      EsoStatus.synced => 'Synced',
      EsoStatus.syncFailed => 'SyncFailed',
      EsoStatus.refreshing => 'Refreshing',
      EsoStatus.stale => 'Stale',
      EsoStatus.drifted => 'Drifted',
      EsoStatus.unknown => 'Unknown',
    };

/// Tri-state drift indicator. **`Unknown` is NEVER an error** — it
/// signals the provider doesn't populate `SyncedResourceVersion`, which
/// is common (the Kubernetes provider, for instance, omits it). UI
/// renders `InSync` → success, `Drifted` → warning,
/// `Unknown` → textMuted (per PR-3f learnings #9).
enum DriftStatus {
  inSync,
  drifted,
  unknown,

  /// Distinct from [unknown]. Used by the list path where the backend
  /// has no observation yet and omits the field entirely. Maps to
  /// "no drift hint available" in the UI — usually rendered as no pill
  /// at all rather than a textMuted "Unknown" pill.
  notObserved,
}

DriftStatus _driftStatusFromJson(Object? v) {
  if (v is! String || v.isEmpty) return DriftStatus.notObserved;
  switch (v) {
    case 'InSync':
      return DriftStatus.inSync;
    case 'Drifted':
      return DriftStatus.drifted;
    case 'Unknown':
      return DriftStatus.unknown;
    default:
      return DriftStatus.notObserved;
  }
}

/// Reasons accompanying `driftStatus = Unknown` on a detail response.
/// Empty when drift is definite. Detail screen surfaces this as a
/// tooltip under the drift pill so operators see WHY drift wasn't
/// resolvable rather than guessing.
enum DriftUnknownReason {
  noSyncedRv,
  noTargetName,
  secretDeleted,
  rbacDenied,
  transientError,
  clientError,

  /// Open-enum forward-compat fallback. Renders as generic "Drift not
  /// resolvable" copy on the detail screen.
  unspecified,
}

DriftUnknownReason driftUnknownReasonFromJson(Object? v) {
  if (v is! String) return DriftUnknownReason.unspecified;
  switch (v) {
    case 'no_synced_rv':
      return DriftUnknownReason.noSyncedRv;
    case 'no_target_name':
      return DriftUnknownReason.noTargetName;
    case 'secret_deleted':
      return DriftUnknownReason.secretDeleted;
    case 'rbac_denied':
      return DriftUnknownReason.rbacDenied;
    case 'transient_error':
      return DriftUnknownReason.transientError;
    case 'client_error':
      return DriftUnknownReason.clientError;
    default:
      return DriftUnknownReason.unspecified;
  }
}

/// Layer that supplied an annotation-resolved threshold. See
/// `backend/internal/externalsecrets/types.go` — these match the
/// resolver's enum.
enum EsoThresholdSource {
  packageDefault,
  externalSecret,
  secretStore,
  clusterSecretStore,
  unknown,
}

EsoThresholdSource _thresholdSourceFromJson(Object? v) {
  if (v is! String) return EsoThresholdSource.unknown;
  switch (v) {
    case 'default':
      return EsoThresholdSource.packageDefault;
    case 'externalsecret':
      return EsoThresholdSource.externalSecret;
    case 'secretstore':
      return EsoThresholdSource.secretStore;
    case 'clustersecretstore':
      return EsoThresholdSource.clusterSecretStore;
    default:
      return EsoThresholdSource.unknown;
  }
}

// ---------------------------------------------------------------------------
// Wire-format value types
// ---------------------------------------------------------------------------

/// Decoded `/v1/externalsecrets/status` response. Mirrors `ESOStatus`
/// in the Go types.
class EsoDiscoveryStatus {
  const EsoDiscoveryStatus({
    required this.detected,
    this.namespace,
    this.version,
    this.lastChecked = '',
  });

  final bool detected;
  final String? namespace;
  final String? version;

  /// RFC-3339 timestamp; empty when the first probe hasn't completed.
  final String lastChecked;

  factory EsoDiscoveryStatus.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return EsoDiscoveryStatus(
      detected: json['detected'] as bool? ?? false,
      namespace: s(json['namespace']),
      version: s(json['version']),
      lastChecked: json['lastChecked'] as String? ?? '',
    );
  }

  static const empty = EsoDiscoveryStatus(detected: false);
}

/// Reference from an ExternalSecret / ClusterExternalSecret / PushSecret
/// to its backing SecretStore or ClusterSecretStore.
class EsoStoreRef {
  const EsoStoreRef({required this.name, required this.kind});

  final String name;

  /// `"SecretStore"` or `"ClusterSecretStore"`. Open enum on the wire,
  /// kept as raw String so a future ESO ref kind doesn't crash the
  /// parser.
  final String kind;

  factory EsoStoreRef.fromJson(Map<String, dynamic> json) => EsoStoreRef(
        name: json['name'] as String? ?? '',
        kind: json['kind'] as String? ?? '',
      );
}

/// One ExternalSecret row on the list endpoint, or the detail response.
///
/// Wire contract: the list endpoint populates [lastObservedDriftStatus]
/// (poller hint) and leaves [driftStatus] = [DriftStatus.notObserved];
/// the detail endpoint inverts this. Callers must NOT collapse the two
/// fields into one — the live-vs-cached distinction is meaningful.
class ExternalSecret {
  const ExternalSecret({
    required this.namespace,
    required this.name,
    required this.uid,
    required this.status,
    required this.storeRef,
    this.driftStatus = DriftStatus.notObserved,
    this.driftUnknownReason = DriftUnknownReason.unspecified,
    this.lastObservedDriftStatus = DriftStatus.notObserved,
    this.readyReason,
    this.readyMessage,
    this.targetSecretName,
    this.refreshInterval,
    this.lastSyncTime,
    this.syncedResourceVersion,
    this.staleAfterMinutes,
    this.staleAfterMinutesSource = EsoThresholdSource.unknown,
    this.alertOnRecovery,
    this.alertOnRecoverySource = EsoThresholdSource.unknown,
    this.alertOnLifecycle,
    this.alertOnLifecycleSource = EsoThresholdSource.unknown,
  });

  final String namespace;
  final String name;
  final String uid;
  final EsoStatus status;
  final EsoStoreRef storeRef;

  /// Live drift state (detail endpoint only). [DriftStatus.notObserved]
  /// on list rows.
  final DriftStatus driftStatus;

  /// Populated only when [driftStatus] == [DriftStatus.unknown].
  final DriftUnknownReason driftUnknownReason;

  /// Poller's last-observed drift state (list endpoint only).
  /// [DriftStatus.notObserved] on detail responses and when no
  /// observation has been recorded yet.
  final DriftStatus lastObservedDriftStatus;

  final String? readyReason;
  final String? readyMessage;
  final String? targetSecretName;

  /// Duration string, e.g. `"1h"`, `"30m"`. Rendered verbatim — no
  /// parsing required for any current call site.
  final String? refreshInterval;

  /// RFC-3339 timestamp. Null when the ES hasn't synced yet.
  final String? lastSyncTime;

  final String? syncedResourceVersion;

  /// Annotation-resolved thresholds. Null in Phase A; populated by
  /// Phase D's resolver. Mirrors web's nullable shape so the UI can
  /// distinguish "resolver hasn't run" from "resolver ran, no value".
  final int? staleAfterMinutes;
  final EsoThresholdSource staleAfterMinutesSource;
  final bool? alertOnRecovery;
  final EsoThresholdSource alertOnRecoverySource;
  final bool? alertOnLifecycle;
  final EsoThresholdSource alertOnLifecycleSource;

  factory ExternalSecret.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    // Defensive cast mirrors certmanager_repository.dart's helper —
    // accept num (canonical), parse String (forward-compat / mock-
    // backend / replay-proxy resilience), reject everything else.
    int? n(Object? v) {
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v);
      return null;
    }

    bool? b(Object? v) {
      if (v is bool) return v;
      return null;
    }

    return ExternalSecret(
      namespace: json['namespace'] as String? ?? '',
      name: json['name'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      status: _esoStatusFromJson(json['status']),
      storeRef: json['storeRef'] is Map
          ? EsoStoreRef.fromJson(
              Map<String, dynamic>.from(json['storeRef'] as Map),
            )
          : const EsoStoreRef(name: '', kind: ''),
      driftStatus: _driftStatusFromJson(json['driftStatus']),
      driftUnknownReason:
          driftUnknownReasonFromJson(json['driftUnknownReason']),
      lastObservedDriftStatus:
          _driftStatusFromJson(json['lastObservedDriftStatus']),
      readyReason: s(json['readyReason']),
      readyMessage: s(json['readyMessage']),
      targetSecretName: s(json['targetSecretName']),
      refreshInterval: s(json['refreshInterval']),
      lastSyncTime: s(json['lastSyncTime']),
      syncedResourceVersion: s(json['syncedResourceVersion']),
      staleAfterMinutes: n(json['staleAfterMinutes']),
      staleAfterMinutesSource:
          _thresholdSourceFromJson(json['staleAfterMinutesSource']),
      alertOnRecovery: b(json['alertOnRecovery']),
      alertOnRecoverySource:
          _thresholdSourceFromJson(json['alertOnRecoverySource']),
      alertOnLifecycle: b(json['alertOnLifecycle']),
      alertOnLifecycleSource:
          _thresholdSourceFromJson(json['alertOnLifecycleSource']),
    );
  }

  /// The drift state to render on a list row. Prefers [driftStatus]
  /// (detail-only) when present, falls back to [lastObservedDriftStatus]
  /// (list-only). Returns [DriftStatus.notObserved] when neither is
  /// populated — caller renders no pill in that case.
  DriftStatus get effectiveDriftStatus =>
      driftStatus != DriftStatus.notObserved
          ? driftStatus
          : lastObservedDriftStatus;
}

/// ClusterExternalSecret — the cluster-scoped fan-out form. Selector
/// + namespace lists are pulled verbatim from the response; mobile
/// renders them as chip strips.
class ClusterExternalSecret {
  const ClusterExternalSecret({
    required this.name,
    required this.uid,
    required this.status,
    required this.storeRef,
    this.readyReason,
    this.readyMessage,
    this.targetSecretName,
    this.refreshInterval,
    this.namespaceSelectors = const <String>[],
    this.namespaces = const <String>[],
    this.provisionedNamespaces = const <String>[],
    this.failedNamespaces = const <String>[],
    this.externalSecretBaseName,
  });

  final String name;
  final String uid;
  final EsoStatus status;
  final EsoStoreRef storeRef;
  final String? readyReason;
  final String? readyMessage;
  final String? targetSecretName;
  final String? refreshInterval;

  /// Selector clauses rendered as `"k=v"` strings by the backend.
  final List<String> namespaceSelectors;

  /// Static namespace list (alternative to selector).
  final List<String> namespaces;

  /// Resolved namespaces where the child ES has been created
  /// successfully.
  final List<String> provisionedNamespaces;

  /// Namespaces where fan-out failed (RBAC / quota / similar).
  final List<String> failedNamespaces;

  final String? externalSecretBaseName;

  factory ClusterExternalSecret.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    List<String> strList(Object? raw) =>
        raw is List ? raw.whereType<String>().toList() : const <String>[];

    return ClusterExternalSecret(
      name: json['name'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      status: _esoStatusFromJson(json['status']),
      storeRef: json['storeRef'] is Map
          ? EsoStoreRef.fromJson(
              Map<String, dynamic>.from(json['storeRef'] as Map),
            )
          : const EsoStoreRef(name: '', kind: ''),
      readyReason: s(json['readyReason']),
      readyMessage: s(json['readyMessage']),
      targetSecretName: s(json['targetSecretName']),
      refreshInterval: s(json['refreshInterval']),
      namespaceSelectors: strList(json['namespaceSelectors']),
      namespaces: strList(json['namespaces']),
      provisionedNamespaces: strList(json['provisionedNamespaces']),
      failedNamespaces: strList(json['failedNamespaces']),
      externalSecretBaseName: s(json['externalSecretBaseName']),
    );
  }
}

/// SecretStore — both namespaced and cluster-scoped variants share this
/// type, discriminated by [scope] (`"Namespaced"` / `"Cluster"`).
/// Cluster-scoped stores have empty [namespace].
class SecretStore {
  const SecretStore({
    required this.name,
    required this.uid,
    required this.scope,
    required this.status,
    required this.ready,
    required this.provider,
    this.namespace = '',
    this.readyReason,
    this.readyMessage,
    this.providerSpec = const <String, dynamic>{},
    this.staleAfterMinutes,
    this.alertOnRecovery,
    this.alertOnLifecycle,
  });

  /// Empty for ClusterSecretStore.
  final String namespace;
  final String name;
  final String uid;

  /// `"Namespaced"` or `"Cluster"`. Kept as raw String so a future ESO
  /// scope literal doesn't break the parser.
  final String scope;

  final EsoStatus status;
  final bool ready;
  final String? readyReason;
  final String? readyMessage;

  /// Provider family — `"vault"`, `"aws"`, `"gcpsm"`, `"azurekv"`,
  /// `"kubernetes"`, etc. Empty when no provider key is set.
  final String provider;

  /// Raw `spec.provider.<provider>` sub-object, surfaced verbatim. The
  /// detail screen renders this as a read-only KV pane — mobile does
  /// not parse provider-specific shapes.
  final Map<String, dynamic> providerSpec;

  /// Phase D annotation-set thresholds. Inherited by ESes referencing
  /// this store unless overridden at the ES level.
  final int? staleAfterMinutes;
  final bool? alertOnRecovery;
  final bool? alertOnLifecycle;

  bool get isCluster => scope == 'Cluster';

  factory SecretStore.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    int? n(Object? v) {
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v);
      return null;
    }

    Map<String, dynamic> mapOf(Object? raw) {
      if (raw is Map) return Map<String, dynamic>.from(raw);
      return const <String, dynamic>{};
    }

    return SecretStore(
      namespace: json['namespace'] as String? ?? '',
      name: json['name'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      scope: json['scope'] as String? ?? '',
      status: _esoStatusFromJson(json['status']),
      ready: json['ready'] as bool? ?? false,
      readyReason: s(json['readyReason']),
      readyMessage: s(json['readyMessage']),
      provider: json['provider'] as String? ?? '',
      providerSpec: mapOf(json['providerSpec']),
      staleAfterMinutes: n(json['staleAfterMinutes']),
      alertOnRecovery:
          json['alertOnRecovery'] is bool ? json['alertOnRecovery'] as bool : null,
      alertOnLifecycle: json['alertOnLifecycle'] is bool
          ? json['alertOnLifecycle'] as bool
          : null,
    );
  }
}

/// PushSecret — the inverse-direction CRD that pushes a Kubernetes
/// Secret out to a source store. Read-only in v1 (backend has no write
/// surface for it); mobile mirrors that posture.
class PushSecret {
  const PushSecret({
    required this.namespace,
    required this.name,
    required this.uid,
    required this.status,
    required this.storeRefs,
    this.readyReason,
    this.readyMessage,
    this.sourceSecretName,
    this.refreshInterval,
    this.lastSyncTime,
  });

  final String namespace;
  final String name;
  final String uid;
  final EsoStatus status;

  /// PushSecret can fan a single Secret to multiple stores.
  final List<EsoStoreRef> storeRefs;

  final String? readyReason;
  final String? readyMessage;
  final String? sourceSecretName;
  final String? refreshInterval;
  final String? lastSyncTime;

  factory PushSecret.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    final raw = json['storeRefs'];
    final refs = raw is List
        ? raw
            .whereType<Map<dynamic, dynamic>>()
            .map((m) => EsoStoreRef.fromJson(Map<String, dynamic>.from(m)))
            .toList()
        : const <EsoStoreRef>[];

    return PushSecret(
      namespace: json['namespace'] as String? ?? '',
      name: json['name'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      status: _esoStatusFromJson(json['status']),
      storeRefs: refs,
      readyReason: s(json['readyReason']),
      readyMessage: s(json['readyMessage']),
      sourceSecretName: s(json['sourceSecretName']),
      refreshInterval: s(json['refreshInterval']),
      lastSyncTime: s(json['lastSyncTime']),
    );
  }
}

/// Per-store cost estimate carried alongside the metrics response. The
/// backend suppresses this card entirely (nil) for self-hosted /
/// unknown providers (Vault, Kubernetes, Akeyless, etc.); UI must NOT
/// fabricate a zero in that case — show nothing instead.
class CostEstimate {
  const CostEstimate({
    required this.billingProvider,
    this.currency,
    this.usdPerMillion,
    this.estimated24h,
    this.lastUpdated,
  });

  final String billingProvider;
  final String? currency;
  final double? usdPerMillion;
  final double? estimated24h;
  final String? lastUpdated;

  factory CostEstimate.fromJson(Map<String, dynamic> json) {
    double? d(Object? v) {
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v);
      return null;
    }

    return CostEstimate(
      billingProvider: json['billingProvider'] as String? ?? '',
      currency: json['currency'] as String?,
      usdPerMillion: d(json['usdPerMillion']),
      estimated24h: d(json['estimated24h']),
      lastUpdated: json['lastUpdated'] as String?,
    );
  }
}

/// `GET /externalsecrets/{cluster,}stores/.../metrics` response.
///
/// Both [ratePerMin] and [last24h] are nullable: null means
/// "Prometheus has no series yet for the dependent set" OR "Prometheus
/// is offline" — distinguished by whether [error] is populated. The UI
/// MUST NOT fabricate a zero in either case (per backend R25).
class StoreMetrics {
  const StoreMetrics({
    this.ratePerMin,
    this.last24h,
    this.cost,
    this.error,
    this.windowEnd,
  });

  /// `sum(rate(externalsecret_sync_calls_total[5m]))` projected to
  /// per-minute. Null when no series exist OR Prometheus is offline.
  final double? ratePerMin;

  /// Total requests over the last 24h. Same null semantics as
  /// [ratePerMin].
  final double? last24h;

  /// Cost block. Null for self-hosted providers (no rate card) AND
  /// when usage data is absent.
  final CostEstimate? cost;

  /// Populated on degradation; HTTP is still 200 in that case. UI
  /// renders this as a banner above the metrics body.
  final String? error;

  /// RFC-3339 sample timestamp; empty / null when degraded.
  final String? windowEnd;

  bool get isDegraded => error != null && error!.isNotEmpty;

  factory StoreMetrics.fromJson(Map<String, dynamic> json) {
    double? d(Object? v) {
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v);
      return null;
    }

    return StoreMetrics(
      ratePerMin: d(json['ratePerMin']),
      last24h: d(json['last24h']),
      cost: json['cost'] is Map
          ? CostEstimate.fromJson(
              Map<String, dynamic>.from(json['cost'] as Map),
            )
          : null,
      error: json['error'] as String?,
      windowEnd: json['windowEnd'] as String?,
    );
  }
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

/// Stateless wrapper over `/v1/externalsecrets/*`. Cluster pinning
/// threads through `clusterIdOverride` so the wire header always
/// matches the family-key slot the caller writes back into.
class EsoRepository {
  EsoRepository(this._dio);

  final Dio _dio;

  /// Fetches ESO discovery status. Returns [EsoDiscoveryStatus.empty] on
  /// 5xx so callers route straight to `FeatureUnavailableState.eso()` —
  /// a flaky reverse-proxy probe shouldn't surface as an error card.
  Future<EsoDiscoveryStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/externalsecrets/status',
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return EsoDiscoveryStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return EsoDiscoveryStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return EsoDiscoveryStatus.empty;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists ExternalSecrets. Optional `namespace` filters server-side.
  /// Each row carries `lastObservedDriftStatus` (poller hint, stale by
  /// up to 90s); `driftStatus` is always absent — that lives on detail.
  Future<List<ExternalSecret>> listExternalSecrets({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<ExternalSecret>(
        path: '/api/v1/externalsecrets/externalsecrets',
        query: (namespace != null && namespace.isNotEmpty)
            ? {'namespace': namespace}
            : null,
        parse: ExternalSecret.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Fetches a single ExternalSecret with live drift resolved. Backend
  /// runs an impersonated `get secret` to compare `syncedResourceVersion`
  /// against the live Secret's RV.
  Future<ExternalSecret> getExternalSecret({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    return _getOne<ExternalSecret>(
      path: '/api/v1/externalsecrets/externalsecrets/'
          '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
      parse: ExternalSecret.fromJson,
      notFoundMessage: 'External secret $namespace/$name not found',
      clusterIdOverride: clusterIdOverride,
      cancelToken: cancelToken,
    );
  }

  /// Lists ClusterExternalSecrets. Backend returns empty list (not 403)
  /// when the impersonated user lacks `list clusterexternalsecrets` —
  /// permissive-read pattern mirroring the cert-manager and policy
  /// handlers.
  Future<List<ClusterExternalSecret>> listClusterExternalSecrets({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<ClusterExternalSecret>(
        path: '/api/v1/externalsecrets/clusterexternalsecrets',
        parse: ClusterExternalSecret.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  Future<ClusterExternalSecret> getClusterExternalSecret({
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<ClusterExternalSecret>(
        path: '/api/v1/externalsecrets/clusterexternalsecrets/'
            '${Uri.encodeComponent(name)}',
        parse: ClusterExternalSecret.fromJson,
        notFoundMessage: 'ClusterExternalSecret $name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists namespaced SecretStores. Optional `namespace` filters
  /// server-side via the `?namespace=` query param.
  Future<List<SecretStore>> listStores({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<SecretStore>(
        path: '/api/v1/externalsecrets/stores',
        query: (namespace != null && namespace.isNotEmpty)
            ? {'namespace': namespace}
            : null,
        parse: SecretStore.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  Future<SecretStore> getStore({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<SecretStore>(
        path: '/api/v1/externalsecrets/stores/'
            '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
        parse: SecretStore.fromJson,
        notFoundMessage: 'SecretStore $namespace/$name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists ClusterSecretStores. Same permissive-read posture as
  /// [listClusterExternalSecrets].
  Future<List<SecretStore>> listClusterStores({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<SecretStore>(
        path: '/api/v1/externalsecrets/clusterstores',
        parse: SecretStore.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  Future<SecretStore> getClusterStore({
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<SecretStore>(
        path: '/api/v1/externalsecrets/clusterstores/'
            '${Uri.encodeComponent(name)}',
        parse: SecretStore.fromJson,
        notFoundMessage: 'ClusterSecretStore $name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Fetches per-namespaced-store metrics (rate + cost-tier).
  Future<StoreMetrics> getStoreMetrics({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<StoreMetrics>(
        path: '/api/v1/externalsecrets/stores/'
            '${Uri.encodeComponent(namespace)}/'
            '${Uri.encodeComponent(name)}/metrics',
        parse: StoreMetrics.fromJson,
        notFoundMessage: 'Store metrics $namespace/$name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Fetches per-cluster-store metrics. Same response shape as the
  /// namespaced variant; aggregation across every namespace's ESes that
  /// reference this ClusterSecretStore.
  Future<StoreMetrics> getClusterStoreMetrics({
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<StoreMetrics>(
        path: '/api/v1/externalsecrets/clusterstores/'
            '${Uri.encodeComponent(name)}/metrics',
        parse: StoreMetrics.fromJson,
        notFoundMessage: 'ClusterStore metrics $name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists PushSecrets. Optional `namespace` filters server-side.
  Future<List<PushSecret>> listPushSecrets({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<PushSecret>(
        path: '/api/v1/externalsecrets/pushsecrets',
        query: (namespace != null && namespace.isNotEmpty)
            ? {'namespace': namespace}
            : null,
        parse: PushSecret.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  Future<PushSecret> getPushSecret({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _getOne<PushSecret>(
        path: '/api/v1/externalsecrets/pushsecrets/'
            '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
        parse: PushSecret.fromJson,
        notFoundMessage: 'PushSecret $namespace/$name not found',
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

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

  Future<T> _getOne<T>({
    required String path,
    required T Function(Map<String, dynamic>) parse,
    required String notFoundMessage,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return parse(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for $path',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      // Surface 404 as ApiError verbatim from the backend — the detail
      // screens map that to a clearer "not found" message via the
      // notFoundMessage hint while still showing the backend's detail
      // when present.
      final err = e.error;
      if (err is ApiError) throw err;
      throw ApiError.fromDio(e);
    }
  }
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

final esoRepositoryProvider = Provider<EsoRepository>((ref) {
  return EsoRepository(ref.watch(dioProvider));
});

/// Per-cluster ESO discovery status. Drives
/// `FeatureUnavailableState.eso()` on every ESO surface.
final esoStatusProvider = FutureProvider.autoDispose
    .family<EsoDiscoveryStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('eso status invalidated');
  });
  return ref.read(esoRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the ExternalSecret list provider. Carries cluster id
/// (so cluster switches force a fresh slot) and an optional namespace
/// filter (so two open list screens with different namespace filters
/// don't share state).
class ExternalSecretListKey {
  /// Normalise [namespace]: empty string is treated as null so two
  /// ostensibly-equal keys collapse to the same slot.
  ExternalSecretListKey({required this.clusterId, String? namespace})
      : namespace = (namespace != null && namespace.isEmpty) ? null : namespace;

  final String clusterId;
  final String? namespace;

  @override
  bool operator ==(Object other) =>
      other is ExternalSecretListKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

final externalSecretListProvider = FutureProvider.autoDispose
    .family<List<ExternalSecret>, ExternalSecretListKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('externalsecret list invalidated');
  });
  return ref.read(esoRepositoryProvider).listExternalSecrets(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

class ExternalSecretDetailKey {
  const ExternalSecretDetailKey({
    required this.clusterId,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ExternalSecretDetailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, name);
}

final externalSecretDetailProvider = FutureProvider.autoDispose
    .family<ExternalSecret, ExternalSecretDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('externalsecret detail invalidated');
  });
  return ref.read(esoRepositoryProvider).getExternalSecret(
        namespace: key.namespace,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

final clusterExternalSecretListProvider = FutureProvider.autoDispose
    .family<List<ClusterExternalSecret>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('CES list invalidated');
  });
  return ref.read(esoRepositoryProvider).listClusterExternalSecrets(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

class ClusterExternalSecretDetailKey {
  const ClusterExternalSecretDetailKey({
    required this.clusterId,
    required this.name,
  });

  final String clusterId;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ClusterExternalSecretDetailKey &&
      other.clusterId == clusterId &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, name);
}

final clusterExternalSecretDetailProvider = FutureProvider.autoDispose.family<
    ClusterExternalSecret, ClusterExternalSecretDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('CES detail invalidated');
  });
  return ref.read(esoRepositoryProvider).getClusterExternalSecret(
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Standalone provider for namespaced stores keyed by clusterId — this
/// is distinct from `wizards/widgets/store_picker.dart`'s
/// `storeListProvider` (which pairs namespaced + cluster lists into a
/// `_StoreLists` struct for the picker dropdown). PR-4h's read-side
/// screens need separate stream slots per scope, so they don't share
/// the picker's combined family.
final storesListProvider = FutureProvider.autoDispose
    .family<List<SecretStore>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('stores list invalidated');
  });
  return ref.read(esoRepositoryProvider).listStores(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

class StoreDetailKey {
  const StoreDetailKey({
    required this.clusterId,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is StoreDetailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, name);
}

final storeDetailProvider = FutureProvider.autoDispose
    .family<SecretStore, StoreDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('store detail invalidated');
  });
  return ref.read(esoRepositoryProvider).getStore(
        namespace: key.namespace,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

final clusterStoresListProvider = FutureProvider.autoDispose
    .family<List<SecretStore>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('cluster stores list invalidated');
  });
  return ref.read(esoRepositoryProvider).listClusterStores(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

class ClusterStoreDetailKey {
  const ClusterStoreDetailKey({required this.clusterId, required this.name});

  final String clusterId;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ClusterStoreDetailKey &&
      other.clusterId == clusterId &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, name);
}

final clusterStoreDetailProvider = FutureProvider.autoDispose
    .family<SecretStore, ClusterStoreDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('cluster store detail invalidated');
  });
  return ref.read(esoRepositoryProvider).getClusterStore(
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Metrics family — same key shape as the store detail key so the
/// detail screen can fetch detail + metrics in parallel without a
/// shared key class.
final storeMetricsProvider = FutureProvider.autoDispose
    .family<StoreMetrics, StoreDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('store metrics invalidated');
  });
  return ref.read(esoRepositoryProvider).getStoreMetrics(
        namespace: key.namespace,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

final clusterStoreMetricsProvider = FutureProvider.autoDispose
    .family<StoreMetrics, ClusterStoreDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('cluster store metrics invalidated');
  });
  return ref.read(esoRepositoryProvider).getClusterStoreMetrics(
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

final pushSecretListProvider = FutureProvider.autoDispose
    .family<List<PushSecret>, ExternalSecretListKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('pushsecret list invalidated');
  });
  return ref.read(esoRepositoryProvider).listPushSecrets(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

class PushSecretDetailKey {
  const PushSecretDetailKey({
    required this.clusterId,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is PushSecretDetailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, name);
}

final pushSecretDetailProvider = FutureProvider.autoDispose
    .family<PushSecret, PushSecretDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('pushsecret detail invalidated');
  });
  return ref.read(esoRepositoryProvider).getPushSecret(
        namespace: key.namespace,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
