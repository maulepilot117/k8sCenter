// Log volume histogram. Wraps KubeBarChart over the LogVolumeResult
// from /v1/logs/volume. Hidden when the volume fetch fails or returns
// no buckets — best-effort surface; the operator's primary interest is
// the results list below.

import 'package:flutter/material.dart';

import '../../../api/loki_repository.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/kube_bar_chart.dart';
import '../../../widgets/kube_line_chart.dart' show KubeChartSeverity;

class LogVolumeHistogram extends StatelessWidget {
  const LogVolumeHistogram({
    required this.result,
    super.key,
  });

  final LogVolumeResult result;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (result.isEmpty) return const SizedBox.shrink();

    final points = result.buckets
        .map((b) => (t: b.timestamp, v: b.count.toDouble()))
        .toList();
    final series = (
      label: 'lines',
      points: points,
      severity: KubeChartSeverity.info,
    );

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
          Row(
            children: [
              Text(
                'Volume',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
              const Spacer(),
              Text(
                '${result.total.toString()} lines',
                style: TextStyle(color: colors.textMuted, fontSize: 12),
              ),
            ],
          ),
          const SizedBox(height: 8),
          KubeBarChart(series: series, height: 80),
        ],
      ),
    );
  }
}
