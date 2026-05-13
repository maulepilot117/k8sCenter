// Top-level mesh dashboard. Two side-by-side cards (Istio / Linkerd)
// with installed status, control-plane namespace, version, and mode
// for Istio. When neither mesh is installed renders the standard
// `FeatureUnavailableState.mesh()`.
//
// Navigation: each card carries CTAs to the routing list and the
// mTLS posture screen. The mTLS screen requires a namespace; on tap
// without an active namespace the user lands on the screen and
// receives the namespace prompt.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'mesh_widgets.dart';

class MeshDashboardScreen extends ConsumerWidget {
  const MeshDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(meshStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('Service Mesh')),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(meshStatusProvider(clusterId));
          try {
            await ref.read(meshStatusProvider(clusterId).future);
          } on Object {
            // surfaces via .when error branch
          }
        },
        child: statusAsync.when(
          loading: () =>
              const _ScrollableShell(child: Center(child: CircularProgressIndicator())),
          error: (e, _) {
            if (e is ApiError && (e.statusCode == 401 || e.statusCode == 403)) {
              return _ScrollableShell(
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(
                      'Access denied: ${e.message}',
                      textAlign: TextAlign.center,
                    ),
                  ),
                ),
              );
            }
            return _ScrollableShell(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(e.toString(), textAlign: TextAlign.center),
              ),
            );
          },
          data: (status) {
            if (!status.isInstalled) {
              return _ScrollableShell(child: FeatureUnavailableState.mesh());
            }
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(12, 12, 12, 24),
              children: [
                _EngineCard(
                  name: 'Istio',
                  info: status.istio,
                  accentIcon: Icons.hub_outlined,
                  clusterId: clusterId,
                  meshKey: 'istio',
                ),
                const SizedBox(height: 12),
                _EngineCard(
                  name: 'Linkerd',
                  info: status.linkerd,
                  accentIcon: Icons.linked_camera_outlined,
                  clusterId: clusterId,
                  meshKey: 'linkerd',
                ),
                const SizedBox(height: 16),
                _Actions(clusterId: clusterId),
                if (status.lastChecked != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 12, left: 4),
                    child: _LastCheckedLine(timestamp: status.lastChecked!),
                  ),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _EngineCard extends StatelessWidget {
  const _EngineCard({
    required this.name,
    required this.info,
    required this.accentIcon,
    required this.clusterId,
    required this.meshKey,
  });

  final String name;
  final MeshInfo info;
  final IconData accentIcon;
  final String clusterId;

  /// `"istio"` | `"linkerd"` — drives the routing list deep-link's
  /// `mesh=` filter so taps on the Istio card pre-filter the list.
  final String meshKey;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final accent = info.installed ? colors.success : colors.textMuted;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(accentIcon, color: accent),
                const SizedBox(width: 8),
                Text(
                  name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontWeight: FontWeight.w700,
                    fontSize: 16,
                  ),
                ),
                const Spacer(),
                MeshPill(
                  label: info.installed ? 'Installed' : 'Not installed',
                  color: accent,
                ),
              ],
            ),
            const SizedBox(height: 8),
            if (info.installed) ...[
              if (info.namespace != null)
                MeshKvLine(label: 'Namespace', value: info.namespace!),
              if (info.version != null)
                MeshKvLine(label: 'Version', value: info.version!),
              if (info.mode != null) MeshKvLine(label: 'Mode', value: info.mode!),
              if (info.namespace == null &&
                  info.version == null &&
                  info.mode == null)
                Text(
                  'Details hidden for non-admin users.',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
            ] else
              Text(
                '$name is not installed on this cluster. The routing, mTLS, '
                'and golden-signal surfaces will be empty.',
                style: TextStyle(color: colors.textMuted, fontSize: 12),
              ),
          ],
        ),
      ),
    );
  }
}

class _Actions extends StatelessWidget {
  const _Actions({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          OutlinedButton.icon(
            onPressed: () =>
                context.push('/clusters/$clusterId/mesh/routing'),
            icon: const Icon(Icons.alt_route),
            label: const Text('Routing rules'),
          ),
          OutlinedButton.icon(
            onPressed: () => context.push('/clusters/$clusterId/mesh/mtls'),
            icon: const Icon(Icons.lock_outline),
            label: const Text('mTLS posture'),
          ),
        ],
      ),
    );
  }
}

class _LastCheckedLine extends StatelessWidget {
  const _LastCheckedLine({required this.timestamp});

  final String timestamp;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Text(
      'Last checked: $timestamp',
      style: TextStyle(color: colors.textMuted, fontSize: 11),
    );
  }
}

/// Same scrollable wrapper trick as DomainListScaffold — gives the
/// RefreshIndicator scroll physics when the data branch is empty.
class _ScrollableShell extends StatelessWidget {
  const _ScrollableShell({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [SizedBox(height: 320, child: child)],
    );
  }
}
