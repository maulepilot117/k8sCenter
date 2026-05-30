// Notification feed list. Pull-to-refresh, severity-tinted rows, tap to
// mark read + deep-link to the affected resource (when carried). Filter
// chips for severity and source live in a top sliver so they survive
// rebuilds — read state changes only invalidate the list query.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../cluster/cluster_provider.dart';
import '../../cluster/cluster_repository.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'feed_repository.dart';

class NotificationFeedScreen extends ConsumerWidget {
  const NotificationFeedScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final feed = ref.watch(notificationsFeedProvider);
    final unread = ref.watch(unreadCountProvider).asData?.value ?? 0;

    return Scaffold(
      appBar: AppBar(
        title: MergeSemantics(
          child: Row(
            children: [
              const Text('Notifications'),
              const SizedBox(width: 8),
              if (unread > 0)
                Semantics(
                  label: '$unread unread notifications',
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 8,
                      vertical: 2,
                    ),
                    decoration: BoxDecoration(
                      color: colors.accent,
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: ExcludeSemantics(
                      child: Text(
                        '$unread',
                        style: const TextStyle(
                          color: Colors.white,
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
        actions: [
          if (unread > 0)
            TextButton(
              onPressed: () async {
                try {
                  await ref
                      .read(notificationsRepositoryProvider)
                      .markAllRead();
                } on ApiError {
                  if (!context.mounted) return;
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                      content: Text('Could not mark all as read'),
                    ),
                  );
                  return;
                }
                ref.invalidate(notificationsFeedProvider);
                ref.invalidate(unreadCountProvider);
              },
              child: const Text('Mark all read'),
            ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(notificationsFeedProvider);
          ref.invalidate(unreadCountProvider);
          await ref.read(notificationsFeedProvider.future);
        },
        child: feed.when(
          loading: () => const Center(child: LoadingState()),
          error: (e, _) => ErrorStateView(
            message: e.toString(),
            onRetry: () => ref.invalidate(notificationsFeedProvider),
          ),
          data: (page) {
            if (page.items.isEmpty) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: const [
                  SizedBox(height: 200),
                  Center(child: Text('No notifications')),
                ],
              );
            }
            // The feed is fetched with the default limit (50) and no
            // load-more, so when the backend reports more items than were
            // returned, surface an honest footer instead of silently
            // dropping the remainder. The footer occupies a synthetic
            // trailing index, so the separator/item builders guard against
            // it to avoid an out-of-range read into page.items.
            final truncated = page.total > page.items.length;
            final itemCount = page.items.length + (truncated ? 1 : 0);
            return ListView.separated(
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: itemCount,
              separatorBuilder: (_, _) =>
                  Divider(height: 1, color: colors.borderSubtle),
              itemBuilder: (context, i) {
                if (truncated && i == page.items.length) {
                  return Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 16,
                    ),
                    child: Center(
                      child: Text(
                        'Showing ${page.items.length} of ${page.total}',
                        style: TextStyle(
                          color: colors.textMuted,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  );
                }
                return _NotificationTile(item: page.items[i]);
              },
            );
          },
        ),
      ),
    );
  }
}

class _NotificationTile extends ConsumerWidget {
  const _NotificationTile({required this.item});
  final NotificationItem item;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final severityColor = switch (item.severity.toLowerCase()) {
      'critical' || 'error' => colors.error,
      'warning' => colors.warning,
      'info' => colors.accent,
      _ => colors.textMuted,
    };
    final unreadDot = item.read
        ? const SizedBox(width: 8)
        : ExcludeSemantics(
            child: Container(
              width: 8,
              height: 8,
              margin: const EdgeInsets.only(right: 4),
              decoration: BoxDecoration(
                color: colors.accent,
                shape: BoxShape.circle,
              ),
            ),
          );

    // Extracted so the outer Semantics can expose the tap action to the
    // accessibility tree without duplicating the closure. ExcludeSemantics
    // below hides the InkWell's own tap from the a11y tree (avoiding a
    // doubled announce of the inner Text children), so the Semantics
    // wrapper's onTap is the sole AT-reachable activation path.
    Future<void> onTap() async {
      if (!item.read) {
        try {
          await ref.read(notificationsRepositoryProvider).markRead(item.id);
          ref.invalidate(notificationsFeedProvider);
          ref.invalidate(unreadCountProvider);
        } on ApiError {
          // A transient read-flag write failure must NOT block the
          // deep-link drill-down the user tapped. Surface a SnackBar
          // and fall through to the mounted-check + navigation below.
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(content: Text('Could not mark as read')),
            );
          }
        }
      }
      if (!context.mounted) return;
      if (item.hasResourceTarget) {
        final clusterId = item.clusterId ?? 'local';
        final ns = item.resourceNamespace ?? '';
        final canonicalKind = item.resourceKind ?? '';
        // If the notification points at a different cluster than the
        // active one, switch clusters before navigating — otherwise
        // the X-Cluster-ID header on the detail fetch would carry
        // the active cluster and silently 404. Cross-cluster
        // notifications normally don't appear in the feed (the
        // backend filters server-side), so this is defensive.
        //
        // The clusterId comes from an untrusted notification payload, so
        // only honor the swap when it names a cluster the user actually
        // has registered. Reads the already-resolved clustersProvider
        // snapshot synchronously (never awaits); a not-yet-loaded list
        // leaves the active cluster untouched rather than trusting the
        // payload. Mirrors _isKnownCluster in app_router.dart.
        final activeCluster = ref.read(activeClusterProvider);
        final knownClusters = ref.read(clustersProvider).valueOrNull;
        final isKnown =
            knownClusters?.clusters.any((c) => c.id == clusterId) ?? false;
        if (clusterId != activeCluster && isKnown) {
          ref.read(activeClusterProvider.notifier).setCluster(clusterId);
        }
        // Use the kind segment as-is; kindDetailPath() falls back to
        // the generic-detail catch-all when the canonical Kind isn't
        // a registered specialized screen.
        final path = kindDetailPath(
          clusterId: clusterId,
          kind: canonicalKind.toLowerCase(),
          namespace: ns,
          name: item.resourceName ?? '',
        );
        context.push(path);
      }
    }

    return Semantics(
      button: item.hasResourceTarget,
      label: '${item.read ? '' : 'Unread. '}${item.severity} notification: ${item.title}${item.message.isNotEmpty ? '. ${item.message}' : ''}${item.hasResourceTarget ? '. Tap to view resource.' : ''}',
      onTap: item.hasResourceTarget ? onTap : null,
      child: ExcludeSemantics(
        child: InkWell(
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
            color: item.read ? null : colors.bgElevated,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                unreadDot,
                const SizedBox(width: 8),
                _SeverityChip(label: item.severity, color: severityColor),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              item.title,
                              style: TextStyle(
                                color: colors.textPrimary,
                                fontSize: 14,
                                fontWeight: item.read
                                    ? FontWeight.w400
                                    : FontWeight.w600,
                              ),
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                          Text(
                            _shortAge(item.createdAt),
                            style: TextStyle(
                              color: colors.textMuted,
                              fontSize: 11,
                            ),
                          ),
                        ],
                      ),
                      if (item.message.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Text(
                          item.message,
                          style: TextStyle(
                              color: colors.textSecondary, fontSize: 12),
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ],
                      if (item.hasResourceTarget) ...[
                        const SizedBox(height: 4),
                        Text(
                          '${item.resourceKind} · '
                          '${item.resourceNamespace?.isEmpty ?? true ? "<cluster>" : item.resourceNamespace} · '
                          '${item.resourceName}',
                          style: TextStyle(
                            color: colors.textMuted,
                            fontSize: 11,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                if (item.hasResourceTarget)
                  Icon(Icons.chevron_right, color: colors.textMuted, size: 18),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _SeverityChip extends StatelessWidget {
  const _SeverityChip({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    // Feature-local chip: wrap with Semantics so TalkBack/VoiceOver reads
    // the severity domain prefix rather than just the bare label text.
    return Semantics(
      container: true,
      label: 'Severity: $label',
      child: ExcludeSemantics(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.16),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: color.withValues(alpha: 0.4)),
          ),
          child: Text(
            label.toLowerCase(),
            style: TextStyle(
              color: color,
              fontSize: 10,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ),
    );
  }
}

String _shortAge(DateTime t) {
  final delta = DateTime.now().difference(t);
  if (delta.inDays >= 1) return '${delta.inDays}d';
  if (delta.inHours >= 1) return '${delta.inHours}h';
  if (delta.inMinutes >= 1) return '${delta.inMinutes}m';
  return 'now';
}
