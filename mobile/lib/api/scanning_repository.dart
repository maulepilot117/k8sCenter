// Mobile-side wrapper over the backend security-scanning API at
// `/v1/scanning/{status,vulnerabilities,vulnerabilities/{ns}/{kind}/{name}}`.
// Wire types mirror `backend/internal/scanning/types.go` exactly. Web parallel:
// `frontend/islands/VulnerabilityDashboard.tsx` +
// `frontend/islands/VulnerabilityDetail.tsx`.
//
// Two endpoints back the mobile surfaces:
//   * `GET /v1/scanning/status` — scanner discovery (Trivy / Kubescape).
//   * `GET /v1/scanning/vulnerabilities?namespace=<ns>` — workload-level
//     vulnerability summaries scoped to a namespace (mandatory param).
//   * `GET /v1/scanning/vulnerabilities/{ns}/{kind}/{name}` — Trivy-only
//     CVE detail per workload. Kubescape rows have no detail endpoint
//     and the backend returns `501 Not Implemented` if Trivy is absent;
//     the UI gates the row tap accordingly.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards it
// as an explicit `X-Cluster-ID` header — same invariant as PR-4i.
//
// Severity normalization: the backend list summary fields are stable
// camelCase ints (`critical`, `high`, `medium`, `low`). The detail CVE
// envelope uses UPPER-CASE severity strings (`CRITICAL` / `HIGH` / …);
// mobile lowercases on parse so chip filtering and color mapping share
// one severity vocabulary across both surfaces.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

/// Vulnerability scanner. Open enum on the wire so an as-yet-unsupported
/// scanner (e.g., Falco, NeuVector) doesn't crash the parser.
enum Scanner { trivy, kubescape, unknown }

Scanner _scannerFromJson(Object? v) {
  if (v is! String) return Scanner.unknown;
  switch (v) {
    case 'trivy':
      return Scanner.trivy;
    case 'kubescape':
      return Scanner.kubescape;
    default:
      return Scanner.unknown;
  }
}

String scannerLabel(Scanner s) => switch (s) {
      Scanner.trivy => 'Trivy',
      Scanner.kubescape => 'Kubescape',
      Scanner.unknown => 'Unknown',
    };

/// Severity filter chip values shared between the list screen and detail
/// screen. `all` + `none` plus the four canonical severities; `none`
/// renders workloads with zero CVEs (useful to confirm scan coverage).
enum SeverityFilter { all, critical, high, medium, low, none }

/// Severity ordering desc — critical first, low last. Used by sort
/// comparators when a chip filter changes the displayed subset.
const List<String> kSeverityOrder = ['critical', 'high', 'medium', 'low'];

// ---------------------------------------------------------------------------
// Wire-format value types
// ---------------------------------------------------------------------------

/// Per-severity vulnerability counts. Mirrors `SeveritySummary` in
/// `backend/internal/scanning/types.go`.
class SeveritySummary {
  const SeveritySummary({
    this.critical = 0,
    this.high = 0,
    this.medium = 0,
    this.low = 0,
  });

  final int critical;
  final int high;
  final int medium;
  final int low;

  /// Total non-info CVEs at any severity. Used as a sort tiebreaker and
  /// for the "no issues" empty-state distinction.
  int get total => critical + high + medium + low;

  /// Severity "score" used to sort the workload list — Critical worth
  /// 1000× a High mirrors the web's
  /// `frontend/islands/VulnerabilityDashboard.tsx` sort comparator.
  int get severityScore => critical * 1000 + high;

  factory SeveritySummary.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    return SeveritySummary(
      critical: i(json['critical']),
      high: i(json['high']),
      medium: i(json['medium']),
      low: i(json['low']),
    );
  }

  static const empty = SeveritySummary();
}

/// Per-scanner availability. Mirrors `ScannerDetail`. `namespace` is
/// stripped to null for non-admin users by the backend handler — admins
/// see the installation namespace for the diagnostic hint copy.
class ScannerDetail {
  const ScannerDetail({required this.available, this.namespace});

  final bool available;
  final String? namespace;

  factory ScannerDetail.fromJson(Map<String, dynamic> json) {
    final ns = json['namespace'];
    return ScannerDetail(
      available: json['available'] == true,
      namespace: ns is String && ns.isNotEmpty ? ns : null,
    );
  }
}

/// Decoded `/v1/scanning/status` response. Mirrors `ScannerStatus`.
///
/// Distinct from "neither scanner installed" vs "backend unreachable" —
/// see [serviceUnavailable]. The dashboard renders
/// `FeatureUnavailableState.scanning()` on `detected == false &&
/// !serviceUnavailable`; transient 5xx surfaces a retry-able banner.
class ScanningStatus {
  const ScanningStatus({
    required this.detected,
    this.trivy,
    this.kubescape,
    this.lastChecked = '',
    this.serviceUnavailable = false,
  });

  /// True when at least one scanner is detected.
  final bool detected;

  /// Per-scanner availability. `null` when the backend hasn't reported
  /// the scanner in this response (older backend or partial discovery).
  final ScannerDetail? trivy;
  final ScannerDetail? kubescape;

  /// RFC-3339 timestamp; empty when the first probe hasn't completed.
  final String lastChecked;

  /// True when the backend status endpoint returned 5xx — distinguishes
  /// "backend unreachable" from "no scanner installed". Both flow
  /// through `detected: false` for FeatureUnavailableState purposes;
  /// consumers that want to nudge toward retry can branch on this flag.
  final bool serviceUnavailable;

  bool get trivyAvailable => trivy?.available == true;
  bool get kubescapeAvailable => kubescape?.available == true;

  factory ScanningStatus.fromJson(Map<String, dynamic> json) {
    // Backend's `detected` field is the canonical "" / "trivy" /
    // "kubescape" / "both" enum from
    // `backend/internal/scanning/types.go::Scanner`. Mobile collapses to
    // a boolean since the per-scanner availability is already in the
    // `trivy` / `kubescape` blocks.
    final detectedField = json['detected'];
    final trivyBlock = json['trivy'];
    final kubescapeBlock = json['kubescape'];
    final trivyAvail = trivyBlock is Map
        ? ScannerDetail.fromJson(Map<String, dynamic>.from(trivyBlock))
        : null;
    final kubescapeAvail = kubescapeBlock is Map
        ? ScannerDetail.fromJson(Map<String, dynamic>.from(kubescapeBlock))
        : null;
    // Trust the per-scanner blocks over the `detected` string — if a
    // future backend returns `"both"` but only populates `trivy`, the
    // mobile cards still reflect ground truth.
    final detected = trivyAvail?.available == true ||
        kubescapeAvail?.available == true ||
        (detectedField is String && detectedField.isNotEmpty);
    return ScanningStatus(
      detected: detected,
      trivy: trivyAvail,
      kubescape: kubescapeAvail,
      lastChecked: json['lastChecked'] as String? ?? '',
    );
  }

  static const empty = ScanningStatus(detected: false);
  static const unreachable = ScanningStatus(
    detected: false,
    serviceUnavailable: true,
  );
}

/// One container image's vulnerability counts. Mirrors `ImageVulnInfo`.
class ImageVulnInfo {
  const ImageVulnInfo({required this.image, required this.severities});

  /// Image reference (`repo:tag` or full registry path).
  final String image;
  final SeveritySummary severities;

  factory ImageVulnInfo.fromJson(Map<String, dynamic> json) {
    final sev = json['severities'];
    return ImageVulnInfo(
      image: json['image'] as String? ?? '',
      severities: sev is Map
          ? SeveritySummary.fromJson(Map<String, dynamic>.from(sev))
          : SeveritySummary.empty,
    );
  }
}

/// One workload's vulnerability roll-up. Mirrors `WorkloadVulnSummary`.
class WorkloadVulnSummary {
  const WorkloadVulnSummary({
    required this.namespace,
    required this.kind,
    required this.name,
    required this.images,
    required this.total,
    this.lastScanned = '',
    this.scanner = Scanner.unknown,
  });

  final String namespace;
  final String kind;
  final String name;

  final List<ImageVulnInfo> images;
  final SeveritySummary total;

  /// RFC-3339 timestamp. Empty when the engine didn't emit one; the
  /// 7-day stale threshold treats empty as not stale (no signal to
  /// compare against).
  final String lastScanned;

  final Scanner scanner;

  /// Stable identifier for `SliverList.builder` keys + cross-screen
  /// deep-linking. `namespace/kind/name` is unique per workload — the
  /// backend lists each workload once per scanner so the scanner tag
  /// joins the tuple for cluster setups running both engines.
  String get stableKey => '$namespace/$kind/$name/${_scannerKey(scanner)}';

  factory WorkloadVulnSummary.fromJson(Map<String, dynamic> json) {
    final imgsRaw = json['images'];
    final imgs = <ImageVulnInfo>[];
    if (imgsRaw is List) {
      for (final v in imgsRaw) {
        if (v is Map) {
          imgs.add(ImageVulnInfo.fromJson(Map<String, dynamic>.from(v)));
        }
      }
    }
    final totalRaw = json['total'];
    return WorkloadVulnSummary(
      namespace: json['namespace'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      images: imgs,
      total: totalRaw is Map
          ? SeveritySummary.fromJson(Map<String, dynamic>.from(totalRaw))
          : SeveritySummary.empty,
      lastScanned: json['lastScanned'] as String? ?? '',
      scanner: _scannerFromJson(json['scanner']),
    );
  }
}

String _scannerKey(Scanner s) => switch (s) {
      Scanner.trivy => 'trivy',
      Scanner.kubescape => 'kubescape',
      Scanner.unknown => 'unknown',
    };

/// Aggregate counts returned alongside the workload list. Mirrors
/// `VulnListMetadata`. Used for the dashboard summary chip row when a
/// namespace is selected.
class VulnListMetadata {
  const VulnListMetadata({
    required this.total,
    required this.severity,
  });

  final int total;
  final SeveritySummary severity;

  factory VulnListMetadata.fromJson(Map<String, dynamic> json) {
    int i(Object? v) => v is num ? v.toInt() : 0;
    final sev = json['severity'];
    return VulnListMetadata(
      total: i(json['total']),
      severity: sev is Map
          ? SeveritySummary.fromJson(Map<String, dynamic>.from(sev))
          : SeveritySummary.empty,
    );
  }

  static const empty = VulnListMetadata(
    total: 0,
    severity: SeveritySummary.empty,
  );
}

/// Wraps the list endpoint's `{vulnerabilities, summary}` envelope so
/// both pieces stay together through provider plumbing.
class VulnListResponse {
  const VulnListResponse({
    required this.vulnerabilities,
    required this.summary,
  });

  final List<WorkloadVulnSummary> vulnerabilities;
  final VulnListMetadata summary;

  static const empty = VulnListResponse(
    vulnerabilities: <WorkloadVulnSummary>[],
    summary: VulnListMetadata.empty,
  );
}

/// Single CVE finding. Mirrors `CVEDetail`. Severity strings are
/// lowercased on parse so the rendering helpers share the four-bucket
/// vocabulary with the list endpoint. `cvssScore == null` when the
/// upstream feed lacked a score — render as "—" not "0.0".
class CVEDetail {
  const CVEDetail({
    required this.id,
    required this.severity,
    required this.cvssScore,
    required this.package,
    required this.installedVersion,
    required this.fixedVersion,
    required this.title,
    required this.primaryLink,
  });

  final String id;

  /// Lowercase severity bucket: `critical | high | medium | low |
  /// unknown`. Anything else falls through to `unknown`.
  final String severity;

  /// 0.0 – 10.0 when present; null when the upstream feed lacked a
  /// score (Aqua / GHSA sometimes omit it).
  final double? cvssScore;

  final String package;
  final String installedVersion;

  /// Empty string when no fix is available — used by the "Fixable only"
  /// filter chip.
  final String fixedVersion;

  final String title;

  /// Vendor's primary link for the CVE. Mobile validates this is http(s)
  /// before opening to defend against `javascript:` / `data:` URLs from
  /// third-party feeds; see [safeHttpUrl].
  final String primaryLink;

  bool get hasFix => fixedVersion.isNotEmpty;

  factory CVEDetail.fromJson(Map<String, dynamic> json) {
    double? optDouble(Object? v) {
      if (v == null) return null;
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v);
      return null;
    }

    final rawSev = json['severity'];
    final sev = rawSev is String ? rawSev.toLowerCase() : 'unknown';
    // Map any unexpected severity string back to "unknown" so chip
    // filters + colour helpers stay in the canonical five-bucket
    // vocabulary.
    final normalised = const {
      'critical',
      'high',
      'medium',
      'low',
      'unknown',
    }.contains(sev)
        ? sev
        : 'unknown';

    return CVEDetail(
      id: json['id'] as String? ?? '',
      severity: normalised,
      cvssScore: optDouble(json['cvssScore']),
      package: json['package'] as String? ?? '',
      installedVersion: json['installedVersion'] as String? ?? '',
      fixedVersion: json['fixedVersion'] as String? ?? '',
      title: json['title'] as String? ?? '',
      primaryLink: json['primaryLink'] as String? ?? '',
    );
  }
}

/// Per-image vulnerability list. Mirrors `ImageVulnDetail`. `container`
/// is the workload's container name; `name` is the image reference
/// (`repo:tag` form, possibly with a registry host prefix).
class ImageVulnDetail {
  const ImageVulnDetail({
    required this.name,
    required this.container,
    required this.vulnerabilities,
  });

  final String name;
  final String container;
  final List<CVEDetail> vulnerabilities;

  factory ImageVulnDetail.fromJson(Map<String, dynamic> json) {
    final vRaw = json['vulnerabilities'];
    final v = <CVEDetail>[];
    if (vRaw is List) {
      for (final entry in vRaw) {
        if (entry is Map) {
          v.add(CVEDetail.fromJson(Map<String, dynamic>.from(entry)));
        }
      }
    }
    return ImageVulnDetail(
      name: json['name'] as String? ?? '',
      container: json['container'] as String? ?? '',
      vulnerabilities: v,
    );
  }
}

/// Full per-workload detail envelope. Mirrors `WorkloadVulnDetail`.
class WorkloadVulnDetail {
  const WorkloadVulnDetail({
    required this.namespace,
    required this.kind,
    required this.name,
    required this.scanner,
    required this.lastScanned,
    required this.images,
  });

  final String namespace;
  final String kind;
  final String name;
  final Scanner scanner;
  final String lastScanned;
  final List<ImageVulnDetail> images;

  /// Roll-up across all images. Computed client-side — backend doesn't
  /// emit a top-level summary on this envelope, only per-image.
  ///
  /// CVEs with severity `'unknown'` (the open-enum fallback) are
  /// included in the `low` bucket so `summary.total` stays consistent
  /// with `fixableCount`'s "iterate every CVE" semantics. The
  /// `unknown` case is intentionally folded into `low` rather than
  /// surfaced as a separate chip — the UI has no chip for `unknown`
  /// and this keeps `total == critical + high + medium + low` invariant.
  SeveritySummary get summary {
    int c = 0, h = 0, m = 0, l = 0;
    for (final img in images) {
      for (final v in img.vulnerabilities) {
        switch (v.severity) {
          case 'critical':
            c++;
          case 'high':
            h++;
          case 'medium':
            m++;
          case 'low':
            l++;
          default:
            // 'unknown' + any future open-enum value count towards the
            // total so callers can reason about completeness without a
            // separate bucket.
            l++;
        }
      }
    }
    return SeveritySummary(critical: c, high: h, medium: m, low: l);
  }

  /// Count of CVEs with a fixed version available — drives the
  /// "Fixable only" filter visibility on the detail screen.
  int get fixableCount {
    var n = 0;
    for (final img in images) {
      for (final v in img.vulnerabilities) {
        if (v.hasFix) n++;
      }
    }
    return n;
  }

  factory WorkloadVulnDetail.fromJson(Map<String, dynamic> json) {
    final imgsRaw = json['images'];
    final imgs = <ImageVulnDetail>[];
    if (imgsRaw is List) {
      for (final entry in imgsRaw) {
        if (entry is Map) {
          imgs.add(ImageVulnDetail.fromJson(Map<String, dynamic>.from(entry)));
        }
      }
    }
    return WorkloadVulnDetail(
      namespace: json['namespace'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      scanner: _scannerFromJson(json['scanner']),
      lastScanned: json['lastScanned'] as String? ?? '',
      images: imgs,
    );
  }
}

// ---------------------------------------------------------------------------
// Stale-scan threshold + URL safety helper
// ---------------------------------------------------------------------------

/// Last-scanned timestamps older than this are flagged inline. Mirrors
/// `frontend/islands/VulnerabilityDashboard.tsx::STALE_THRESHOLD_MS`.
const Duration kScanStaleThreshold = Duration(days: 7);

/// True when [lastScannedIso] is more than [kScanStaleThreshold] old.
/// An empty string yields false — no scan timestamp means no signal to
/// compare against, not "stale".
bool isScanStale(String lastScannedIso) {
  if (lastScannedIso.isEmpty) return false;
  final t = DateTime.tryParse(lastScannedIso);
  if (t == null) return false;
  return DateTime.now().toUtc().difference(t.toUtc()) > kScanStaleThreshold;
}

/// Returns [raw] only when it's a syntactically valid http(s) URL.
/// External CVE feeds (Trivy → NVD / Aqua / GHSA) can contain any
/// string, including `javascript:` and `data:` URLs that would execute
/// script in the device's origin if rendered as a clickable link.
/// Falls back to [fallback] otherwise.
///
/// Web parallel: `frontend/islands/VulnerabilityDetail.tsx::safeHttpUrl`.
String safeHttpUrl(String raw, String fallback) {
  if (raw.isEmpty) return fallback;
  final u = Uri.tryParse(raw);
  if (u == null) return fallback;
  final scheme = u.scheme.toLowerCase();
  if (scheme == 'http' || scheme == 'https') return raw;
  return fallback;
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

/// Stateless wrapper over `/v1/scanning/*`. Cluster pinning threads
/// through `clusterIdOverride` so the wire header always matches the
/// family-key slot the caller writes back into.
class ScanningRepository {
  ScanningRepository(this._dio);

  final Dio _dio;

  /// Fetches scanner-discovery status. Returns
  /// [ScanningStatus.unreachable] on 5xx so the surface keeps rendering
  /// install-guidance copy without flashing an error card.
  Future<ScanningStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/scanning/status',
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return ScanningStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return ScanningStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return ScanningStatus.unreachable;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists workload vulnerability summaries for [namespace]. Server-side
  /// RBAC filters out workloads the user can't list, and per-scanner
  /// access is gated separately — a user with Trivy access but not
  /// Kubescape will see Trivy rows only.
  ///
  /// Unlike [status], this rethrows all non-cancel [DioException]s
  /// (including 5xx) as [ApiError]. The list view is a data endpoint —
  /// silently swallowing backend errors here would mask real outages
  /// (cf. the policy compliance-history anti-pattern). 5xx →
  /// unreachable applies only to feature-detection endpoints.
  Future<VulnListResponse> listVulnerabilities({
    required String namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/scanning/vulnerabilities',
        queryParameters: {'namespace': namespace},
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        final m = Map<String, dynamic>.from(data);
        final raw = m['vulnerabilities'];
        final list = <WorkloadVulnSummary>[];
        if (raw is List) {
          for (final v in raw) {
            if (v is Map) {
              list.add(
                WorkloadVulnSummary.fromJson(Map<String, dynamic>.from(v)),
              );
            }
          }
        }
        final summary = m['summary'];
        return VulnListResponse(
          vulnerabilities: list,
          summary: summary is Map
              ? VulnListMetadata.fromJson(Map<String, dynamic>.from(summary))
              : VulnListMetadata.empty,
        );
      }
      return VulnListResponse.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches CVE detail for a single workload. Trivy-only on the
  /// backend; calling on a Kubescape-scanned workload yields
  /// `501 Not Implemented` with message
  /// `CVE-level detail requires Trivy Operator` — surfaced verbatim via
  /// the [ApiError] so the UI can render the targeted help string.
  Future<WorkloadVulnDetail> getVulnerabilityDetail({
    required String namespace,
    required String kind,
    required String name,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/scanning/vulnerabilities/'
        '${Uri.encodeComponent(namespace)}/'
        '${Uri.encodeComponent(kind)}/'
        '${Uri.encodeComponent(name)}',
        options: _opts(clusterIdOverride),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return WorkloadVulnDetail.fromJson(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for $namespace/$kind/$name',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Options? _opts(String? clusterIdOverride) => clusterIdOverride == null
      ? null
      : Options(headers: {'X-Cluster-ID': clusterIdOverride});
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

final scanningRepositoryProvider = Provider<ScanningRepository>((ref) {
  return ScanningRepository(ref.watch(dioProvider));
});

/// Per-cluster scanner status. Drives `FeatureUnavailableState.scanning()`
/// on every observatory surface. Keyed on cluster id so a cluster switch
/// keys a fresh entry rather than reusing the prior cluster's discovery.
final scanningStatusProvider = FutureProvider.autoDispose
    .family<ScanningStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('scanning status invalidated');
  });
  return ref.read(scanningRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Composite key for the per-namespace vulnerability list. Namespace is
/// part of the key so the provider cache holds one list per namespace —
/// switching namespaces in the picker doesn't re-fetch on the way back.
class VulnListKey {
  const VulnListKey({required this.clusterId, required this.namespace});

  final String clusterId;
  final String namespace;

  @override
  bool operator ==(Object other) =>
      other is VulnListKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

final vulnerabilitiesListProvider = FutureProvider.autoDispose
    .family<VulnListResponse, VulnListKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('vulnerabilities list invalidated');
  });
  return ref.read(scanningRepositoryProvider).listVulnerabilities(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

/// Composite key for the per-workload detail. Includes scanner because
/// the backend only returns Trivy data — Kubescape rows in the list
/// route to the same provider but the repository surfaces a 501 that
/// the detail screen renders as a targeted help message.
class VulnDetailKey {
  const VulnDetailKey({
    required this.clusterId,
    required this.namespace,
    required this.kind,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String kind;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is VulnDetailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.kind == kind &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, kind, name);
}

final vulnerabilityDetailProvider = FutureProvider.autoDispose
    .family<WorkloadVulnDetail, VulnDetailKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('vulnerability detail invalidated');
  });
  return ref.read(scanningRepositoryProvider).getVulnerabilityDetail(
        namespace: key.namespace,
        kind: key.kind,
        name: key.name,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
