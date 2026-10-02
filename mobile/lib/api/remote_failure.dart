// Typed view of the R-8 remote-cluster reason codes.
//
// The backend answers a remote-cluster failure with a reason from the
// closed `k8s.ReasonCode` set (backend/internal/k8s/target_status.go):
//
//   - KTD4 error envelopes carry it as `error.reason`: unreachable,
//     credentials_invalid, cluster_unknown, db_unavailable, plus
//     unsupported_platform on a 501 "not supported on remote" refusal and
//     discovery_unavailable when a feature's discovery read failed.
//   - KTD5 status routes keep the family's negative value
//     (`detected: false` / `""`) and add an optional `reason`:
//     discovery_missing when the feature is genuinely absent, or one of the
//     could-not-tell reasons above when the answer is unknown.
//
// The web app renders the same split (frontend/lib/dashboard/widget-state.ts
// PRESENCE_UNKNOWN_REASONS). Reasons outside this set (forbidden,
// endpoint-specific codes such as active_job_exists) are not remote
// failures and keep their screen's own handling.

import 'package:dio/dio.dart';

import 'api_error.dart';

enum RemoteFailureKind {
  /// The selected cluster could not be reached (`unreachable`).
  unreachable,

  /// The cluster rejected its stored credentials (`credentials_invalid`).
  credentialsInvalid,

  /// The selected cluster id is not in the registry (`cluster_unknown`).
  clusterUnknown,

  /// The cluster registry itself is unavailable (`db_unavailable`).
  dbUnavailable,

  /// k8sCenter does not implement this view for remote clusters
  /// (`unsupported_platform`, 501).
  unsupportedPlatform,

  /// The feature's discovery read failed, so presence is unknown
  /// (`discovery_unavailable`).
  discoveryUnavailable,

  /// The target's discovery does not contain the feature
  /// (`discovery_missing`).
  notInstalled,
}

class RemoteFailure {
  const RemoteFailure._(this.kind);

  final RemoteFailureKind kind;

  static const Map<String, RemoteFailureKind> _byReason = {
    'unreachable': RemoteFailureKind.unreachable,
    'credentials_invalid': RemoteFailureKind.credentialsInvalid,
    'cluster_unknown': RemoteFailureKind.clusterUnknown,
    'db_unavailable': RemoteFailureKind.dbUnavailable,
    'unsupported_platform': RemoteFailureKind.unsupportedPlatform,
    'discovery_unavailable': RemoteFailureKind.discoveryUnavailable,
    'discovery_missing': RemoteFailureKind.notInstalled,
  };

  /// Maps a reason code to its failure, or null for an absent, empty or
  /// unrecognised reason.
  static RemoteFailure? fromReason(String? reason) {
    final kind = _byReason[reason];
    return kind == null ? null : RemoteFailure._(kind);
  }

  /// Reads the reason off an [ApiError] (directly, or wrapped in the
  /// DioException the ErrorMappingInterceptor rejects with). Null when the
  /// error is not an API error or carries no R-8 reason.
  static RemoteFailure? fromError(Object? error) {
    final api = switch (error) {
      ApiError e => e,
      DioException(error: ApiError e) => e,
      _ => null,
    };
    return api == null ? null : fromReason(api.reason);
  }

  /// Reads a KTD5 status payload's `reason`. Null means the family's
  /// negative value is a plain verdict (always the case on the local
  /// cluster, which sends no reason). A non-null result with
  /// [RemoteFailureKind.notInstalled] is also a verdict; any other kind
  /// means the backend could not tell whether the feature is installed.
  /// Same function as [fromReason]; the name documents the call site.
  static RemoteFailure? fromStatusReason(String? reason) => fromReason(reason);

  /// True when the backend could not tell whether the feature is
  /// installed, so a negative status value must not read as "not
  /// installed".
  bool get presenceUnknown => kind != RemoteFailureKind.notInstalled;

  /// Whether retrying the same request can succeed without someone first
  /// changing the cluster, its credentials or k8sCenter itself.
  bool get retryable => switch (kind) {
        RemoteFailureKind.unreachable ||
        RemoteFailureKind.dbUnavailable ||
        RemoteFailureKind.discoveryUnavailable =>
          true,
        RemoteFailureKind.credentialsInvalid ||
        RemoteFailureKind.clusterUnknown ||
        RemoteFailureKind.unsupportedPlatform ||
        RemoteFailureKind.notInstalled =>
          false,
      };

  String get title => switch (kind) {
        RemoteFailureKind.unreachable => 'Cluster unreachable',
        RemoteFailureKind.credentialsInvalid => 'Credentials no longer valid',
        RemoteFailureKind.clusterUnknown => 'Cluster not registered',
        RemoteFailureKind.dbUnavailable => 'Cluster registry unavailable',
        RemoteFailureKind.unsupportedPlatform =>
          'Not available for remote clusters',
        RemoteFailureKind.discoveryUnavailable => 'Could not check this cluster',
        RemoteFailureKind.notInstalled => 'Not installed on this cluster',
      };

  String get message => switch (kind) {
        RemoteFailureKind.unreachable =>
          'The selected cluster could not be reached. Check that it is '
              'online, then try again.',
        RemoteFailureKind.credentialsInvalid =>
          'k8sCenter could not connect to the selected cluster with its '
              'stored credentials. An administrator needs to update them.',
        RemoteFailureKind.clusterUnknown =>
          'The selected cluster is no longer registered. Pick another '
              'cluster.',
        RemoteFailureKind.dbUnavailable =>
          'The cluster registry is unavailable right now. Try again shortly.',
        RemoteFailureKind.unsupportedPlatform =>
          'This view only works on the local cluster. Switch to the local '
              'cluster to use it.',
        RemoteFailureKind.discoveryUnavailable =>
          'Discovery on the selected cluster failed, so k8sCenter could not '
              'tell whether this feature is installed.',
        RemoteFailureKind.notInstalled =>
          'This feature is not installed on the selected cluster.',
      };
}
