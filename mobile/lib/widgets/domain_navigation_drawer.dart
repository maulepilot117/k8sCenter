// Drawer rendering DOMAIN_SECTIONS. Tap a kind to navigate to its list
// screen. Closes the drawer after navigation.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../cluster/cluster_provider.dart';
import '../features/notifications_center/feed_repository.dart';
import '../routing/domain_sections.dart';
import '../theme/kube_theme_builder.dart';

class DomainNavigationDrawer extends ConsumerWidget {
  const DomainNavigationDrawer({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final clusterId = ref.watch(activeClusterProvider);

    return Drawer(
      child: ListView(
        padding: EdgeInsets.zero,
        children: [
          DrawerHeader(
            decoration: BoxDecoration(color: colors.bgSurface),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                Text(
                  'k8sCenter',
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'cluster: $clusterId',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ],
            ),
          ),
          ListTile(
            leading: Icon(Icons.cloud_outlined, color: colors.accent),
            title: const Text('Dashboard'),
            onTap: () {
              Navigator.of(context).pop();
              context.go('/');
            },
          ),
          ListTile(
            leading: Icon(Icons.notifications_outlined, color: colors.accent),
            title: const Text('Notifications'),
            trailing: _UnreadBadge(),
            onTap: () {
              Navigator.of(context).pop();
              context.go('/notifications');
            },
          ),
          for (final section in domainSections) ...[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
              child: Text(
                section.label.toUpperCase(),
                style: TextStyle(
                  color: colors.textMuted,
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.8,
                ),
              ),
            ),
            for (final kind in section.kinds)
              ListTile(
                key: ValueKey('drawer-kind-${kind.kind}'),
                leading: Icon(kind.icon, color: colors.textSecondary, size: 20),
                title: Text(kind.label),
                dense: true,
                onTap: () {
                  Navigator.of(context).pop();
                  context.go(
                    '/clusters/$clusterId/${section.pathSegment}/${kind.kind}',
                  );
                },
              ),
          ],
        ],
      ),
    );
  }
}

/// Drawer trailing widget — shows unread notification count.
/// Reads `unreadCountProvider`; renders nothing when zero or loading.
class _UnreadBadge extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final count = ref.watch(unreadCountProvider).asData?.value ?? 0;
    if (count == 0) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: colors.accent,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        count > 99 ? '99+' : '$count',
        style: const TextStyle(
          color: Colors.white,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
