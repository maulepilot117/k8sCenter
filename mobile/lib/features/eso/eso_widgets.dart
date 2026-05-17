// Shared pill + small-widget helpers consumed by every ESO surface
// (dashboard, lists, details, metrics panel). Keeping the pill mapping
// in one file is what enforces R10 (web/dart isomorphism) for drift
// tri-state colour and EsoStatus colour — a future regression where
// `Unknown` rendered as `error` instead of `textMuted` (PR-3f learnings
// #9) would have to land here first.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'bulk_refresh_sheet.dart';
import 'force_sync_controller.dart';

/// Wraps a per-cluster ESO surface body in the standard discovery-status
/// gate. The outer ConsumerWidget watches `esoStatusProvider(clusterId)`,
/// handles loading / error / not-detected uniformly, and only calls
/// [builder] when ESO is detected. Six ESO list screens and the dashboard
/// previously copy-pasted this outer shell; adding a seventh surface no
/// longer requires duplicating it.
class EsoStatusGate extends ConsumerWidget {
  const EsoStatusGate({super.key, required this.builder});

  /// Called with the current activeClusterId once status resolves to
  /// `detected: true`. Implementations typically return the screen's
  /// per-cluster `_ListBody`-shaped body.
  final Widget Function(String clusterId) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));
    return statusAsync.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => ErrorStateView(
        message: e is ApiError ? e.message : e.toString(),
        onRetry: () => ref.invalidate(esoStatusProvider(clusterId)),
      ),
      data: (status) {
        if (!status.detected) return FeatureUnavailableState.eso();
        return builder(clusterId);
      },
    );
  }
}

/// Render the appropriate degraded state for an ESO detail endpoint
/// failure. List screens gate on `esoStatusProvider` before fetching, but
/// detail screens are reached directly via deep-link / push notification,
/// so they have no chance to render `FeatureUnavailableState.eso()` until
/// the detail endpoint itself responds. A 503 here means the backend has
/// observed that ESO is no longer detected (cluster restart, CRD
/// removal); collapse it to the same install-guidance UX the lists use,
/// not a generic retry-this-error view.
Widget esoDetailErrorState({
  required Object error,
  required VoidCallback onRetry,
}) {
  if (error is ApiError && error.statusCode == 503) {
    return FeatureUnavailableState.eso();
  }
  return ErrorStateView(
    message: error is ApiError ? error.message : error.toString(),
    onRetry: onRetry,
  );
}

/// Surface a non-empty readyMessage from any ESO resource (ExternalSecret /
/// ClusterExternalSecret / SecretStore / ClusterSecretStore). Three
/// detail screens previously kept private copies of an identical widget;
/// this is the canonical one.
class EsoReadyMessageCard extends StatelessWidget {
  const EsoReadyMessageCard({
    super.key,
    required this.reason,
    required this.message,
    required this.colors,
  });

  final String? reason;
  final String message;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            reason ?? 'Status detail',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
          Text(
            message,
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
        ],
      ),
    );
  }
}

/// Drift indicator pill. Tri-state colour mapping is the only place
/// drift colour lives — every drift surface across PR-4h reads from
/// here. Per R7: **`Unknown` is never red.** It means the provider
/// doesn't populate `SyncedResourceVersion`; rendering it red would
/// confuse operators on every ESO store backed by the Kubernetes
/// provider (or any other provider that omits resource versions).
class DriftPill extends StatelessWidget {
  const DriftPill({
    super.key,
    required this.status,
    this.reason = DriftUnknownReason.unspecified,
    this.dense = false,
  });

  final DriftStatus status;

  /// Only consulted when [status] is [DriftStatus.unknown]. Drives the
  /// hover tooltip — "Provider doesn't expose resource version", "RBAC
  /// denied", etc. — so operators know WHY drift wasn't resolvable.
  final DriftUnknownReason reason;

  /// Dense mode shrinks padding + font for embedding inside list rows
  /// next to the resource name.
  final bool dense;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final theme = _driftPillTheme(status, colors);
    if (theme == null) {
      // notObserved → render no pill at all. The list endpoint omits
      // `lastObservedDriftStatus` when the poller has never observed
      // this ES; rendering an "Unknown" pill in that case would
      // contradict the wire-shape contract documented on the backend
      // type (LastObservedDriftStatus omitempty).
      return const SizedBox.shrink();
    }
    final pill = Semantics(
      container: true,
      label: 'Drift state: ${theme.label}',
      excludeSemantics: true,
      child: Container(
        padding: EdgeInsets.symmetric(
          horizontal: dense ? 6 : 8,
          vertical: dense ? 2 : 3,
        ),
        decoration: BoxDecoration(
          color: theme.background,
          borderRadius: BorderRadius.circular(dense ? 3 : 4),
          border: Border.all(color: theme.foreground),
        ),
        child: Text(
          theme.label,
          style: TextStyle(
            color: theme.foreground,
            fontSize: dense ? 10 : 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
    if (status == DriftStatus.unknown) {
      return Tooltip(message: _driftUnknownTooltip(reason), child: pill);
    }
    return pill;
  }
}

class _PillTheme {
  const _PillTheme({
    required this.label,
    required this.foreground,
    required this.background,
  });
  final String label;
  final Color foreground;
  final Color background;
}

/// Returns the pill theme for a drift state, or null when the caller
/// should render nothing ([DriftStatus.notObserved]).
_PillTheme? _driftPillTheme(DriftStatus s, KubeColors colors) {
  switch (s) {
    case DriftStatus.inSync:
      return _PillTheme(
        label: 'In sync',
        foreground: colors.success,
        background: colors.successDim,
      );
    case DriftStatus.drifted:
      return _PillTheme(
        label: 'Drifted',
        foreground: colors.warning,
        background: colors.warningDim,
      );
    case DriftStatus.unknown:
      return _PillTheme(
        label: 'Unknown',
        foreground: colors.textMuted,
        // Subtle background — textMuted is the "informational, not
        // actionable" token. Mixing it with bgElevated keeps the pill
        // recognizable without elevating its visual weight to match
        // SyncFailed or Drifted (which ARE actionable).
        background: colors.bgElevated,
      );
    case DriftStatus.notObserved:
      return null;
  }
}

String _driftUnknownTooltip(DriftUnknownReason r) => switch (r) {
      DriftUnknownReason.noSyncedRv =>
        'Provider does not populate syncedResourceVersion — drift '
            'cannot be determined.',
      DriftUnknownReason.noTargetName =>
        'ExternalSecret has no target Secret name set yet.',
      DriftUnknownReason.secretDeleted =>
        'Synced Secret was deleted — the controller will recreate it on '
            'the next sync.',
      DriftUnknownReason.rbacDenied =>
        'Your account lacks "get secret" permission on this namespace, '
            'so drift cannot be checked.',
      DriftUnknownReason.transientError =>
        'Temporary error reaching the Kubernetes API — retry shortly.',
      DriftUnknownReason.clientError =>
        'Internal error building the impersonating Kubernetes client.',
      DriftUnknownReason.unspecified =>
        'Drift status is not available for this resource.',
    };

/// Status pill for ExternalSecret / SecretStore / PushSecret lifecycle
/// state. Maps the open-enum [EsoStatus] onto the canonical
/// success/warning/error/info palette per status.
class EsoStatusPill extends StatelessWidget {
  const EsoStatusPill({
    super.key,
    required this.status,
    this.dense = false,
  });

  final EsoStatus status;
  final bool dense;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final theme = _statusPillTheme(status, colors);
    return Semantics(
      container: true,
      label: 'ESO status: ${theme.label}',
      excludeSemantics: true,
      child: Container(
        padding: EdgeInsets.symmetric(
          horizontal: dense ? 6 : 8,
          vertical: dense ? 2 : 3,
        ),
        decoration: BoxDecoration(
          color: theme.background,
          borderRadius: BorderRadius.circular(dense ? 3 : 4),
          border: Border.all(color: theme.foreground),
        ),
        child: Text(
          theme.label,
          style: TextStyle(
            color: theme.foreground,
            fontSize: dense ? 10 : 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

_PillTheme _statusPillTheme(EsoStatus s, KubeColors colors) {
  switch (s) {
    case EsoStatus.synced:
      return _PillTheme(
        label: 'Synced',
        foreground: colors.success,
        background: colors.successDim,
      );
    case EsoStatus.refreshing:
      return _PillTheme(
        label: 'Refreshing',
        foreground: colors.info,
        background: colors.bgElevated,
      );
    case EsoStatus.stale:
      return _PillTheme(
        label: 'Stale',
        foreground: colors.warning,
        background: colors.warningDim,
      );
    case EsoStatus.drifted:
      return _PillTheme(
        label: 'Drifted',
        foreground: colors.warning,
        background: colors.warningDim,
      );
    case EsoStatus.syncFailed:
      return _PillTheme(
        label: 'SyncFailed',
        foreground: colors.error,
        background: colors.errorDim,
      );
    case EsoStatus.unknown:
      return _PillTheme(
        label: 'Unknown',
        foreground: colors.textMuted,
        background: colors.bgElevated,
      );
  }
}

/// Small chip surfaced next to a SecretStore name to indicate its
/// provider family. Mirrors `frontend/components/eso/ESOBadges.tsx`'s
/// `ProviderBadge` — colour-neutral (textSecondary on bgElevated) so
/// it doesn't compete with the status pill for attention.
class ProviderChip extends StatelessWidget {
  const ProviderChip({super.key, required this.provider});

  final String provider;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final label = provider.isEmpty ? 'no provider' : provider;
    return Semantics(
      container: true,
      label: 'Provider: $label',
      excludeSemantics: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: colors.bgElevated,
          borderRadius: BorderRadius.circular(3),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 10,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

/// Force Sync button for the ExternalSecret detail screen. Replaces
/// the M4-era `DisabledRevertDriftButton` — the web/backend canonical
/// term is "Force Sync" (the M4 mobile label was wrong; there is no
/// separate "revert drift" action).
///
/// Behavior:
///   * Disabled with explanatory tooltip when the active cluster is
///     non-local (backend returns 501 for remote-cluster ESO writes;
///     the UI gate is the first line of defense).
///   * Enabled-tap shows the existing `confirm_sheet.dart` with
///     type-to-confirm against the ExternalSecret's name.
///   * On confirmed tap, drives [ForceSyncController]; success shows
///     a snackbar (the controller also invalidates
///     [externalSecretDetailProvider] so the drift chip refreshes).
class ForceSyncButton extends ConsumerStatefulWidget {
  const ForceSyncButton({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  /// Tooltip surfaced on the disabled (non-local-cluster) state.
  static const String nonLocalTooltip =
      'Force Sync is local-cluster only — use the desktop UI for '
      'remote clusters.';

  @override
  ConsumerState<ForceSyncButton> createState() => _ForceSyncButtonState();
}

class _ForceSyncButtonState extends ConsumerState<ForceSyncButton> {
  int _lastHandledGeneration = 0;

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final isLocal = clusterId == 'local';
    final key = ForceSyncKey(
      clusterId: clusterId,
      namespace: widget.namespace,
      name: widget.name,
    );
    final state = ref.watch(forceSyncControllerProvider(key));

    // Side-effect: snackbar on success/failure. Guarded against
    // re-firing on rebuild via the controller's monotonically-increasing
    // generation field. `addPostFrameCallback` defers messaging until
    // the current build is committed so we don't violate Flutter's
    // no-`setState`-during-build rule.
    _handleStateForSnackbar(state, key);

    final inFlight = state is ForceSyncInFlight;
    final onPressed = !isLocal || inFlight
        ? null
        : () => _onTap(context, key);

    return Tooltip(
      message: isLocal ? '' : ForceSyncButton.nonLocalTooltip,
      child: OutlinedButton.icon(
        onPressed: onPressed,
        icon: inFlight
            ? const SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : const Icon(Icons.sync, size: 16),
        label: const Text('Force Sync'),
      ),
    );
  }

  void _handleStateForSnackbar(ForceSyncState state, ForceSyncKey key) {
    final gen = switch (state) {
      ForceSyncSuccess(:final generation) => generation,
      ForceSyncFailure(:final generation) => generation,
      _ => 0,
    };
    if (gen == 0 || gen == _lastHandledGeneration) return;
    _lastHandledGeneration = gen;
    final messenger = ScaffoldMessenger.of(context);
    final colors = Theme.of(context).extension<KubeColors>()!;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      if (state is ForceSyncSuccess) {
        messenger.showSnackBar(SnackBar(content: Text(state.message)));
      } else if (state is ForceSyncFailure) {
        messenger.showSnackBar(SnackBar(
          content: Text(state.message),
          backgroundColor:
              state.alreadyRefreshing ? null : colors.error,
        ));
      }
      // Acknowledge so a follow-up tap can re-fire.
      ref.read(forceSyncControllerProvider(key).notifier).acknowledge();
    });
  }

  Future<void> _onTap(BuildContext context, ForceSyncKey key) async {
    final confirmed = await showConfirmSheet(
      context: context,
      title: 'Force Sync',
      message: 'This triggers an immediate sync of '
          '${widget.namespace}/${widget.name} against its store. '
          'The current Secret is overwritten with the upstream value.',
      confirmLabel: 'Force Sync',
      typeToConfirm: widget.name,
    );
    if (confirmed != true) return;
    await ref.read(forceSyncControllerProvider(key).notifier).forceSync();
  }
}

/// Dashboard-level entry point for the bulk-refresh modal. Local-cluster
/// only per R12. Non-local renders disabled with a tooltip matching the
/// Force Sync button.
///
/// Hidden entirely on clusters where ESO is not detected — clicking
/// "Bulk refresh" against a cluster with no ESO CRDs returns 503 and
/// confuses the operator. The button lives in the dashboard AppBar
/// (outside `EsoStatusGate`'s body), so it has to gate itself.
class BulkRefreshButton extends ConsumerWidget {
  const BulkRefreshButton({super.key});

  static const String nonLocalTooltip =
      'Bulk refresh is local-cluster only — use the desktop UI for '
      'remote clusters.';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));
    // Hide the button while ESO status is loading or has resolved to
    // not-detected. The dashboard's main body renders
    // FeatureUnavailableState in those cases, so a parallel "Bulk
    // refresh" button in the AppBar would be misleading.
    final esoDetected = statusAsync.maybeWhen(
      data: (s) => s.detected,
      orElse: () => false,
    );
    if (!esoDetected) return const SizedBox.shrink();
    final isLocal = clusterId == 'local';
    return Tooltip(
      message: isLocal ? '' : nonLocalTooltip,
      child: OutlinedButton.icon(
        onPressed: isLocal
            ? () => showBulkRefreshSheet(
                  context: context,
                  clusterId: clusterId,
                )
            : null,
        icon: const Icon(Icons.cyclone, size: 16),
        label: const Text('Bulk refresh'),
      ),
    );
  }
}

/// Renders a "key: value" row using theme tokens. Used by detail
/// screens for read-only attribute dumps (storeRef, refreshInterval,
/// lastSyncTime). Pulled out so all ESO detail surfaces share one
/// labeling pattern.
class EsoKvRow extends StatelessWidget {
  const EsoKvRow({
    super.key,
    required this.label,
    required this.value,
    this.valueColor,
  });

  final String label;
  final String value;
  final Color? valueColor;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 120,
            child: Text(
              label,
              style: TextStyle(
                color: colors.textMuted,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                color: valueColor ?? colors.textPrimary,
                fontSize: 13,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Renders a wrap of small chips. Used for selector / namespace lists
/// on ClusterExternalSecret detail.
///
/// Caps the visible chip count at [maxVisible] so a `ClusterExternalSecret`
/// whose namespaceSelector matches hundreds of namespaces does not stall
/// the detail screen during layout. The remainder is summarised inline
/// with a "+N more" trailing chip — full visibility is a desktop affordance.
class ChipStrip extends StatelessWidget {
  const ChipStrip({
    super.key,
    required this.label,
    required this.items,
    this.foreground,
    this.maxVisible = 50,
  });

  final String label;
  final List<String> items;
  final Color? foreground;
  final int maxVisible;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) return const SizedBox.shrink();
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fg = foreground ?? colors.textSecondary;
    final visible =
        items.length <= maxVisible ? items : items.sublist(0, maxVisible);
    final overflow = items.length - visible.length;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 12,
              fontWeight: FontWeight.w500,
            ),
          ),
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: [
              for (final item in visible)
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: colors.bgElevated,
                    borderRadius: BorderRadius.circular(3),
                    border: Border.all(color: colors.borderSubtle),
                  ),
                  child: Text(
                    item,
                    style: TextStyle(color: fg, fontSize: 11),
                  ),
                ),
              if (overflow > 0)
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: colors.bgElevated,
                    borderRadius: BorderRadius.circular(3),
                    border: Border.all(color: colors.borderSubtle),
                  ),
                  child: Text(
                    '+$overflow more',
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 11,
                      fontStyle: FontStyle.italic,
                    ),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}
