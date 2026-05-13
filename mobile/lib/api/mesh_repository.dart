// Mobile-side wrapper over the backend service mesh API
// (`/v1/mesh/{status,routing,routing/{id},policies,mtls,golden-signals}`).
// Wire types mirror `backend/internal/servicemesh/types.go` exactly.
// Web parallel: `frontend/islands/Mesh*.tsx` + `frontend/lib/mesh-types.ts`.
//
// Read surfaces only — verb actions are not part of mesh and there are no
// writes here. cert-manager wizards landed in PR-3e; mesh has no wizards.
//
// Composite ID:
//   The backend emits each routing / policy resource as
//   `{mesh}:{namespace}:{kindCode}:{name}` (URL-encoded). Mobile uses
//   [MeshRouteId] / [PolicyId equivalent] from `util/composite_id.dart`
//   for parse/encode but stores the raw string on the wire so opaque
//   round-trips never re-encode.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards
// it as an explicit `X-Cluster-ID` header. The PR-3c interceptor
// invariant (`only injects header when absent`) is the live contract.
//
// Backend partial-failure pattern:
//   `RoutingResponse.errors` and `PoliciesResponse.errors` carry a
//   per-CRD failure map alongside successful results. The detail
//   screens surface these as inline banners — they are NOT fatal.
//   Cf. PR-3f's `_PartialFetchWarning` carry-over.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// Per-engine availability flags from `/v1/mesh/status`. Mirrors
/// `backend/internal/servicemesh/types.go::MeshInfo` exactly.
class MeshInfo {
  const MeshInfo({
    required this.installed,
    this.namespace,
    this.version,
    this.mode,
  });

  final bool installed;

  /// Control-plane namespace (e.g. `istio-system`, `linkerd`). Empty for
  /// non-admin users — backend redacts before serialising.
  final String? namespace;
  final String? version;

  /// Istio-only: "sidecar" or "ambient". Empty for Linkerd.
  final String? mode;

  factory MeshInfo.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return MeshInfo(
      installed: json['installed'] as bool? ?? false,
      namespace: s(json['namespace']),
      version: s(json['version']),
      mode: s(json['mode']),
    );
  }

  static const empty = MeshInfo(installed: false);
}

/// Decoded `/v1/mesh/status` response. `detected` is one of `""`,
/// `"istio"`, `"linkerd"`, `"both"`. `isInstalled` is the gate for
/// `FeatureUnavailableState.mesh()`.
class MeshStatus {
  const MeshStatus({
    required this.detected,
    required this.istio,
    required this.linkerd,
    this.lastChecked,
  });

  /// `""` when neither mesh is detected.
  final String detected;
  final MeshInfo istio;
  final MeshInfo linkerd;
  final String? lastChecked;

  bool get isInstalled => detected.isNotEmpty;
  bool get hasIstio => detected == 'istio' || detected == 'both';
  bool get hasLinkerd => detected == 'linkerd' || detected == 'both';
  bool get hasBoth => detected == 'both';

  factory MeshStatus.fromJson(Map<String, dynamic> json) {
    final istio = json['istio'];
    final linkerd = json['linkerd'];
    return MeshStatus(
      detected: json['detected'] as String? ?? '',
      istio: istio is Map
          ? MeshInfo.fromJson(Map<String, dynamic>.from(istio))
          : MeshInfo.empty,
      linkerd: linkerd is Map
          ? MeshInfo.fromJson(Map<String, dynamic>.from(linkerd))
          : MeshInfo.empty,
      lastChecked: json['lastChecked'] as String?,
    );
  }

  static const empty = MeshStatus(
    detected: '',
    istio: MeshInfo.empty,
    linkerd: MeshInfo.empty,
  );
}

/// One HTTP match rule on a routing CRD (Istio VirtualService /
/// Linkerd HTTPRoute / etc.). Backend tag names mirrored exactly.
class RouteMatcher {
  const RouteMatcher({
    this.name,
    this.method,
    this.pathExact,
    this.pathPrefix,
    this.pathRegex,
  });

  final String? name;
  final String? method;
  final String? pathExact;
  final String? pathPrefix;
  final String? pathRegex;

  factory RouteMatcher.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return RouteMatcher(
      name: s(json['name']),
      method: s(json['method']),
      pathExact: s(json['pathExact']),
      pathPrefix: s(json['pathPrefix']),
      pathRegex: s(json['pathRegex']),
    );
  }
}

/// One destination in a route's traffic split.
class RouteDestination {
  const RouteDestination({
    this.host,
    this.subset,
    this.port,
    this.weight,
  });

  final String? host;
  final String? subset;
  final int? port;
  final int? weight;

  factory RouteDestination.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    int? n(Object? v) => (v as num?)?.toInt();
    return RouteDestination(
      host: s(json['host']),
      subset: s(json['subset']),
      port: n(json['port']),
      weight: n(json['weight']),
    );
  }
}

/// One row on the routing list / one detail response.
/// `raw` is the full unstructured spec from the CRD — used for the
/// YAML sub-panel on the detail screen.
class TrafficRoute {
  const TrafficRoute({
    required this.id,
    required this.mesh,
    required this.kind,
    required this.name,
    this.namespace = '',
    this.hosts = const <String>[],
    this.gateways = const <String>[],
    this.subsets = const <String>[],
    this.selector,
    this.matchers = const <RouteMatcher>[],
    this.destinations = const <RouteDestination>[],
    this.raw,
  });

  /// Backend-issued composite id, `mesh:namespace:kindCode:name`,
  /// URL-encoded. Round-trips through [MeshRouteId.tryParse].
  final String id;

  /// `"istio"` or `"linkerd"`. Display-only — branch tab visibility
  /// on the id prefix, not this field.
  final String mesh;

  /// Canonical CRD kind (`VirtualService`, `DestinationRule`,
  /// `ServiceProfile`, `HTTPRoute`, etc.).
  final String kind;

  final String name;
  final String namespace;

  final List<String> hosts;
  final List<String> gateways;
  final List<String> subsets;

  /// Stringified `matchLabels` for Server-like resources. Empty for
  /// VirtualService / HTTPRoute (which carry no selector).
  final String? selector;

  final List<RouteMatcher> matchers;
  final List<RouteDestination> destinations;

  /// Full unstructured spec for the YAML panel on the detail screen.
  /// Detail responses populate this; list responses leave it null.
  final Map<String, Object?>? raw;

  factory TrafficRoute.fromJson(Map<String, dynamic> json) {
    List<String> strs(Object? v) =>
        v is List ? v.whereType<String>().toList() : const <String>[];
    List<T> objs<T>(
      Object? v,
      T Function(Map<String, dynamic>) parse,
    ) {
      if (v is! List) return <T>[];
      return v
          .whereType<Map<dynamic, dynamic>>()
          .map((m) => parse(Map<String, dynamic>.from(m)))
          .toList();
    }

    final raw = json['raw'];
    return TrafficRoute(
      id: json['id'] as String? ?? '',
      mesh: json['mesh'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      hosts: strs(json['hosts']),
      gateways: strs(json['gateways']),
      subsets: strs(json['subsets']),
      selector: json['selector'] is String &&
              (json['selector'] as String).isNotEmpty
          ? json['selector'] as String
          : null,
      matchers: objs(json['matchers'], RouteMatcher.fromJson),
      destinations: objs(json['destinations'], RouteDestination.fromJson),
      raw: raw is Map ? Map<String, Object?>.from(raw) : null,
    );
  }
}

/// Decoded `/v1/mesh/routing` response envelope. `errors` carries
/// per-CRD partial fetch failures.
class RoutingResponse {
  const RoutingResponse({
    required this.status,
    required this.routes,
    required this.errors,
  });

  final MeshStatus status;
  final List<TrafficRoute> routes;

  /// Per-CRD failure map keyed by `"mesh/Kind"` (e.g.
  /// `"istio/VirtualService": "forbidden"`). Empty when every CRD list
  /// call succeeded. Renders as a banner on the list screen; never
  /// surfaces as a whole-screen error.
  final Map<String, String> errors;

  factory RoutingResponse.fromJson(Map<String, dynamic> json) {
    final status = json['status'];
    final routes = json['routes'];
    final errors = json['errors'];
    return RoutingResponse(
      status: status is Map
          ? MeshStatus.fromJson(Map<String, dynamic>.from(status))
          : MeshStatus.empty,
      routes: routes is List
          ? routes
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => TrafficRoute.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <TrafficRoute>[],
      errors: errors is Map
          ? errors.map((k, v) => MapEntry(k.toString(), v?.toString() ?? ''))
          : const <String, String>{},
    );
  }

  static const empty = RoutingResponse(
    status: MeshStatus.empty,
    routes: <TrafficRoute>[],
    errors: <String, String>{},
  );
}

/// One mesh policy row (PeerAuthentication, AuthorizationPolicy,
/// Server, MeshTLSAuthentication, etc.).
class MeshedPolicy {
  const MeshedPolicy({
    required this.id,
    required this.mesh,
    required this.kind,
    required this.name,
    this.namespace = '',
    this.action,
    this.effect,
    this.mtlsMode,
    this.selector,
    this.ruleCount = 0,
    this.raw,
  });

  final String id;
  final String mesh;
  final String kind;
  final String name;
  final String namespace;

  /// AuthorizationPolicy: `ALLOW`, `DENY`, `AUDIT`.
  final String? action;

  /// Backend-computed disposition: `deny_all`, `allow_all`, or empty.
  final String? effect;

  /// PeerAuthentication: `STRICT`, `PERMISSIVE`, `DISABLE`, `UNSET`.
  final String? mtlsMode;

  /// Stringified matchLabels selector.
  final String? selector;

  final int ruleCount;
  final Map<String, Object?>? raw;

  factory MeshedPolicy.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    final raw = json['raw'];
    return MeshedPolicy(
      id: json['id'] as String? ?? '',
      mesh: json['mesh'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      action: s(json['action']),
      effect: s(json['effect']),
      mtlsMode: s(json['mtlsMode']),
      selector: s(json['selector']),
      ruleCount: (json['ruleCount'] as num?)?.toInt() ?? 0,
      raw: raw is Map ? Map<String, Object?>.from(raw) : null,
    );
  }
}

class PoliciesResponse {
  const PoliciesResponse({
    required this.status,
    required this.policies,
    required this.errors,
  });

  final MeshStatus status;
  final List<MeshedPolicy> policies;
  final Map<String, String> errors;

  factory PoliciesResponse.fromJson(Map<String, dynamic> json) {
    final status = json['status'];
    final policies = json['policies'];
    final errors = json['errors'];
    return PoliciesResponse(
      status: status is Map
          ? MeshStatus.fromJson(Map<String, dynamic>.from(status))
          : MeshStatus.empty,
      policies: policies is List
          ? policies
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => MeshedPolicy.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <MeshedPolicy>[],
      errors: errors is Map
          ? errors.map((k, v) => MapEntry(k.toString(), v?.toString() ?? ''))
          : const <String, String>{},
    );
  }

  static const empty = PoliciesResponse(
    status: MeshStatus.empty,
    policies: <MeshedPolicy>[],
    errors: <String, String>{},
  );
}

/// Per-workload mTLS posture row.
class WorkloadMTLS {
  const WorkloadMTLS({
    required this.namespace,
    required this.workload,
    required this.mesh,
    required this.state,
    required this.source,
    this.workloadKind,
    this.istioMode,
    this.sourceDetail,
    this.workloadKindConfident = true,
  });

  final String namespace;
  final String workload;

  /// `"Deployment"`, `"StatefulSet"`, etc. Empty when unmeshed or when
  /// the backend's owner-reference walk found no owning workload.
  final String? workloadKind;
  final String mesh;

  /// `"active" | "inactive" | "mixed" | "unmeshed"`. Mobile renders this
  /// verbatim — unknown values fall back to the muted text colour.
  final String state;

  /// `"policy" | "metric" | "default"`. Drives which of the three
  /// attribution sources lit up.
  final String source;

  /// Istio-only PeerAuthentication mode after resolution.
  final String? istioMode;

  /// `"workload" | "namespace" | "mesh"` — which PeerAuthentication
  /// scope ultimately won. Empty for Linkerd or metric-driven rows.
  final String? sourceDetail;

  /// `false` when the kind was inferred from a ReplicaSet name heuristic
  /// (no owner-reference lookup landed). Surface with an asterisk +
  /// tooltip on the detail screen.
  final bool workloadKindConfident;

  factory WorkloadMTLS.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return WorkloadMTLS(
      namespace: json['namespace'] as String? ?? '',
      workload: json['workload'] as String? ?? '',
      workloadKind: s(json['workloadKind']),
      mesh: json['mesh'] as String? ?? '',
      state: json['state'] as String? ?? '',
      source: json['source'] as String? ?? '',
      istioMode: s(json['istioMode']),
      sourceDetail: s(json['sourceDetail']),
      workloadKindConfident: json['workloadKindConfident'] as bool? ?? true,
    );
  }
}

class MTLSPostureResponse {
  const MTLSPostureResponse({
    required this.status,
    required this.workloads,
    required this.errors,
  });

  final MeshStatus status;
  final List<WorkloadMTLS> workloads;

  /// Partial-failure map. Keys to watch for:
  ///   `prometheus-cross-check` / `truncated` — warning-level (yellow);
  ///   `pods` / `policies` / `istio/...` / `linkerd/...` — error-level.
  final Map<String, String> errors;

  factory MTLSPostureResponse.fromJson(Map<String, dynamic> json) {
    final status = json['status'];
    final workloads = json['workloads'];
    final errors = json['errors'];
    return MTLSPostureResponse(
      status: status is Map
          ? MeshStatus.fromJson(Map<String, dynamic>.from(status))
          : MeshStatus.empty,
      workloads: workloads is List
          ? workloads
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => WorkloadMTLS.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <WorkloadMTLS>[],
      errors: errors is Map
          ? errors.map((k, v) => MapEntry(k.toString(), v?.toString() ?? ''))
          : const <String, String>{},
    );
  }

  static const empty = MTLSPostureResponse(
    status: MeshStatus.empty,
    workloads: <WorkloadMTLS>[],
    errors: <String, String>{},
  );
}

/// Point-in-time golden-signal scalars for a single service.
///
/// The backend exposes scalars, NOT time series — the chart renderer
/// on mobile is a tile grid, not a LineChart. The plan's "4-up
/// LineChart" wording predates the backend audit; this struct is the
/// source of truth.
class GoldenSignals {
  const GoldenSignals({
    required this.mesh,
    required this.namespace,
    required this.service,
    required this.available,
    this.reason,
    this.missingQueries = const <String>[],
    this.rps = 0.0,
    this.errorRate = 0.0,
    this.p50Ms = 0.0,
    this.p95Ms = 0.0,
    this.p99Ms = 0.0,
  });

  final String mesh;
  final String namespace;
  final String service;

  /// `false` when Prometheus is offline. [reason] carries the operator-
  /// visible explanation in that case.
  final bool available;
  final String? reason;

  /// Names of failed PromQL queries (e.g. `["p99", "errorRate"]`).
  /// Non-empty when partial-success — surfaced as a banner above the
  /// tile grid; the failed tiles render an em-dash.
  final List<String> missingQueries;

  /// Requests per second.
  final double rps;

  /// Error rate as a fraction in `[0, 1]`. UI multiplies by 100.
  final double errorRate;

  final double p50Ms;
  final double p95Ms;
  final double p99Ms;

  bool isMetricMissing(String name) => missingQueries.contains(name);

  factory GoldenSignals.fromJson(Map<String, dynamic> json) {
    double n(Object? v) => (v as num?)?.toDouble() ?? 0.0;
    return GoldenSignals(
      mesh: json['mesh'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      service: json['service'] as String? ?? '',
      available: json['available'] as bool? ?? false,
      reason: json['reason'] is String && (json['reason'] as String).isNotEmpty
          ? json['reason'] as String
          : null,
      missingQueries: json['missingQueries'] is List
          ? (json['missingQueries'] as List).whereType<String>().toList()
          : const <String>[],
      rps: n(json['rps']),
      errorRate: n(json['errorRate']),
      p50Ms: n(json['p50Ms']),
      p95Ms: n(json['p95Ms']),
      p99Ms: n(json['p99Ms']),
    );
  }
}

class GoldenSignalsResponse {
  const GoldenSignalsResponse({
    required this.status,
    required this.signals,
  });

  final MeshStatus status;
  final GoldenSignals signals;

  factory GoldenSignalsResponse.fromJson(Map<String, dynamic> json) {
    final status = json['status'];
    final signals = json['signals'];
    return GoldenSignalsResponse(
      status: status is Map
          ? MeshStatus.fromJson(Map<String, dynamic>.from(status))
          : MeshStatus.empty,
      signals: signals is Map
          ? GoldenSignals.fromJson(Map<String, dynamic>.from(signals))
          : GoldenSignals(
              mesh: '',
              namespace: '',
              service: '',
              available: false,
            ),
    );
  }
}

/// Mobile wrapper over `/v1/mesh/*`. Stateless — cluster pinning
/// threads through `clusterIdOverride` so the wire header always
/// matches the family-key slot the caller writes back into.
class MeshRepository {
  MeshRepository(this._dio);

  final Dio _dio;

  /// Fetches mesh discovery status. Returns [MeshStatus.empty] on 5xx
  /// so callers route straight to `FeatureUnavailableState.mesh()` —
  /// a flaky reverse-proxy probe should not surface as an error card.
  Future<MeshStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/mesh/status',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        // Backend wraps the response payload as `{"status": {...}}` on
        // top of the standard `{"data": {...}}` envelope. The
        // routing/policies/mtls endpoints all share the same shape.
        final inner = data['status'];
        if (inner is Map) {
          return MeshStatus.fromJson(Map<String, dynamic>.from(inner));
        }
        // Some test harnesses skip the inner envelope. Tolerate.
        return MeshStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return MeshStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return MeshStatus.empty;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Future<RoutingResponse> listRouting({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    return _fetchEnvelope<RoutingResponse>(
      path: '/api/v1/mesh/routing',
      query: namespace == null || namespace.isEmpty
          ? null
          : {'namespace': namespace},
      parse: RoutingResponse.fromJson,
      empty: RoutingResponse.empty,
      clusterIdOverride: clusterIdOverride,
      cancelToken: cancelToken,
    );
  }

  /// Fetches a single routing CRD by composite id. The id is
  /// URL-encoded here once — callers pass the raw
  /// `mesh:ns:kindCode:name` string. Double-encoding via
  /// [MeshRouteId.encode] on the caller side would corrupt the path.
  Future<TrafficRoute> getRoute({
    required String id,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/mesh/routing/${Uri.encodeComponent(id)}',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        // Detail handler returns either `{"route": {...}}` or the raw
        // TrafficRoute object depending on the route. Tolerate both.
        final inner = data['route'];
        final src = inner is Map ? inner : data;
        return TrafficRoute.fromJson(Map<String, dynamic>.from(src));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for route $id',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Future<PoliciesResponse> listPolicies({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    return _fetchEnvelope<PoliciesResponse>(
      path: '/api/v1/mesh/policies',
      query: namespace == null || namespace.isEmpty
          ? null
          : {'namespace': namespace},
      parse: PoliciesResponse.fromJson,
      empty: PoliciesResponse.empty,
      clusterIdOverride: clusterIdOverride,
      cancelToken: cancelToken,
    );
  }

  /// Fetches mTLS posture for a namespace. Backend hard-requires
  /// `?namespace=`; passing empty produces a 400 which surfaces as an
  /// inline error on the namespace selector.
  Future<MTLSPostureResponse> mtlsPosture({
    required String namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    return _fetchEnvelope<MTLSPostureResponse>(
      path: '/api/v1/mesh/mtls',
      query: {'namespace': namespace},
      parse: MTLSPostureResponse.fromJson,
      empty: MTLSPostureResponse.empty,
      clusterIdOverride: clusterIdOverride,
      cancelToken: cancelToken,
    );
  }

  /// Fetches golden signals for a single service. `mesh` is required
  /// when both meshes are installed; otherwise the backend infers it.
  /// Backend timeout is 2s — partial-success (some missing queries) is
  /// the common case, not the exceptional one.
  Future<GoldenSignalsResponse> goldenSignals({
    required String namespace,
    required String service,
    String? mesh,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    final query = <String, String>{
      'namespace': namespace,
      'service': service,
      if (mesh != null && mesh.isNotEmpty) 'mesh': mesh,
    };
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/mesh/golden-signals',
        queryParameters: query,
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return GoldenSignalsResponse.fromJson(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty golden-signals response',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Shared envelope unwrapping for the four list-style endpoints
  /// (routing, policies, mtls — and any future addition that follows
  /// the `{"data": {...}}` shape). Centralised so a backend envelope
  /// change touches one place instead of four.
  Future<T> _fetchEnvelope<T>({
    required String path,
    Map<String, String>? query,
    required T Function(Map<String, dynamic>) parse,
    required T empty,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        queryParameters: query,
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return parse(Map<String, dynamic>.from(data));
      }
      return empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final meshRepositoryProvider = Provider<MeshRepository>((ref) {
  return MeshRepository(ref.watch(dioProvider));
});

/// Per-cluster mesh discovery status. Drives `FeatureUnavailableState
/// .mesh()` and decides which engine cards render on the dashboard.
final meshStatusProvider = FutureProvider.autoDispose
    .family<MeshStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('mesh status invalidated');
  });
  return ref.read(meshRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the routing list provider. Carries both cluster id
/// (so cluster switches force a fresh slot) and namespace (so two open
/// routing-list screens with different namespace filters don't share
/// state).
class MeshRoutingKey {
  const MeshRoutingKey({required this.clusterId, this.namespace});

  final String clusterId;

  /// Empty / null → cluster-wide list. The handler enforces RBAC.
  final String? namespace;

  @override
  bool operator ==(Object other) =>
      other is MeshRoutingKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

final meshRoutingProvider = FutureProvider.autoDispose
    .family<RoutingResponse, MeshRoutingKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('mesh routing invalidated');
  });
  return ref.read(meshRepositoryProvider).listRouting(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

class MeshRouteDetailKey {
  const MeshRouteDetailKey({required this.clusterId, required this.id});

  final String clusterId;
  final String id;

  @override
  bool operator ==(Object other) =>
      other is MeshRouteDetailKey &&
      other.clusterId == clusterId &&
      other.id == id;

  @override
  int get hashCode => Object.hash(clusterId, id);
}

final meshRouteDetailProvider = FutureProvider.autoDispose
    .family<TrafficRoute, MeshRouteDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('mesh route detail invalidated');
  });
  return ref.read(meshRepositoryProvider).getRoute(
        id: key.id,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the mTLS posture provider. Namespace is required
/// (mTLS posture is only meaningful per namespace) so the key carries
/// it as a non-nullable field.
class MeshMtlsKey {
  const MeshMtlsKey({required this.clusterId, required this.namespace});

  final String clusterId;
  final String namespace;

  @override
  bool operator ==(Object other) =>
      other is MeshMtlsKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

final meshMtlsPostureProvider = FutureProvider.autoDispose
    .family<MTLSPostureResponse, MeshMtlsKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('mesh mtls invalidated');
  });
  return ref.read(meshRepositoryProvider).mtlsPosture(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the golden-signals provider. Carries every parameter
/// the wire request needs so a service swap on the same screen produces
/// a fresh cache slot rather than overwriting the prior one.
class MeshGoldenSignalsKey {
  const MeshGoldenSignalsKey({
    required this.clusterId,
    required this.namespace,
    required this.service,
    this.mesh,
  });

  final String clusterId;
  final String namespace;
  final String service;

  /// `"istio"` or `"linkerd"` when both meshes are installed. Null
  /// when only one is installed (backend infers it).
  final String? mesh;

  @override
  bool operator ==(Object other) =>
      other is MeshGoldenSignalsKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.service == service &&
      other.mesh == mesh;

  @override
  int get hashCode => Object.hash(clusterId, namespace, service, mesh);
}

final meshGoldenSignalsProvider = FutureProvider.autoDispose
    .family<GoldenSignalsResponse, MeshGoldenSignalsKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('mesh golden-signals invalidated');
  });
  return ref.read(meshRepositoryProvider).goldenSignals(
        namespace: key.namespace,
        service: key.service,
        mesh: key.mesh,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
