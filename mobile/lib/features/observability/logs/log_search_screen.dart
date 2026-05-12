// Top-level LogQL editor screen. Composes the filter bar, volume
// histogram, and virtual-scroll results list.
//
// Status gating routes through `lokiStatusProvider`. When the cluster
// doesn't have Loki (`detected: false`), the screen renders
// `FeatureUnavailableState.loki()` — the single-pod live tail under
// /clusters/.../workloads/pods/.../logs/... still works as the
// M1-shipped fallback.
//
// This surface is separate from the M1 pod log tail. The pod tail's
// scope is "watch one container live"; this surface's scope is "ad-hoc
// search across multiple pods in a namespace". The plan keeps both
// rather than collapsing them so neither has to compromise on UX.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/loki_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/empty_states.dart';
import '../../../widgets/feature_unavailable_state.dart';
import 'log_filter_bar.dart';
import 'log_results_list.dart';
import 'log_search_controller.dart';
import 'log_volume_histogram.dart';

class LogSearchScreen extends ConsumerWidget {
  const LogSearchScreen({
    this.initialNamespace,
    super.key,
  });

  /// Optional deep-link seed (e.g. notification → "view logs in ns X").
  final String? initialNamespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Log search'),
      ),
      body: _LogSearchBody(
        clusterId: clusterId,
        initialNamespace: initialNamespace,
      ),
    );
  }
}

class _LogSearchBody extends ConsumerWidget {
  const _LogSearchBody({
    required this.clusterId,
    this.initialNamespace,
  });

  final String clusterId;
  final String? initialNamespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(lokiStatusProvider(clusterId));
    return status.when(
      loading: () => const LoadingState(),
      error: (e, _) => ErrorStateView(
        message: 'Failed to query Loki status: $e',
        onRetry: () => ref.invalidate(lokiStatusProvider(clusterId)),
      ),
      data: (s) {
        if (!s.detected) {
          return FeatureUnavailableState.loki();
        }
        return _LogSearchContent(
          clusterId: clusterId,
          initialNamespace: initialNamespace,
        );
      },
    );
  }
}

class _LogSearchContent extends ConsumerWidget {
  const _LogSearchContent({
    required this.clusterId,
    this.initialNamespace,
  });

  final String clusterId;
  final String? initialNamespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(logSearchControllerProvider(clusterId));
    final notifier =
        ref.read(logSearchControllerProvider(clusterId).notifier);

    return RefreshIndicator(
      onRefresh: notifier.refresh,
      child: ListView(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
        children: [
          LogFilterBar(
            initialNamespace: initialNamespace,
            onSubmit: notifier.submit,
            inFlight: state.result is LogQueryLoading,
          ),
          const SizedBox(height: 12),
          if (state.volume is LogVolumeLoaded) ...[
            LogVolumeHistogram(
              result: (state.volume as LogVolumeLoaded).result,
            ),
            const SizedBox(height: 12),
          ] else if (state.volume is LogVolumeLoading)
            const _VolumePlaceholder(),
          _ResultPanel(
            state: state,
            onRetry: notifier.refresh,
          ),
        ],
      ),
    );
  }
}

class _VolumePlaceholder extends StatelessWidget {
  const _VolumePlaceholder();

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 18),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Center(
        child: SizedBox(
          height: 24,
          width: 24,
          child: CircularProgressIndicator(
            strokeWidth: 2,
            color: colors.accent,
          ),
        ),
      ),
    );
  }
}

class _ResultPanel extends StatelessWidget {
  const _ResultPanel({required this.state, required this.onRetry});

  final LogSearchState state;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return switch (state.result) {
      LogQueryIdle() => const _IdlePanel(),
      LogQueryLoading() => const SizedBox(
          height: 120,
          child: LoadingState(),
        ),
      LogQueryFailed(:final message) => ErrorStateView(
          message: message,
          onRetry: () => onRetry(),
        ),
      LogQueryLoaded(:final result) => LogResultsList(result: result),
    };
  }
}

class _IdlePanel extends StatelessWidget {
  const _IdlePanel();

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 32),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.search, size: 40, color: colors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Pick a namespace and press Run to search logs.',
            textAlign: TextAlign.center,
            style: TextStyle(color: colors.textSecondary, fontSize: 14),
          ),
          const SizedBox(height: 4),
          Text(
            'Single-pod live tail is still available from the pod '
            'detail screen.',
            textAlign: TextAlign.center,
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}
