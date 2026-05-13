// Per-store metrics panel. Reads the live `/v1/externalsecrets/
// {cluster,}stores/.../metrics` response shape and decides chart-vs-
// table per field (per plan §"Deferred to Implementation" — the
// response shape is determined at runtime, not hardcoded here).
//
// Wire contract: `ratePerMin` + `last24h` are null when Prometheus has
// no series yet OR is offline (distinguished by `error`). The UI MUST
// NOT fabricate a zero — `null` ≠ `0`. Cost block is null for
// self-hosted providers (Vault, Kubernetes); the panel suppresses the
// card rather than showing "$0".

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'eso_widgets.dart';

/// Renders rate (chart-suitable when paired with a time series, KV
/// otherwise — the current backend emits an instant rate so KV it is)
/// and cost-tier estimate. Distinguishes the three null cases:
///   * Prom offline (error string)
///   * No series yet (null rate, no error)
///   * Self-hosted provider (no cost block at all)
class StoreMetricsPanel extends ConsumerWidget {
  const StoreMetricsPanel({
    super.key,
    required this.metricsAsync,
    required this.onRetry,
    this.storeLabel,
  });

  /// Pass the `ref.watch(...)` result from one of:
  ///   * [storeMetricsProvider]
  ///   * [clusterStoreMetricsProvider]
  final AsyncValue<StoreMetrics> metricsAsync;

  /// Pull-to-refresh / inline retry callback the panel exposes when the
  /// fetch failed. Distinct from the `error` field on the 200 envelope —
  /// THIS handles network/auth failures.
  final VoidCallback onRetry;

  /// Optional caption rendered above the panel ("vault-store / app").
  /// Used by the embedded sub-tab placement on store detail screens.
  final String? storeLabel;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    return metricsAsync.when(
      loading: () => const Padding(
        padding: EdgeInsets.symmetric(vertical: 24),
        child: Center(child: CircularProgressIndicator()),
      ),
      error: (e, _) => ErrorStateView(
        message: e is ApiError ? e.message : e.toString(),
        onRetry: onRetry,
      ),
      data: (metrics) => _MetricsBody(
        metrics: metrics,
        storeLabel: storeLabel,
        colors: colors,
      ),
    );
  }
}

class _MetricsBody extends StatelessWidget {
  const _MetricsBody({
    required this.metrics,
    required this.storeLabel,
    required this.colors,
  });

  final StoreMetrics metrics;
  final String? storeLabel;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (storeLabel != null) ...[
          Text(
            storeLabel!,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
        ],
        if (metrics.isDegraded)
          _DegradedBanner(message: metrics.error!, colors: colors),
        const SizedBox(height: 8),
        _RatePanel(metrics: metrics, colors: colors),
        if (metrics.cost != null) ...[
          const SizedBox(height: 12),
          _CostPanel(cost: metrics.cost!, colors: colors),
        ],
        if (metrics.windowEnd != null && metrics.windowEnd!.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(
            'Sampled at ${metrics.windowEnd!}',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ],
    );
  }
}

class _DegradedBanner extends StatelessWidget {
  const _DegradedBanner({required this.message, required this.colors});

  final String message;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: colors.warningDim,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: colors.warning),
      ),
      child: Row(
        children: [
          Icon(Icons.warning_amber_outlined,
              size: 16, color: colors.warning),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _RatePanel extends StatelessWidget {
  const _RatePanel({required this.metrics, required this.colors});

  final StoreMetrics metrics;
  final KubeColors colors;

  String _fmtRate(double? v) {
    if (v == null) {
      return metrics.isDegraded ? '—' : 'no data yet';
    }
    if (v < 0.01) return v.toStringAsFixed(4);
    return v.toStringAsFixed(2);
  }

  String _fmtCount(double? v) {
    if (v == null) {
      return metrics.isDegraded ? '—' : 'no data yet';
    }
    if (v < 1) return v.toStringAsFixed(2);
    return v.toStringAsFixed(0);
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgElevated,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Sync rate',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
          EsoKvRow(
            label: 'Rate / min',
            value: _fmtRate(metrics.ratePerMin),
            valueColor: metrics.ratePerMin == null ? colors.textMuted : null,
          ),
          EsoKvRow(
            label: 'Last 24h',
            value: _fmtCount(metrics.last24h),
            valueColor: metrics.last24h == null ? colors.textMuted : null,
          ),
        ],
      ),
    );
  }
}

class _CostPanel extends StatelessWidget {
  const _CostPanel({required this.cost, required this.colors});

  final CostEstimate cost;
  final KubeColors colors;

  String _fmtMoney(double? v, String currency) {
    if (v == null) return '—';
    return '${v.toStringAsFixed(2)} $currency';
  }

  @override
  Widget build(BuildContext context) {
    final cur = cost.currency ?? 'USD';
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgElevated,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                'Cost estimate',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(width: 8),
              ProviderChip(provider: cost.billingProvider),
            ],
          ),
          const SizedBox(height: 6),
          EsoKvRow(
            label: 'USD / 1M',
            value: cost.usdPerMillion == null
                ? '—'
                : cost.usdPerMillion!.toStringAsFixed(2),
          ),
          EsoKvRow(
            label: 'Est. 24h',
            value: _fmtMoney(cost.estimated24h, cur),
          ),
          if (cost.lastUpdated != null)
            EsoKvRow(label: 'Rate card date', value: cost.lastUpdated!),
          const SizedBox(height: 4),
          Text(
            'Not connected to live billing — public list price snapshot.',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }
}
