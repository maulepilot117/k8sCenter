// Detail-screen chrome for any kind. Header (kind icon + name + namespace
// + status pill) + tabbed body (Overview, YAML, Events) plus optional
// extra tabs supplied by the caller (M4: Metrics, Logs, Diagnose).
//
// PR-1d ships YAML as a read-only SelectableText render of the raw
// resource map (toJson then JsonEncoder.withIndent). Syntax highlighting
// (code_text_field) lands in PR-1e or M2 to keep this PR's surface area
// reviewable. Events tab is a stub pending PR-1e's events fetch.
//
// `extraTabs` (M4 PR-4a): callers append tabs to the right of Events.
// Order is operator-meaningful — typical M4 layout is Overview / YAML /
// Events / Metrics / Logs. The TabBar becomes scrollable when the
// total tab count exceeds the screen's horizontal capacity.

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/resource_repository.dart';
import '../api/yaml_apply_controller.dart';
import '../cluster/cluster_provider.dart';
import '../features/resources/k8s_helpers.dart';
import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';
import 'yaml_editor_panel.dart';

/// One extra tab entry callers append to the scaffold's default
/// Overview / YAML / Events triplet. Held as a class (not a record)
/// so callers can compare instances and so the type is stable across
/// future field additions.
class DetailExtraTab {
  const DetailExtraTab({required this.label, required this.body});

  final String label;
  final Widget body;
}

class ResourceDetailScaffold extends StatelessWidget {
  const ResourceDetailScaffold({
    super.key,
    required this.kindLabel,
    required this.name,
    required this.resource,
    this.namespace,
    this.statusLabel,
    this.statusColor,
    required this.overview,
    this.icon = Icons.inventory_2_outlined,
    this.isSensitive = false,
    this.overviewScrollable = true,
    this.uid,
    this.trailingAction,
    this.editableYaml = false,
    this.applyKey,
    this.extraTabs = const <DetailExtraTab>[],
  }) : assert(!editableYaml || applyKey != null,
            'editableYaml requires applyKey for the YAML editor panel');

  /// Heuristic the Secret screen sets to true. When true, the YAML tab
  /// redacts `data` and `stringData` fields before rendering — without
  /// this, the YAML tab leaks plaintext base64 values regardless of any
  /// per-key Reveal toggle in the Overview tab.
  const ResourceDetailScaffold.secret({
    Key? key,
    required String name,
    required Map<String, dynamic> resource,
    String? namespace,
    String? statusLabel,
    Color? statusColor,
    required Widget overview,
    IconData icon = Icons.key_outlined,
    String? uid,
    Widget? trailingAction,
    bool editableYaml = false,
    YamlApplyKey? applyKey,
    List<DetailExtraTab> extraTabs = const <DetailExtraTab>[],
  }) : this(
          key: key,
          kindLabel: 'Secret',
          name: name,
          resource: resource,
          namespace: namespace,
          statusLabel: statusLabel,
          statusColor: statusColor,
          overview: overview,
          icon: icon,
          isSensitive: true,
          uid: uid,
          trailingAction: trailingAction,
          editableYaml: editableYaml,
          applyKey: applyKey,
          extraTabs: extraTabs,
        );

  final String kindLabel;
  final String name;
  final String? namespace;
  final String? statusLabel;
  final Color? statusColor;

  /// Full backend resource map; used to drive the YAML tab.
  final Map<String, dynamic> resource;

  /// Kind-specific overview content (slot, not a builder, so list-of-rows
  /// is the ergonomic shape).
  final Widget overview;

  final IconData icon;

  /// True when this resource carries sensitive payloads (Secrets,
  /// service-account tokens). The YAML tab redacts `data` and
  /// `stringData` before rendering when set — defends against the
  /// "operator copies plaintext from YAML tab and bypasses the Reveal
  /// gate" failure mode.
  final bool isSensitive;

  /// When false, the Overview tab renders [overview] directly without
  /// wrapping it in a SingleChildScrollView. Use for screens that need
  /// to provide their own scroll body (e.g., ConfigMap detail uses a
  /// CustomScrollView with a SliverList for many-key virtualization).
  final bool overviewScrollable;

  /// Optional metadata.uid; required for the Events tab to filter
  /// events by `involvedObject.uid`. When null, Events tab falls back
  /// to filtering by kind+namespace+name (less precise on recreated
  /// resources but still correct for the common case).
  final String? uid;

  /// Optional widget rendered in the app-bar actions slot (typically a
  /// [ResourceActionsButton]). Renders before the status pill so action
  /// affordances are reachable on both phone and tablet without a
  /// separate FloatingActionButton — operators reach for the top-right
  /// when they want to act on the focused resource.
  final Widget? trailingAction;

  /// When true, the YAML tab swaps the read-only [_YamlTab] for the
  /// edit-mode [YamlEditorPanel]. M2 ships this for ConfigMap and Secret
  /// detail; other kinds will get it as wizards land in M3.
  final bool editableYaml;

  /// Composite key for the YAML edit state machine. Required when
  /// [editableYaml] is true.
  final YamlApplyKey? applyKey;

  /// Caller-appended tabs rendered after Events. Empty by default —
  /// the M1 detail screens render the canonical 3-tab layout. M4
  /// per-resource Metrics + Logs tabs land here.
  final List<DetailExtraTab> extraTabs;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final extraCount = extraTabs.length;
    return DefaultTabController(
      length: 3 + extraCount,
      child: Scaffold(
        appBar: AppBar(
          title: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                name,
                style: TextStyle(color: colors.textPrimary, fontSize: 16),
                overflow: TextOverflow.ellipsis,
              ),
              Text(
                namespace == null ? kindLabel : '$kindLabel · $namespace',
                style: TextStyle(color: colors.textMuted, fontSize: 12),
              ),
            ],
          ),
          leading: IconButton(
            icon: const Icon(Icons.arrow_back),
            onPressed: () => Navigator.of(context).maybePop(),
          ),
          actions: [
            ?trailingAction,
            if (statusLabel != null)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Center(
                  child: _StatusPill(
                    label: statusLabel!,
                    color: statusColor ?? colors.accent,
                  ),
                ),
              ),
          ],
          bottom: TabBar(
            // Becomes scrollable when extra tabs push the row past
            // the screen's horizontal capacity. Default 3-tab layout
            // stays non-scrollable (extraCount == 0).
            isScrollable: extraCount > 0,
            tabAlignment: extraCount > 0 ? TabAlignment.start : null,
            tabs: [
              const Tab(text: 'Overview'),
              const Tab(text: 'YAML'),
              const Tab(text: 'Events'),
              for (final t in extraTabs) Tab(text: t.label),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            if (overviewScrollable)
              SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: overview,
              )
            else
              overview,
            if (editableYaml && applyKey != null)
              YamlEditorPanel(
                applyKey: applyKey!,
                resource: resource,
                stripSensitiveDataFields: isSensitive,
                headerWarning: isSensitive
                    ? Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 12,
                            vertical: 8,
                          ),
                          decoration: BoxDecoration(
                            color: colors.warningDim,
                            borderRadius: BorderRadius.circular(6),
                          ),
                          child: Row(
                            children: [
                              Icon(Icons.lock_outline,
                                  size: 16, color: colors.warning),
                              const SizedBox(width: 8),
                              Expanded(
                                child: Text(
                                  'Secret data and stringData are stripped '
                                  'from the editor — server-side apply leaves '
                                  'existing credential values untouched.',
                                  style: TextStyle(
                                    color: colors.warning,
                                    fontSize: 12,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      )
                    : null,
              )
            else
              _YamlTab(resource: resource, sensitive: isSensitive),
            EventsTab(
              kind: kindLabel,
              namespace: namespace,
              name: name,
              uid: uid,
            ),
            for (final t in extraTabs) t.body,
          ],
        ),
      ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}

class _YamlTab extends StatelessWidget {
  const _YamlTab({required this.resource, required this.sensitive});

  final Map<String, dynamic> resource;
  final bool sensitive;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final source = sensitive ? _redactSensitive(resource) : resource;
    const encoder = JsonEncoder.withIndent('  ');
    final pretty = encoder.convert(source);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (sensitive)
            Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 8,
                ),
                decoration: BoxDecoration(
                  color: colors.warningDim,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Row(
                  children: [
                    Icon(Icons.lock_outline, size: 16, color: colors.warning),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        'Sensitive fields (data, stringData) are redacted. '
                        'Use the Overview tab\'s per-key Reveal action.',
                        style: TextStyle(
                          color: colors.warning,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          SelectableText(
            pretty,
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              color: colors.textPrimary,
              height: 1.4,
            ),
          ),
        ],
      ),
    );
  }
}

/// Returns a deep copy of the resource map with sensitive payload fields
/// redacted. Currently scrubs `data` and `stringData` (Secret + a few
/// adjacent kinds with similar shapes); preserves metadata, type, and
/// non-sensitive fields so the operator can still verify the structure.
Map<String, dynamic> _redactSensitive(Map<String, dynamic> input) {
  final out = <String, dynamic>{};
  for (final entry in input.entries) {
    if (entry.key == 'data' || entry.key == 'stringData') {
      final v = entry.value;
      if (v is Map) {
        out[entry.key] = {
          for (final k in v.keys) k: '<redacted: tap Reveal in Overview>',
        };
      } else {
        out[entry.key] = '<redacted>';
      }
    } else {
      out[entry.key] = entry.value;
    }
  }
  return out;
}


/// Helper for kind-specific overviews — renders a label/value row.
class DetailRow extends StatelessWidget {
  const DetailRow({super.key, required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 140,
            child: Text(
              label,
              style: TextStyle(color: colors.textSecondary, fontSize: 13),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: TextStyle(color: colors.textPrimary, fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}

/// Section header inside an overview.
class DetailSection extends StatelessWidget {
  const DetailSection({super.key, required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      margin: const EdgeInsets.only(bottom: 12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
            ),
          ),
          const SizedBox(height: 8),
          child,
        ],
      ),
    );
  }
}

/// Events tab — fetches `events` from the resource's namespace and
/// filters by `involvedObject`. Falls back to kind+name match when no
/// uid is supplied (older detail callers, cluster-scoped resources).
class EventsTab extends ConsumerWidget {
  const EventsTab({
    super.key,
    required this.kind,
    required this.namespace,
    required this.name,
    this.uid,
  });

  final String kind;
  final String? namespace;
  final String name;
  final String? uid;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    // Events live in the same namespace as their target. For
    // cluster-scoped resources (namespace is null/empty) we have no
    // precise filter without a uid — UID match across the cross-namespace
    // event list is the only correct option, but a cluster-wide fetch
    // capped at `defaultListLimit` may truncate before reaching the
    // matching events on busy clusters. Without a uid, we cannot match
    // precisely, so render an explicit empty-state instead of fetching.
    final ns = (namespace == null || namespace!.isEmpty) ? null : namespace;
    if (ns == null && (uid == null || uid!.isEmpty)) {
      final colors = Theme.of(context).extension<KubeColors>()!;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Events not available for cluster-scoped resources without uid.',
            style: TextStyle(color: colors.textMuted),
            textAlign: TextAlign.center,
          ),
        ),
      );
    }
    final key = ResourceListKey(
      clusterId: clusterId,
      kind: 'events',
      namespace: ns,
    );
    final events = ref.watch(resourceListProvider(key));
    final colors = Theme.of(context).extension<KubeColors>()!;

    return events.when(
      loading: () => const LoadingState(),
      error: (e, _) => ErrorStateView(
        message: e.toString(),
        onRetry: () => ref.invalidate(resourceListProvider(key)),
      ),
      data: (result) {
        final filtered = result.items.where((e) => _matches(e)).toList()
          ..sort(_byLastTimestampDesc);
        if (filtered.isEmpty) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(Icons.event_note_outlined,
                      size: 48, color: colors.textMuted),
                  const SizedBox(height: 12),
                  Text(
                    'No events for this resource',
                    style: TextStyle(color: colors.textSecondary),
                  ),
                ],
              ),
            ),
          );
        }
        return ListView.builder(
          padding: const EdgeInsets.symmetric(vertical: 8),
          itemCount: filtered.length,
          itemBuilder: (context, i) => _EventTile(event: filtered[i]),
        );
      },
    );
  }

  /// Matches an event row to this resource. Prefers UID equality (the
  /// only correct signal across resource recreation) and falls back to
  /// kind+name+namespace tuple when uid is unavailable.
  bool _matches(Map<String, dynamic> event) {
    final involved =
        event['involvedObject'] as Map<String, dynamic>? ?? const {};
    if (uid != null && uid!.isNotEmpty) {
      return involved['uid'] == uid;
    }
    final involvedKind = involved['kind'] as String? ?? '';
    final involvedName = involved['name'] as String? ?? '';
    final involvedNs = involved['namespace'] as String? ?? '';
    if (!_kindMatches(involvedKind, kind)) return false;
    if (involvedName != name) return false;
    if (namespace == null || namespace!.isEmpty) return true;
    return involvedNs == namespace;
  }

  /// Tolerant Kubernetes-Kind comparison. The hint may arrive as the
  /// canonical Kind ("Ingress") from a specialized detail screen, or as
  /// the URL route segment ("ingresses", "namespaces") from the
  /// generic-detail fallback. Kubernetes events always carry the
  /// canonical Kind in `involvedObject.kind`, so this normalizer accepts
  /// either side adding a trailing 's' or 'es'.
  bool _kindMatches(String involvedKind, String hint) {
    final a = involvedKind.toLowerCase();
    final b = hint.toLowerCase();
    if (a == b) return true;
    return a == '${b}s' ||
        a == '${b}es' ||
        b == '${a}s' ||
        b == '${a}es';
  }

  int _byLastTimestampDesc(
      Map<String, dynamic> a, Map<String, dynamic> b) {
    final ta = _eventTimestamp(a);
    final tb = _eventTimestamp(b);
    return tb.compareTo(ta);
  }

  String _eventTimestamp(Map<String, dynamic> e) {
    return (e['lastTimestamp'] as String?) ??
        (e['eventTime'] as String?) ??
        ((e['metadata'] as Map?)?['creationTimestamp'] as String?) ??
        '';
  }
}

class _EventTile extends StatelessWidget {
  const _EventTile({required this.event});

  final Map<String, dynamic> event;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final type = event['type'] as String? ?? 'Normal';
    final reason = event['reason'] as String? ?? '—';
    final message = event['message'] as String? ?? '';
    final count = (event['count'] as num?)?.toInt() ?? 1;
    final ts = (event['lastTimestamp'] as String?) ??
        (event['eventTime'] as String?) ??
        ((event['metadata'] as Map?)?['creationTimestamp'] as String?) ??
        '';
    final isWarning = type == 'Warning';
    final tone = isWarning ? colors.warning : colors.accent;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: tone.withValues(alpha: 0.16),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Text(
                  type,
                  style: TextStyle(
                    color: tone,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                reason,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
              const Spacer(),
              if (count > 1)
                Text(
                  '×$count',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
              const SizedBox(width: 8),
              Text(
                formatAge(ts),
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ],
          ),
          if (message.isNotEmpty) ...[
            const SizedBox(height: 6),
            SelectableText(
              message,
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
          ],
        ],
      ),
    );
  }
}
