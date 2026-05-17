// ForceSyncController — owns the POST /externalsecrets/{ns}/{name}/force-sync
// write path for a single ExternalSecret.
//
// The controller mirrors the M3 `executeAction` write shape (single-shot
// trigger, transient `inFlight` → terminal `success | failure`, no auto-
// invalidate on the same controller) and the M4 `RefreshableController`
// race-protection invariants (cluster-pin re-check at result arrival,
// dispose-guarded state writes, cancel-on-disposal CancelToken).
//
// State machine: idle → inFlight → success | failure. The UI observes
// `success` once, surfaces a snackbar, and the controller bumps its
// state back to `idle` so a follow-up tap can re-fire. On `success`, the
// controller also invalidates `externalSecretDetailProvider` for the
// same key — the detail screen's drift chip transitions from Drifted →
// Unknown (in-flight) → InSync over the next poll cycle without
// operator-visible inconsistency.
//
// Cluster-pin discipline: the family key carries the pinned cluster id;
// `clusterIdOverride` is forwarded as `X-Cluster-ID` on the wire so the
// request lands on the pinned cluster even if the operator switches
// mid-flight. The postEmission check after the await catches the case
// where the operator switched between submit and response — the response
// already pinned the request, but a follow-up `invalidate` would target
// the wrong (newly-active) family slot, so we surface a clear failure
// message instead.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../widgets/refreshable_controller.dart';

/// State machine for a Force Sync invocation.
sealed class ForceSyncState {
  const ForceSyncState();
}

class ForceSyncIdle extends ForceSyncState {
  const ForceSyncIdle();
}

class ForceSyncInFlight extends ForceSyncState {
  const ForceSyncInFlight();
}

/// 202 received from the backend; UI surfaces a snackbar with [message].
/// Carries a generation id so the UI consumes the success exactly once
/// rather than re-firing the snackbar on every rebuild.
class ForceSyncSuccess extends ForceSyncState {
  const ForceSyncSuccess({required this.message, required this.generation});

  final String message;
  final int generation;
}

/// Non-cancel error path. [message] is operator-ready (mapped from
/// ApiError or a cluster-pin mismatch). [alreadyRefreshing] is true for
/// the 409 `already_refreshing` case so the UI can soften the surface
/// to an informational snackbar rather than a destructive failure card.
class ForceSyncFailure extends ForceSyncState {
  const ForceSyncFailure({
    required this.message,
    required this.generation,
    this.alreadyRefreshing = false,
  });

  final String message;
  final int generation;
  final bool alreadyRefreshing;
}

/// Family key for the ForceSync controller. Matches
/// [ExternalSecretDetailKey] so the controller and the detail provider
/// share a slot per pinned (cluster, ns, name).
class ForceSyncKey {
  const ForceSyncKey({
    required this.clusterId,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ForceSyncKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, namespace, name);
}

class ForceSyncController
    extends AutoDisposeFamilyNotifier<ForceSyncState, ForceSyncKey>
    with RefreshableController {
  late ForceSyncKey _key;
  int _generation = 0;

  @override
  String get pinnedClusterId => _key.clusterId;

  @override
  String currentActiveClusterId(Ref ref) => ref.read(activeClusterProvider);

  @override
  ForceSyncState build(ForceSyncKey arg) {
    _key = arg;
    initRefreshable(ref);
    return const ForceSyncIdle();
  }

  /// Triggers the POST. Caller is responsible for showing the confirm
  /// sheet first; this method assumes confirmation has happened.
  ///
  /// No-ops when an in-flight request is already running so a fast
  /// double-tap on the Force Sync button doesn't enqueue two POSTs.
  Future<void> forceSync() async {
    if (isDisposed) return;
    if (state is ForceSyncInFlight) return;
    supersede('force-sync triggered');
    final captured = captureDispatchId();
    RefreshableController.safeSetIfAlive<ForceSyncState>(
      isDisposed,
      (s) => state = s,
      const ForceSyncInFlight(),
    );

    try {
      await ref.read(esoRepositoryProvider).forceSync(
            namespace: _key.namespace,
            name: _key.name,
            clusterIdOverride: _key.clusterId,
            cancelToken: currentCancelToken,
          );
      if (!isFresh(captured)) return;
      if (!clusterStillPinned(ref)) {
        _emitFailure(pinnedMismatchMessage(PinPhase.postEmission));
        return;
      }

      // Invalidate the detail provider for the same key so the drift
      // chip re-fetches on the next frame. Per the M4 drift tri-state
      // invariant the intermediate Unknown state renders as textMuted
      // (never red); the chip transitions Drifted → Unknown → InSync.
      ref.invalidate(externalSecretDetailProvider(ExternalSecretDetailKey(
        clusterId: _key.clusterId,
        namespace: _key.namespace,
        name: _key.name,
      )));

      _generation++;
      RefreshableController.safeSetIfAlive<ForceSyncState>(
        isDisposed,
        (s) => state = s,
        ForceSyncSuccess(
          message: 'Force Sync triggered for ${_key.namespace}/${_key.name}',
          generation: _generation,
        ),
      );
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      if (!isFresh(captured)) return;
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      _emitFromApiError(apiErr);
    } on ApiError catch (e) {
      if (!isFresh(captured)) return;
      _emitFromApiError(e);
    } catch (e) {
      if (!isFresh(captured)) return;
      _emitFailure(e.toString());
    }
  }

  /// Resets state back to [ForceSyncIdle]. Called from the consuming
  /// screen's `ref.listen` callback after the success snackbar has been
  /// shown so a follow-up tap can re-trigger the action.
  void acknowledge() {
    if (isDisposed) return;
    if (state is ForceSyncIdle) return;
    RefreshableController.safeSetIfAlive<ForceSyncState>(
      isDisposed,
      (s) => state = s,
      const ForceSyncIdle(),
    );
  }

  void _emitFromApiError(ApiError err) {
    if (err.statusCode == 409 && err.reason == 'already_refreshing') {
      _generation++;
      RefreshableController.safeSetIfAlive<ForceSyncState>(
        isDisposed,
        (s) => state = s,
        ForceSyncFailure(
          message: 'A sync is already in progress for '
              '${_key.namespace}/${_key.name}. Try again in a few seconds.',
          generation: _generation,
          alreadyRefreshing: true,
        ),
      );
      return;
    }
    if (err.statusCode == 501) {
      _emitFailure(
        "Force Sync isn't available for this cluster — only the local "
        'cluster supports ESO writes.',
      );
      return;
    }
    if (err.statusCode == 503) {
      _emitFailure(
        'ESO is not detected on this cluster. Install ESO before triggering '
        'a Force Sync.',
      );
      return;
    }
    if (err.statusCode == 403) {
      _emitFailure(
        "You don't have permission to refresh "
        '${_key.namespace}/${_key.name}.',
      );
      return;
    }
    if (err.statusCode == 404) {
      _emitFailure(
        'ExternalSecret ${_key.namespace}/${_key.name} was not found. '
        'It may have been deleted.',
      );
      return;
    }
    _emitFailure(err.message);
  }

  void _emitFailure(String message) {
    _generation++;
    RefreshableController.safeSetIfAlive<ForceSyncState>(
      isDisposed,
      (s) => state = s,
      ForceSyncFailure(
        message: message,
        generation: _generation,
      ),
    );
  }
}

final forceSyncControllerProvider = AutoDisposeNotifierProvider.family<
    ForceSyncController, ForceSyncState, ForceSyncKey>(
  ForceSyncController.new,
);
