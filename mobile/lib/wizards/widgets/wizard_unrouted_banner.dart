// Banner for server validation errors whose field path didn't match
// the per-wizard `errorRouter`. Without this, unmapped errors would
// silently merge into step 0 with no inline visibility — the operator
// rewinds, sees no message under any field, and has no idea what
// failed.
//
// Wizards typically render this at the top of the Configure step,
// reading from `state.unrouted` populated by [WizardController].

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class WizardUnroutedBanner extends StatelessWidget {
  const WizardUnroutedBanner({super.key, required this.unrouted});

  final Map<String, String> unrouted;

  @override
  Widget build(BuildContext context) {
    if (unrouted.isEmpty) return const SizedBox.shrink();
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.error.withValues(alpha: 0.08),
        border: Border.all(color: colors.error.withValues(alpha: 0.4)),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            Icon(Icons.warning_amber_outlined,
                color: colors.error, size: 18),
            const SizedBox(width: 8),
            Text(
              'Server returned errors',
              style: TextStyle(
                color: colors.textPrimary,
                fontWeight: FontWeight.w600,
                fontSize: 13,
              ),
            ),
          ]),
          const SizedBox(height: 6),
          for (final entry in unrouted.entries)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                '${entry.key}: ${entry.value}',
                style: TextStyle(color: colors.textSecondary, fontSize: 12),
              ),
            ),
        ],
      ),
    );
  }
}
