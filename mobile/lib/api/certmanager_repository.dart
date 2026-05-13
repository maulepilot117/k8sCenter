// Mobile-side wrapper over the backend cert-manager API at
// `/v1/certificates/{status,certificates,certificates/{ns}/{name},issuers,
// clusterissuers,expiring}` plus the two write verbs
// `POST .../certificates/{ns}/{name}/{renew,reissue}`.
// Wire types mirror `backend/internal/certmanager/types.go` exactly.
// Web parallel: `frontend/islands/CertificatesList.tsx`,
// `frontend/islands/CertificateDetail.tsx`, `frontend/islands/IssuersList.tsx`,
// `frontend/lib/certmanager-types.ts`.
//
// Threshold attribution: every cert response carries a resolved
// `warningThresholdDays` + `criticalThresholdDays` pair plus per-key
// `warningThresholdSource` / `criticalThresholdSource` enums. When the
// resolved warn/crit pair would have violated `crit < warn`, the backend
// falls back to package defaults and sets `thresholdConflict: true`. The
// detail screen surfaces this verbatim so operators see "Conflict —
// using defaults" rather than a misleading "Default" attribution.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards it
// as an explicit `X-Cluster-ID` header. The PR-3c interceptor invariant
// (only injects when absent) is the live contract.
//
// Renew + Reissue (write verbs) live here rather than in
// `resource_actions.dart` because the web side does the same — the
// cert-manager handler exposes them under `/v1/certificates/...` rather
// than the generic `/v1/resources/...` action endpoint. Adding them to
// `ActionId` would force a fork between the wire path and every other
// kind's action. Mirroring the web's `apiPost('/v1/certificates/...')`
// pattern keeps the two surfaces isomorphic per R10.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// One of the four ThresholdSource enum constants from
/// `backend/internal/certmanager/types.go`. Unknown / missing values map
/// to [unknown] so callers exhaustively switch without runtime surprises.
enum ThresholdSource {
  /// Resolution fell through to the package default (warn=30d, crit=7d)
  /// — no annotation present anywhere in the chain.
  packageDefault,

  /// Annotation set directly on the Certificate.
  certificate,

  /// Annotation set on the referenced Issuer.
  issuer,

  /// Annotation set on the referenced ClusterIssuer.
  clusterIssuer,

  /// Field not populated by the backend (older response, or never
  /// resolved). UI renders as "Default" so operators don't see a blank.
  unknown,
}

ThresholdSource _thresholdSourceFromJson(Object? v) {
  if (v is! String) return ThresholdSource.unknown;
  switch (v) {
    case 'default':
      return ThresholdSource.packageDefault;
    case 'certificate':
      return ThresholdSource.certificate;
    case 'issuer':
      return ThresholdSource.issuer;
    case 'clusterissuer':
      return ThresholdSource.clusterIssuer;
    default:
      return ThresholdSource.unknown;
  }
}

/// Lifecycle state of a Certificate.
///
/// Mirrors `Status` in `backend/internal/certmanager/types.go`. The
/// backend's enum is open — older responses (or future cert-manager
/// versions adding new conditions) may emit values we don't know about,
/// so [unknown] is the explicit fallback rather than crashing the parser.
enum CertStatus {
  ready,
  issuing,
  failed,
  expiring,
  expired,
  unknown,
}

CertStatus _certStatusFromJson(Object? v) {
  if (v is! String) return CertStatus.unknown;
  switch (v) {
    case 'Ready':
      return CertStatus.ready;
    case 'Issuing':
      return CertStatus.issuing;
    case 'Failed':
      return CertStatus.failed;
    case 'Expiring':
      return CertStatus.expiring;
    case 'Expired':
      return CertStatus.expired;
    default:
      return CertStatus.unknown;
  }
}

/// Human-facing label for [CertStatus] — used by status pills + filter
/// chips. Capitalised to match the wire enum exactly so screenshots
/// across web + mobile read identically.
String certStatusLabel(CertStatus s) => switch (s) {
      CertStatus.ready => 'Ready',
      CertStatus.issuing => 'Issuing',
      CertStatus.failed => 'Failed',
      CertStatus.expiring => 'Expiring',
      CertStatus.expired => 'Expired',
      CertStatus.unknown => 'Unknown',
    };

/// Decoded `/v1/certificates/status` response. Mirrors
/// `CertManagerStatus` in `backend/internal/certmanager/types.go`.
class CertManagerStatus {
  const CertManagerStatus({
    required this.detected,
    this.namespace,
    this.version,
    this.lastChecked = '',
  });

  final bool detected;
  final String? namespace;
  final String? version;

  /// RFC-3339 timestamp of the last discovery probe. Empty when not yet
  /// available (first poll hasn't completed).
  final String lastChecked;

  factory CertManagerStatus.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return CertManagerStatus(
      detected: json['detected'] as bool? ?? false,
      namespace: s(json['namespace']),
      version: s(json['version']),
      lastChecked: json['lastChecked'] as String? ?? '',
    );
  }

  static const empty = CertManagerStatus(detected: false);
}

/// Reference from a Certificate (or CertificateRequest) to its signing
/// Issuer / ClusterIssuer.
class IssuerRef {
  const IssuerRef({required this.name, required this.kind, this.group = ''});

  final String name;

  /// `"Issuer"` or `"ClusterIssuer"`.
  final String kind;

  /// API group; `"cert-manager.io"` in practice. Empty when the backend
  /// elides the field (older responses).
  final String group;

  factory IssuerRef.fromJson(Map<String, dynamic> json) => IssuerRef(
        name: json['name'] as String? ?? '',
        kind: json['kind'] as String? ?? '',
        group: json['group'] as String? ?? '',
      );
}

/// One certificate row on the list endpoints, or the `certificate` field
/// of a [CertificateDetail] response.
class Certificate {
  const Certificate({
    required this.name,
    required this.namespace,
    required this.status,
    required this.issuerRef,
    required this.secretName,
    required this.uid,
    this.reason,
    this.message,
    this.dnsNames = const <String>[],
    this.commonName,
    this.duration,
    this.renewBefore,
    this.notBefore,
    this.notAfter,
    this.renewalTime,
    this.daysRemaining,
    this.warningThresholdDays,
    this.criticalThresholdDays,
    this.thresholdSource = ThresholdSource.unknown,
    this.warningThresholdSource = ThresholdSource.unknown,
    this.criticalThresholdSource = ThresholdSource.unknown,
    this.thresholdConflict = false,
  });

  final String name;
  final String namespace;
  final CertStatus status;
  final String? reason;
  final String? message;
  final IssuerRef issuerRef;
  final String secretName;
  final List<String> dnsNames;
  final String? commonName;

  /// Duration / renewBefore are emitted as Go-format duration strings
  /// (e.g. `"2160h0m0s"`). The detail screen renders verbatim — operators
  /// who care about the value are reading raw spec data anyway.
  final String? duration;
  final String? renewBefore;

  /// RFC-3339 timestamps. Stored as ISO strings (not parsed into
  /// DateTime) because every UI use-case is either render-as-localized
  /// text or "compare to backend-supplied daysRemaining" — neither
  /// requires arithmetic on the mobile side.
  final String? notBefore;
  final String? notAfter;
  final String? renewalTime;

  /// Days until [notAfter]. `null` when the cert hasn't been signed yet
  /// (Issuing without a signed certificate) — distinct from `0` which
  /// means "expires today / already expired".
  final int? daysRemaining;

  /// Resolved warn/critical thresholds after `ApplyThresholds`. Null when
  /// the backend response didn't run the resolver (older callers, or the
  /// /expiring endpoint which emits a different shape).
  final int? warningThresholdDays;
  final int? criticalThresholdDays;

  /// Aggregate "strongest contributing layer" from the resolution chain.
  /// Per-key attribution lives in [warningThresholdSource] /
  /// [criticalThresholdSource] — a cert can override warn alone and
  /// inherit critical from its issuer.
  final ThresholdSource thresholdSource;
  final ThresholdSource warningThresholdSource;
  final ThresholdSource criticalThresholdSource;

  /// True when the operator's resolved warn/crit pair would have
  /// violated `crit < warn`. The resolver fell back to defaults but the
  /// UI surfaces the conflict so operators see why "Default" appeared.
  final bool thresholdConflict;

  final String uid;

  factory Certificate.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    // Defensive cast: accept num (canonical), parse String (forward-compat /
    // mock-backend / replay-proxy resilience), reject everything else by
    // returning null rather than throwing. Mirrors the s() helper above.
    int? n(Object? v) {
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v);
      return null;
    }
    final dnsRaw = json['dnsNames'];
    return Certificate(
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      status: _certStatusFromJson(json['status']),
      reason: s(json['reason']),
      message: s(json['message']),
      issuerRef: json['issuerRef'] is Map
          ? IssuerRef.fromJson(
              Map<String, dynamic>.from(json['issuerRef'] as Map),
            )
          : const IssuerRef(name: '', kind: ''),
      secretName: json['secretName'] as String? ?? '',
      dnsNames: dnsRaw is List
          ? dnsRaw.whereType<String>().toList()
          : const <String>[],
      commonName: s(json['commonName']),
      duration: s(json['duration']),
      renewBefore: s(json['renewBefore']),
      notBefore: s(json['notBefore']),
      notAfter: s(json['notAfter']),
      renewalTime: s(json['renewalTime']),
      daysRemaining: n(json['daysRemaining']),
      warningThresholdDays: n(json['warningThresholdDays']),
      criticalThresholdDays: n(json['criticalThresholdDays']),
      thresholdSource: _thresholdSourceFromJson(json['thresholdSource']),
      warningThresholdSource:
          _thresholdSourceFromJson(json['warningThresholdSource']),
      criticalThresholdSource:
          _thresholdSourceFromJson(json['criticalThresholdSource']),
      thresholdConflict: json['thresholdConflict'] as bool? ?? false,
      uid: json['uid'] as String? ?? '',
    );
  }
}

/// One issuer row — represents either a namespaced Issuer or a
/// ClusterIssuer (discriminated by [scope]).
class Issuer {
  const Issuer({
    required this.name,
    required this.scope,
    required this.type,
    required this.ready,
    required this.uid,
    this.namespace = '',
    this.reason,
    this.message,
    this.acmeEmail,
    this.acmeServer,
    this.warningThresholdDays,
    this.criticalThresholdDays,
    this.updatedAt = '',
  });

  final String name;

  /// Empty for ClusterIssuers.
  final String namespace;

  /// `"Namespaced"` or `"Cluster"`. Matches the backend enum literally so
  /// the detail screen's source attribution renders identically to web.
  final String scope;

  /// `"ACME"`, `"CA"`, `"Vault"`, `"SelfSigned"`, `"Unknown"`. Open enum
  /// — future cert-manager versions may add types.
  final String type;
  final bool ready;
  final String? reason;
  final String? message;

  /// ACME-only.
  final String? acmeEmail;
  final String? acmeServer;

  /// Annotation-set threshold overrides on this issuer. Null means "not
  /// set on this issuer"; the resolution falls through to clusterissuer
  /// (for namespaced issuers) or the package default.
  final int? warningThresholdDays;
  final int? criticalThresholdDays;

  final String uid;
  final String updatedAt;

  bool get isCluster => scope == 'Cluster';

  factory Issuer.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    // Defensive cast: accept num (canonical), parse String (forward-compat /
    // mock-backend / replay-proxy resilience), reject everything else by
    // returning null rather than throwing. Mirrors the s() helper above.
    int? n(Object? v) {
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v);
      return null;
    }
    return Issuer(
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      scope: json['scope'] as String? ?? '',
      type: json['type'] as String? ?? '',
      ready: json['ready'] as bool? ?? false,
      reason: s(json['reason']),
      message: s(json['message']),
      acmeEmail: s(json['acmeEmail']),
      acmeServer: s(json['acmeServer']),
      warningThresholdDays: n(json['warningThresholdDays']),
      criticalThresholdDays: n(json['criticalThresholdDays']),
      uid: json['uid'] as String? ?? '',
      updatedAt: json['updatedAt'] as String? ?? '',
    );
  }
}

/// One CertificateRequest sub-resource row on a [CertificateDetail].
class CertificateRequestRow {
  const CertificateRequestRow({
    required this.name,
    required this.namespace,
    required this.status,
    required this.issuerRef,
    required this.createdAt,
    required this.uid,
    this.reason,
    this.message,
    this.finishedAt,
  });

  final String name;
  final String namespace;
  final CertStatus status;
  final String? reason;
  final String? message;
  final IssuerRef issuerRef;
  final String createdAt;
  final String? finishedAt;
  final String uid;

  factory CertificateRequestRow.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return CertificateRequestRow(
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      status: _certStatusFromJson(json['status']),
      reason: s(json['reason']),
      message: s(json['message']),
      issuerRef: json['issuerRef'] is Map
          ? IssuerRef.fromJson(
              Map<String, dynamic>.from(json['issuerRef'] as Map),
            )
          : const IssuerRef(name: '', kind: ''),
      createdAt: json['createdAt'] as String? ?? '',
      finishedAt: s(json['finishedAt']),
      uid: json['uid'] as String? ?? '',
    );
  }
}

/// One ACME Order sub-resource row.
class OrderRow {
  const OrderRow({
    required this.name,
    required this.namespace,
    required this.state,
    required this.createdAt,
    required this.uid,
    this.reason,
    this.crName,
  });

  final String name;
  final String namespace;

  /// Open enum — cert-manager Order phase strings (`pending`, `valid`,
  /// `ready`, `processing`, `invalid`, `expired`, `errored`).
  final String state;
  final String? reason;
  final String createdAt;
  final String uid;
  final String? crName;

  factory OrderRow.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return OrderRow(
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      state: json['state'] as String? ?? '',
      reason: s(json['reason']),
      createdAt: json['createdAt'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      crName: s(json['crName']),
    );
  }
}

/// One ACME Challenge sub-resource row.
class ChallengeRow {
  const ChallengeRow({
    required this.name,
    required this.namespace,
    required this.type,
    required this.state,
    required this.createdAt,
    required this.uid,
    this.reason,
    this.dnsName,
    this.orderName,
  });

  final String name;
  final String namespace;

  /// `"HTTP-01"` or `"DNS-01"`.
  final String type;
  final String state;
  final String? reason;
  final String? dnsName;
  final String createdAt;
  final String uid;
  final String? orderName;

  factory ChallengeRow.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return ChallengeRow(
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      type: json['type'] as String? ?? '',
      state: json['state'] as String? ?? '',
      reason: s(json['reason']),
      dnsName: s(json['dnsName']),
      createdAt: json['createdAt'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      orderName: s(json['orderName']),
    );
  }
}

/// Aggregated detail response — one [Certificate] plus the
/// CertificateRequest / Order / Challenge sub-resources scoped to it.
///
/// Sub-resource arrays may be empty when:
///   - The cert never had a renewal cycle yet (no CRs).
///   - The cert uses a non-ACME Issuer (no Orders, no Challenges).
///   - RBAC denies the impersonated user sub-resource visibility (a
///     common case on namespace-scoped operators). The detail screen
///     surfaces an "Some sub-resources may be hidden by RBAC" hint when
///     the cert is in Issuing / Failed state but no CRs are present.
class CertificateDetail {
  const CertificateDetail({
    required this.certificate,
    this.certificateRequests = const <CertificateRequestRow>[],
    this.orders = const <OrderRow>[],
    this.challenges = const <ChallengeRow>[],
  });

  final Certificate certificate;
  final List<CertificateRequestRow> certificateRequests;
  final List<OrderRow> orders;
  final List<ChallengeRow> challenges;

  factory CertificateDetail.fromJson(Map<String, dynamic> json) {
    final cert = json['certificate'];
    List<T> rows<T>(
      Object? raw,
      T Function(Map<String, dynamic>) parse,
    ) {
      if (raw is! List) return <T>[];
      return raw
          .whereType<Map<dynamic, dynamic>>()
          .map((m) => parse(Map<String, dynamic>.from(m)))
          .toList();
    }

    return CertificateDetail(
      certificate: cert is Map
          ? Certificate.fromJson(Map<String, dynamic>.from(cert))
          : const Certificate(
              name: '',
              namespace: '',
              status: CertStatus.unknown,
              issuerRef: IssuerRef(name: '', kind: ''),
              secretName: '',
              uid: '',
            ),
      certificateRequests:
          rows(json['certificateRequests'], CertificateRequestRow.fromJson),
      orders: rows(json['orders'], OrderRow.fromJson),
      challenges: rows(json['challenges'], ChallengeRow.fromJson),
    );
  }
}

/// One row of the `/v1/certificates/expiring` response. Distinct from
/// [Certificate] because the backend emits a smaller summary shape here —
/// only the fields the expiry notification UI needs.
class ExpiringCertificate {
  const ExpiringCertificate({
    required this.namespace,
    required this.name,
    required this.uid,
    required this.issuerName,
    required this.secretName,
    required this.notAfter,
    required this.daysRemaining,
    required this.severity,
  });

  final String namespace;
  final String name;
  final String uid;
  final String issuerName;
  final String secretName;

  /// RFC-3339 timestamp; rendered with `toLocaleString` equivalent on
  /// the mobile side.
  final String notAfter;
  final int daysRemaining;

  /// `"warning"`, `"critical"`, or `"expired"`. The Go backend currently
  /// emits only the first two; the web TS type allows `"expired"` for
  /// forward-compatibility. The mobile parser accepts all three and
  /// preserves anything else verbatim (forward-compat with future
  /// cert-manager versions).
  final String severity;

  factory ExpiringCertificate.fromJson(Map<String, dynamic> json) {
    // Defensive numeric cast — matches the n() helper used in Certificate
    // and Issuer parsing above. Accepts num (canonical), parses String
    // (forward-compat / mock-backend resilience), falls back to 0 for
    // anything else rather than throwing — a single corrupt field on one
    // row must not collapse the entire list response into an error state.
    int days(Object? v) {
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v) ?? 0;
      return 0;
    }

    return ExpiringCertificate(
      namespace: json['namespace'] as String? ?? '',
      name: json['name'] as String? ?? '',
      uid: json['uid'] as String? ?? '',
      issuerName: json['issuerName'] as String? ?? '',
      secretName: json['secretName'] as String? ?? '',
      notAfter: json['notAfter'] as String? ?? '',
      daysRemaining: days(json['daysRemaining']),
      severity: json['severity'] as String? ?? 'warning',
    );
  }
}

/// Successful response from POST renew / reissue. Backend emits HTTP 202
/// with `{"status": "renewing"}` or `{"status": "reissuing"}`. Mobile
/// surfaces the verb verbatim so the snackbar copy is identical to web.
class CertActionResult {
  const CertActionResult({required this.status});
  final String status;
}

/// Mobile wrapper over `/v1/certificates/*`. Stateless — cluster pinning
/// threads through `clusterIdOverride` so the wire header always matches
/// the family-key slot the caller writes back into.
class CertManagerRepository {
  CertManagerRepository(this._dio);

  final Dio _dio;

  /// Fetches cert-manager discovery status. Returns
  /// [CertManagerStatus.empty] on 5xx so callers route straight to
  /// `FeatureUnavailableState.certManager()` — a flaky reverse-proxy
  /// probe should not surface as an error card.
  Future<CertManagerStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/certificates/status',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return CertManagerStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return CertManagerStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return CertManagerStatus.empty;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists every Certificate the impersonated user can see across all
  /// namespaces. `namespace` filter is applied client-side on the
  /// backend; mobile threads it through as a query param when set.
  Future<List<Certificate>> listCertificates({
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<Certificate>(
        path: '/api/v1/certificates/certificates',
        query: (namespace != null && namespace.isNotEmpty)
            ? {'namespace': namespace}
            : null,
        parse: Certificate.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Fetches a single Certificate detail (with sub-resources). 404s
  /// surface as [ApiError] so the detail screen renders a clear "not
  /// found" message rather than an empty cert.
  Future<CertificateDetail> getCertificate({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/certificates/certificates/'
        '${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return CertificateDetail.fromJson(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for certificate $namespace/$name',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists namespaced Issuers visible to the impersonated user.
  Future<List<Issuer>> listIssuers({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<Issuer>(
        path: '/api/v1/certificates/issuers',
        parse: Issuer.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists ClusterIssuers. Empty list (not 403) when the impersonated
  /// user lacks cluster-scoped `get clusterissuers` — backend silently
  /// drops them so non-admin operators don't see a noisy permission
  /// failure on every screen mount.
  Future<List<Issuer>> listClusterIssuers({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<Issuer>(
        path: '/api/v1/certificates/clusterissuers',
        parse: Issuer.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Lists certificates currently in the warn or critical bucket per
  /// their resolved thresholds. Backend sorts ascending by
  /// `daysRemaining`.
  Future<List<ExpiringCertificate>> listExpiring({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _fetchList<ExpiringCertificate>(
        path: '/api/v1/certificates/expiring',
        parse: ExpiringCertificate.fromJson,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Triggers cert renewal. Backend returns HTTP 202 with
  /// `{"status": "renewing"}` on success. Failures bubble as [ApiError].
  /// 403 on missing `patch certificates` RBAC; 500 on cert-manager
  /// status-subresource update failure.
  Future<CertActionResult> renew({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _postCertAction(
        verb: 'renew',
        namespace: namespace,
        name: name,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  /// Triggers cert re-issuance by deleting the backing Secret. Backend
  /// validates Secret ownership by `ownerReference.UID` matching the
  /// Certificate's UID, so unrelated Secrets cannot be deleted via this
  /// path even by an operator with secret delete RBAC. Returns 202 with
  /// `{"status": "reissuing"}`.
  Future<CertActionResult> reissue({
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) =>
      _postCertAction(
        verb: 'reissue',
        namespace: namespace,
        name: name,
        clusterIdOverride: clusterIdOverride,
        cancelToken: cancelToken,
      );

  // ---------------------------------------------------------------------------
  // Internal helpers
  // ---------------------------------------------------------------------------

  /// Shared envelope unwrapping for the four list endpoints. Backend
  /// writes a flat array into the `{"data": [...]}` envelope — no inner
  /// kind-specific key. Returns an empty list when the envelope is
  /// missing or the wrong shape rather than throwing, because the
  /// status endpoint already gates the "feature not installed" case
  /// upstream; an empty list on a healthy cluster is the natural
  /// "nothing matches" state.
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
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
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

  Future<CertActionResult> _postCertAction({
    required String verb,
    required String namespace,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.post<Map<String, dynamic>>(
        '/api/v1/certificates/certificates/'
        '${Uri.encodeComponent(namespace)}/'
        '${Uri.encodeComponent(name)}/$verb',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      final status = data is Map ? data['status'] as String? : null;
      // Default to the canonical present-continuous form when the
      // backend response shape unexpectedly elides the status field —
      // matches the backend's literal so the snackbar copy is identical
      // even on a body-less 202. Naive string suffix concat is wrong
      // ("reissue" + "ing" = "reissueing"), so the map is explicit.
      const verbDefault = {
        'renew': 'renewing',
        'reissue': 'reissuing',
      };
      return CertActionResult(
        status: status ?? verbDefault[verb] ?? verb,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final certManagerRepositoryProvider = Provider<CertManagerRepository>((ref) {
  return CertManagerRepository(ref.watch(dioProvider));
});

/// Per-cluster cert-manager discovery status. Drives
/// `FeatureUnavailableState.certManager()` on every cert-manager surface
/// and decides whether the drawer entries render with a "not installed"
/// landing state.
final certManagerStatusProvider = FutureProvider.autoDispose
    .family<CertManagerStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('cert-manager status invalidated');
  });
  return ref.read(certManagerRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the certificates list provider. Carries both cluster
/// id (so cluster switches force a fresh slot) and namespace (so two
/// open list screens with different namespace filters don't share
/// state).
class CertificateListKey {
  /// Normalise [namespace]: empty string is treated as null so
  /// `CertificateListKey(clusterId: 'c', namespace: '')` and
  /// `CertificateListKey(clusterId: 'c')` resolve to the same slot.
  CertificateListKey({required this.clusterId, String? namespace})
      : namespace = (namespace != null && namespace.isEmpty) ? null : namespace;

  final String clusterId;

  /// null → cluster-wide list. Backend filters by RBAC server-side.
  final String? namespace;

  @override
  bool operator ==(Object other) =>
      other is CertificateListKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

final certificateListProvider = FutureProvider.autoDispose
    .family<List<Certificate>, CertificateListKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('certificate list invalidated');
  });
  return ref.read(certManagerRepositoryProvider).listCertificates(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

class CertificateDetailKey {
  const CertificateDetailKey({
    required this.clusterId,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is CertificateDetailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, name);
}

final certificateDetailProvider = FutureProvider.autoDispose
    .family<CertificateDetail, CertificateDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('certificate detail invalidated');
  });
  return ref.read(certManagerRepositoryProvider).getCertificate(
        namespace: key.namespace,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Combined Issuers + ClusterIssuers list. The two backend endpoints are
/// fetched in parallel and concatenated (namespaced first), matching
/// `frontend/islands/IssuersList.tsx`'s `Promise.all` pattern. Failures
/// on either endpoint surface as the family failing — the picker
/// provider in `wizards/widgets/issuer_picker.dart` keys on a different
/// shape and is intentionally separate.
final allIssuersProvider = FutureProvider.autoDispose
    .family<List<Issuer>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('issuers list invalidated');
  });
  final repo = ref.read(certManagerRepositoryProvider);
  final results = await Future.wait([
    repo.listIssuers(clusterIdOverride: clusterId, cancelToken: cancel),
    repo.listClusterIssuers(clusterIdOverride: clusterId, cancelToken: cancel),
  ]);
  return [...results[0], ...results[1]];
});

/// Expiring certificates list — already sorted ascending by
/// `daysRemaining` by the backend.
final expiringCertificatesProvider = FutureProvider.autoDispose
    .family<List<ExpiringCertificate>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('expiring certs invalidated');
  });
  return ref.read(certManagerRepositoryProvider).listExpiring(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});
