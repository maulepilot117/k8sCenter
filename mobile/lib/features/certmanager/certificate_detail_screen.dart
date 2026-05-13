// Certificate detail — three tabs (Overview / Sub-Resources / Events)
// rendered against the backend's `CertificateDetail` envelope. Renew +
// Reissue buttons live in the app-bar actions slot and post to the
// cert-manager-scoped endpoints (`/v1/certificates/.../{renew,reissue}`)
// rather than the generic resource action endpoint — matching the web's
// `frontend/islands/CertificateDetail.tsx` pattern.
//
// Overview tab renders:
//   * Status pill + ExpiryBadge (computed from resolved thresholds)
//   * Issuer, secret, common name, DNS names, validity timestamps
//   * Threshold attribution row (per-key source, with a "Conflict —
//     using defaults" badge when `thresholdConflict: true`)
//   * Reason / message when present
//
// Sub-Resources tab renders three sections (CertificateRequests,
// Orders, Challenges). Empty sections collapse to a hint. When the cert
// is in Issuing or Failed state and no CertificateRequests are visible
// we surface an RBAC hint — namespace-scoped operators commonly can
// see the cert but not the underlying CR list.
//
// Events tab reuses the shared [EventsTab] widget from
// resource_detail_scaffold so cert events render identically to every
// other resource's events tab.
//
// Type-to-confirm contract: Reissue uses the mobile [confirm_sheet]
// pattern (the canonical confirmation surface, per the mobile
// invariants in CLAUDE.md). Renew uses a simple OK/Cancel since it's
// non-destructive.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/certmanager_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart' show EventsTab;
import 'cert_badges.dart';

class CertificateDetailScreen extends ConsumerStatefulWidget {
  const CertificateDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  ConsumerState<CertificateDetailScreen> createState() =>
      _CertificateDetailScreenState();
}

class _CertificateDetailScreenState
    extends ConsumerState<CertificateDetailScreen> {
  bool _busy = false;
  String? _actionMsg;

  CertificateDetailKey _key(String clusterId) => CertificateDetailKey(
        clusterId: clusterId,
        namespace: widget.namespace,
        name: widget.name,
      );

  Future<void> _handleRenew() async {
    final ok = await showConfirmSheet(
      context: context,
      title: 'Renew certificate',
      message:
          'Trigger a renewal of "${widget.name}" in namespace "${widget.namespace}". '
          'cert-manager will request a new certificate from the issuer; the existing '
          'Secret stays in place until the new cert is signed.',
      confirmLabel: 'Renew',
    );
    if (ok != true || !mounted) return;
    await _runAction(
      action: () async {
        final clusterId = ref.read(activeClusterProvider);
        await ref.read(certManagerRepositoryProvider).renew(
              namespace: widget.namespace,
              name: widget.name,
              clusterIdOverride: clusterId,
            );
      },
      successMessage: 'Renewal triggered',
    );
  }

  Future<void> _handleReissue(Certificate cert) async {
    final ok = await showConfirmSheet(
      context: context,
      title: 'Re-issue certificate',
      message:
          'Re-issue will delete Secret "${cert.secretName}" in "${cert.namespace}". '
          'Applications using this Secret will briefly lose TLS until '
          'cert-manager completes re-issuance.',
      confirmLabel: 'Re-issue',
      danger: true,
      // Type-to-confirm gates the destructive verb on the cert name —
      // mirrors the M2 delete pattern and the mobile invariant that
      // type-to-confirm is the single destructive confirmation surface.
      typeToConfirm: widget.name,
    );
    if (ok != true || !mounted) return;
    await _runAction(
      action: () async {
        final clusterId = ref.read(activeClusterProvider);
        await ref.read(certManagerRepositoryProvider).reissue(
              namespace: widget.namespace,
              name: widget.name,
              clusterIdOverride: clusterId,
            );
      },
      successMessage: 'Re-issue triggered',
    );
  }

  Future<void> _runAction({
    required Future<void> Function() action,
    required String successMessage,
  }) async {
    setState(() {
      _busy = true;
      _actionMsg = null;
    });
    try {
      await action();
      if (!mounted) return;
      setState(() {
        _actionMsg = '$successMessage · refreshing…';
      });
      // Invalidate the detail provider so the next build re-fetches the
      // updated cert (renew flips Status → Issuing; reissue triggers a
      // CertificateRequest creation that will appear in Sub-Resources).
      final clusterId = ref.read(activeClusterProvider);
      ref.invalidate(certificateDetailProvider(_key(clusterId)));
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() => _actionMsg = 'Action failed: ${e.message}');
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _actionMsg = 'Action failed: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final detailAsync =
        ref.watch(certificateDetailProvider(_key(clusterId)));
    return DefaultTabController(
      length: 3,
      child: Scaffold(
        appBar: AppBar(
          title: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                widget.name,
                style: const TextStyle(fontSize: 16),
                overflow: TextOverflow.ellipsis,
              ),
              Text(
                'Certificate · ${widget.namespace}',
                style: TextStyle(
                  fontSize: 12,
                  color:
                      Theme.of(context).extension<KubeColors>()!.textMuted,
                ),
              ),
            ],
          ),
          leading: IconButton(
            icon: const Icon(Icons.arrow_back),
            onPressed: () => Navigator.of(context).maybePop(),
          ),
          actions: [
            detailAsync.maybeWhen(
              data: (detail) => Padding(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                child: _ActionButtons(
                  busy: _busy,
                  onRenew: _handleRenew,
                  onReissue: () => _handleReissue(detail.certificate),
                ),
              ),
              orElse: () => const SizedBox.shrink(),
            ),
          ],
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Overview'),
              Tab(text: 'Sub-Resources'),
              Tab(text: 'Events'),
            ],
          ),
        ),
        body: Column(
          children: [
            if (_actionMsg != null) _ActionToast(message: _actionMsg!),
            Expanded(
              child: detailAsync.when(
                loading: () => const LoadingState(),
                error: (e, _) => ErrorStateView(
                  message: e is ApiError ? e.message : e.toString(),
                  onRetry: () => ref.invalidate(
                    certificateDetailProvider(_key(clusterId)),
                  ),
                ),
                data: (detail) => TabBarView(
                  children: [
                    _OverviewTab(cert: detail.certificate),
                    _SubResourcesTab(detail: detail),
                    EventsTab(
                      kind: 'Certificate',
                      namespace: widget.namespace,
                      name: widget.name,
                      uid: detail.certificate.uid,
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionButtons extends StatelessWidget {
  const _ActionButtons({
    required this.busy,
    required this.onRenew,
    required this.onReissue,
  });

  final bool busy;
  final VoidCallback onRenew;
  final VoidCallback onReissue;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (busy) {
      return Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12),
        child: SizedBox(
          width: 16,
          height: 16,
          child: CircularProgressIndicator(strokeWidth: 2, color: colors.accent),
        ),
      );
    }
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        TextButton(onPressed: onRenew, child: const Text('Renew')),
        TextButton(
          onPressed: onReissue,
          style: TextButton.styleFrom(foregroundColor: colors.error),
          child: const Text('Re-issue'),
        ),
      ],
    );
  }
}

class _ActionToast extends StatelessWidget {
  const _ActionToast({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      width: double.infinity,
      color: colors.accent.withValues(alpha: 0.12),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        message,
        style: TextStyle(color: colors.accent, fontSize: 12),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Overview tab
// ---------------------------------------------------------------------------

class _OverviewTab extends StatelessWidget {
  const _OverviewTab({required this.cert});

  final Certificate cert;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CertStatusPill(status: cert.status),
              const SizedBox(width: 8),
              ExpiryBadge(
                daysRemaining: cert.daysRemaining,
                warningThresholdDays: cert.warningThresholdDays,
                criticalThresholdDays: cert.criticalThresholdDays,
              ),
            ],
          ),
          const SizedBox(height: 16),
          _DetailsCard(
            title: 'Details',
            rows: [
              _Row('Namespace', cert.namespace),
              _Row('Issuer', '${cert.issuerRef.kind}/${cert.issuerRef.name}'),
              _Row('Secret', cert.secretName),
              if (cert.commonName != null)
                _Row('Common name', cert.commonName!),
              if (cert.dnsNames.isNotEmpty)
                _Row('DNS names', cert.dnsNames.join(', ')),
              _Row('Not before', _fmt(cert.notBefore)),
              _Row('Not after', _fmt(cert.notAfter)),
              _Row('Renewal time', _fmt(cert.renewalTime)),
              if (cert.duration != null) _Row('Duration', cert.duration!),
              if (cert.renewBefore != null)
                _Row('Renew before', cert.renewBefore!),
            ],
          ),
          const SizedBox(height: 12),
          _ThresholdAttribution(cert: cert),
          if (cert.reason != null || cert.message != null) ...[
            const SizedBox(height: 12),
            _DetailsCard(
              title: 'Status detail',
              rows: [
                if (cert.reason != null) _Row('Reason', cert.reason!),
                if (cert.message != null) _Row('Message', cert.message!),
              ],
            ),
          ],
          const SizedBox(height: 24),
          Text(
            'UID: ${cert.uid}',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }

  static String _fmt(String? iso) {
    if (iso == null || iso.isEmpty) return '—';
    // We preserve the wire ISO-8601 format intentionally: the alternate is
    // either using DateTime.parse + the device's locale (which would emit
    // a region-specific string), or going through intl (a heavier
    // dependency). Operators reading the detail screen typically want
    // the raw timestamp anyway.
    return iso;
  }
}

class _ThresholdAttribution extends StatelessWidget {
  const _ThresholdAttribution({required this.cert});

  final Certificate cert;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final warn = cert.warningThresholdDays;
    final crit = cert.criticalThresholdDays;
    if (warn == null && crit == null && !cert.thresholdConflict) {
      // No resolved thresholds and no conflict — nothing useful to show.
      return const SizedBox.shrink();
    }
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'EXPIRY THRESHOLDS',
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
            ),
          ),
          const SizedBox(height: 8),
          _AttributionLine(
            label: 'Warns at',
            days: warn,
            source: cert.warningThresholdSource,
            cert: cert,
          ),
          _AttributionLine(
            label: 'Critical at',
            days: crit,
            source: cert.criticalThresholdSource,
            cert: cert,
          ),
          if (cert.thresholdConflict) ...[
            const SizedBox(height: 8),
            Tooltip(
              message:
                  'Resolved threshold pair would have violated '
                  'critical < warning. Using package defaults until you '
                  'fix one of the annotations.',
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                decoration: BoxDecoration(
                  color: colors.warning.withValues(alpha: 0.16),
                  borderRadius: BorderRadius.circular(6),
                  border:
                      Border.all(color: colors.warning.withValues(alpha: 0.4)),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.warning_amber_outlined,
                        size: 14, color: colors.warning),
                    const SizedBox(width: 6),
                    Text(
                      'Conflict — using defaults',
                      style: TextStyle(
                        color: colors.warning,
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
          const SizedBox(height: 8),
          Text(
            'Resolution chain: certificate annotation → issuer → '
            'clusterissuer → package default. Each key resolves '
            'independently.',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }
}

class _AttributionLine extends StatelessWidget {
  const _AttributionLine({
    required this.label,
    required this.days,
    required this.source,
    required this.cert,
  });

  final String label;
  final int? days;
  final ThresholdSource source;
  final Certificate cert;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final value = days == null ? '—' : '${days}d';
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: RichText(
        text: TextSpan(
          style: TextStyle(color: colors.textPrimary, fontSize: 13),
          children: [
            TextSpan(text: '$label $value '),
            TextSpan(
              text: '(${_sourceLabel(source, cert)})',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

/// Renders the per-key source attribution label — `"From Issuer X"`
/// when the cert inherited the value from its referenced Issuer, etc.
String _sourceLabel(ThresholdSource source, Certificate cert) {
  switch (source) {
    case ThresholdSource.certificate:
      return 'From this certificate';
    case ThresholdSource.issuer:
      return 'From Issuer ${cert.issuerRef.name}';
    case ThresholdSource.clusterIssuer:
      return 'From ClusterIssuer ${cert.issuerRef.name}';
    case ThresholdSource.defaultSource:
    case ThresholdSource.unknown:
      return 'Default';
  }
}

// ---------------------------------------------------------------------------
// Sub-Resources tab
// ---------------------------------------------------------------------------

class _SubResourcesTab extends StatelessWidget {
  const _SubResourcesTab({required this.detail});

  final CertificateDetail detail;

  bool get _rbacHintApplies {
    final s = detail.certificate.status;
    final issuingOrFailing =
        s == CertStatus.issuing || s == CertStatus.failed;
    return issuingOrFailing && detail.certificateRequests.isEmpty;
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (_rbacHintApplies) ...[
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: colors.warning.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                  color: colors.warning.withValues(alpha: 0.4),
                ),
              ),
              child: Row(
                children: [
                  Icon(Icons.info_outline,
                      size: 16, color: colors.warning),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'Some sub-resources may be hidden by RBAC. '
                      'Your role may not include cert-manager '
                      'CertificateRequests in this namespace.',
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontSize: 12,
                      ),
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 12),
          ],
          _SubResourceSection<CertificateRequestRow>(
            title: 'Certificate Requests',
            items: detail.certificateRequests,
            empty: 'No CertificateRequests visible for this certificate.',
            rowBuilder: (row) => _CrRow(row: row),
          ),
          const SizedBox(height: 12),
          _SubResourceSection<OrderRow>(
            title: 'Orders',
            items: detail.orders,
            empty:
                'No ACME Orders. Non-ACME issuers do not emit Orders.',
            rowBuilder: (row) => _OrderRow(row: row),
          ),
          const SizedBox(height: 12),
          _SubResourceSection<ChallengeRow>(
            title: 'Challenges',
            items: detail.challenges,
            empty: 'No ACME Challenges.',
            rowBuilder: (row) => _ChallengeRow(row: row),
          ),
        ],
      ),
    );
  }
}

class _SubResourceSection<T> extends StatelessWidget {
  const _SubResourceSection({
    required this.title,
    required this.items,
    required this.empty,
    required this.rowBuilder,
  });

  final String title;
  final List<T> items;
  final String empty;
  final Widget Function(T row) rowBuilder;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 8),
            child: Row(
              children: [
                Text(
                  title.toUpperCase(),
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    letterSpacing: 0.5,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  '(${items.length})',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ],
            ),
          ),
          if (items.isEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
              child: Text(
                empty,
                style: TextStyle(color: colors.textMuted, fontSize: 12),
              ),
            )
          else
            ...items.map(rowBuilder),
        ],
      ),
    );
  }
}

class _CrRow extends StatelessWidget {
  const _CrRow({required this.row});
  final CertificateRequestRow row;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: colors.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  row.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w500,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              CertStatusPill(status: row.status),
            ],
          ),
          if (row.reason != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                row.reason!,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Text(
              'Created ${row.createdAt}',
              style: TextStyle(color: colors.textMuted, fontSize: 11),
            ),
          ),
        ],
      ),
    );
  }
}

class _OrderRow extends StatelessWidget {
  const _OrderRow({required this.row});
  final OrderRow row;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: colors.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  row.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w500,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                row.state,
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                ),
              ),
            ],
          ),
          if (row.reason != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                row.reason!,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

class _ChallengeRow extends StatelessWidget {
  const _ChallengeRow({required this.row});
  final ChallengeRow row;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: colors.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  row.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w500,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                '${row.type} · ${row.state}',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                ),
              ),
            ],
          ),
          if (row.dnsName != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                row.dnsName!,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
          if (row.reason != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                row.reason!,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

class _Row {
  const _Row(this.label, this.value);
  final String label;
  final String value;
}

class _DetailsCard extends StatelessWidget {
  const _DetailsCard({required this.title, required this.rows});

  final String title;
  final List<_Row> rows;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title.toUpperCase(),
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
            ),
          ),
          const SizedBox(height: 8),
          for (final r in rows)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(
                    width: 110,
                    child: Text(
                      r.label,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ),
                  Expanded(
                    child: SelectableText(
                      r.value,
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontSize: 12,
                      ),
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

