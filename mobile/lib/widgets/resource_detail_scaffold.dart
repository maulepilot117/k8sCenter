// Detail-screen chrome for any kind. Header (kind icon + name + namespace
// + status pill) + tabbed body (Overview, YAML, Events).
//
// PR-1d ships YAML as a read-only SelectableText render of the raw
// resource map (toJson then JsonEncoder.withIndent). Syntax highlighting
// (code_text_field) lands in PR-1e or M2 to keep this PR's surface area
// reviewable. Events tab is a stub pending PR-1e's events fetch.

import 'dart:convert';

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

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
  });

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

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return DefaultTabController(
      length: 3,
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
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Overview'),
              Tab(text: 'YAML'),
              Tab(text: 'Events'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            SingleChildScrollView(
              padding: const EdgeInsets.all(16),
              child: overview,
            ),
            _YamlTab(resource: resource, sensitive: isSensitive),
            const _EventsPlaceholder(),
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

class _EventsPlaceholder extends StatelessWidget {
  const _EventsPlaceholder();

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.event_note_outlined, size: 48, color: colors.textMuted),
            const SizedBox(height: 12),
            Text(
              'Events arrive in PR-1e',
              style: TextStyle(color: colors.textSecondary),
            ),
          ],
        ),
      ),
    );
  }
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
