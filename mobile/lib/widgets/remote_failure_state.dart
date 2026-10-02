// Shared rendering of the R-8 remote-cluster failures (see
// `api/remote_failure.dart` for the reason-code mapping).
//
//   - [RemoteFailureState] draws one typed failure. Retry appears only for
//     a failure that retrying can fix.
//   - [ApiErrorStateView] is the drop-in for an `AsyncValue.error` branch:
//     the typed state when the error carries an R-8 reason, otherwise the
//     generic [ErrorStateView] with the error's friendly message.
//   - [FeatureAbsentState] is for a status route's negative value: the
//     domain's not-installed card unless the payload's `reason` says the
//     backend could not tell.

import 'package:flutter/material.dart';

import '../api/api_error.dart';
import '../api/remote_failure.dart';
import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';

class RemoteFailureState extends StatelessWidget {
  const RemoteFailureState({
    required this.failure,
    this.onRetry,
    super.key,
  });

  final RemoteFailure failure;

  /// Shown as a Retry button only when [RemoteFailure.retryable].
  final VoidCallback? onRetry;

  IconData get _icon => switch (failure.kind) {
        RemoteFailureKind.unreachable => Icons.cloud_off_outlined,
        RemoteFailureKind.credentialsInvalid => Icons.key_off_outlined,
        RemoteFailureKind.clusterUnknown => Icons.help_outline,
        RemoteFailureKind.dbUnavailable => Icons.storage_outlined,
        RemoteFailureKind.unsupportedPlatform => Icons.info_outline,
        RemoteFailureKind.discoveryUnavailable => Icons.travel_explore_outlined,
        RemoteFailureKind.notInstalled => Icons.extension_off_outlined,
      };

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // Informational states (nothing is broken, the view just does not
    // apply here) use the muted tone; failures use the error tone.
    final informational = failure.kind == RemoteFailureKind.unsupportedPlatform ||
        failure.kind == RemoteFailureKind.notInstalled;
    final retry = failure.retryable ? onRetry : null;

    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ExcludeSemantics(
              child: Icon(
                _icon,
                size: 48,
                color: informational ? colors.textMuted : colors.error,
              ),
            ),
            const SizedBox(height: 16),
            Text(
              failure.title,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              failure.message,
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.textSecondary, height: 1.4),
            ),
            if (retry != null) ...[
              const SizedBox(height: 16),
              FilledButton(onPressed: retry, child: const Text('Retry')),
            ],
          ],
        ),
      ),
    );
  }
}

class ApiErrorStateView extends StatelessWidget {
  const ApiErrorStateView({
    required this.error,
    this.onRetry,
    super.key,
  });

  final Object error;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    final failure = RemoteFailure.fromError(error);
    if (failure != null) {
      return RemoteFailureState(failure: failure, onRetry: onRetry);
    }
    return ErrorStateView(message: ApiError.messageOf(error), onRetry: onRetry);
  }
}

class FeatureAbsentState extends StatelessWidget {
  const FeatureAbsentState({
    required this.reason,
    required this.notInstalled,
    this.onRetry,
    super.key,
  });

  /// The status payload's `reason` (null on the local cluster).
  final String? reason;

  /// The domain's not-installed card, e.g. `FeatureUnavailableState.gitops()`.
  final Widget notInstalled;

  /// Re-reads the status. Offered only for a transient could-not-tell.
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    final failure = RemoteFailure.fromReason(reason);
    if (failure == null || !failure.presenceUnknown) return notInstalled;
    return RemoteFailureState(failure: failure, onRetry: onRetry);
  }
}
