// Shared section header used across wizard Configure steps. Was
// originally duplicated as a private `_SectionHeader` class in five
// wizard screens (service, networkpolicy, ingress, hpa,
// namespace_limits) plus inline ad-hoc shapes in three others (pdb,
// rolebinding, storageclass) — all byte-equivalent. Promoted in M3
// PR-3c review cleanup.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class WizardSectionHeader extends StatelessWidget {
  const WizardSectionHeader(this.title, {super.key, this.subtitle});

  final String title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        if (subtitle != null)
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Text(
              subtitle!,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
      ],
    );
  }
}
