// Per-resource diagnostics screen. Header pulls the target tuple from
// the route params, body composes the rules-engine checklist + the
// two-tier blast-radius panel.
//
// Loading / error / success states route through the canonical
// `LoadingState` + `ErrorStateView` widgets so retry behaviour matches
// the rest of the app. Pull-to-refresh re-fires `_fetch` via
// `controller.refresh()`; the `supersede()` discipline inside the
// controller guarantees a fast-fingered operator can't queue two
// concurrent diagnostics calls that race each other to the state slot.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/api_error.dart';
import '../../../api/diagnostics_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/empty_states.dart';
import 'blast_radius_panel.dart';
import 'diagnostic_checklist.dart';
import 'diagnostics_controller.dart';
import 'scrollable_center.dart';

class DiagnosticsScreen extends ConsumerWidget {
  const DiagnosticsScreen({
    super.key,
    required this.namespace,
    required this.kind,
    required this.name,
  });

  final String namespace;
  final String kind;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final target = DiagnosticTarget(
      clusterId: clusterId,
      namespace: namespace,
      kind: kind,
      name: name,
    );
    final state = ref.watch(diagnosticsControllerProvider(target));
    final notifier =
        ref.read(diagnosticsControllerProvider(target).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'Diagnose',
              style: TextStyle(color: colors.textPrimary, fontSize: 16),
            ),
            Text(
              '$kind · $name · $namespace',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
      body: RefreshIndicator(
        onRefresh: notifier.refresh,
        child: state.when(
          loading: () =>
              const ScrollableCenter(child: LoadingState(message: 'Running diagnostics…')),
          error: (e, _) => ScrollableCenter(
            child: ErrorStateView(
              message: _humanise(e),
              onRetry: notifier.refresh,
            ),
          ),
          data: (response) => _DiagnosticsBody(
            clusterId: clusterId,
            namespace: namespace,
            response: response,
          ),
        ),
      ),
    );
  }

  String _humanise(Object err) {
    if (err is ApiError) {
      if (err.statusCode == 400) {
        return 'Diagnostics is not supported for $kind. '
            'Open a Pod, Deployment, StatefulSet, DaemonSet, Service, '
            'or PersistentVolumeClaim instead.';
      }
      if (err.statusCode == 404) {
        return '$kind $name was not found in $namespace. The resource '
            'may have been deleted while you were navigating.';
      }
      if (err.statusCode == 403) {
        return 'You don\'t have permission to diagnose this $kind. '
            'You need list access on $kind to run diagnostic rules.';
      }
      if (err.statusCode == 409) {
        return err.message;
      }
      if (err.statusCode == 500 &&
          err.message.toLowerCase().contains('timeout')) {
        return 'Diagnostics timed out after 15 seconds. Topology builds '
            'are best-effort on large namespaces — retry, or open this '
            'resource on a desktop for the full graph.';
      }
      return err.message;
    }
    return err.toString();
  }
}

class _DiagnosticsBody extends StatelessWidget {
  const _DiagnosticsBody({
    required this.clusterId,
    required this.namespace,
    required this.response,
  });

  final String clusterId;
  final String namespace;
  final DiagnosticResponse response;

  @override
  Widget build(BuildContext context) {
    return ListView(
      // AlwaysScrollable so RefreshIndicator pull works even when the
      // body content fits inside the viewport.
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      children: [
        DiagnosticChecklist(
          clusterId: clusterId,
          namespace: namespace,
          failed: response.failedResults,
          passed: response.passedResults,
        ),
        const SizedBox(height: 24),
        BlastRadiusPanel(
          clusterId: clusterId,
          namespace: namespace,
          blastRadius: response.blastRadius,
        ),
      ],
    );
  }
}

