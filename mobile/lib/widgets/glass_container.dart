// Liquid-glass surface primitive: ClipRRect + BackdropFilter + translucent
// fill + 1px glass border. CHROME-ONLY — sheets, dialogs, pickers, floating
// overlays. Never wrap scrolling list items: BackdropFilter cost scales
// with covered pixels and stacks per-item.
//
// Honors the platform "reduce transparency" / high-contrast setting by
// dropping the blur and falling back to the solid elevated surface, the
// same fallback the web frontend applies via prefers-reduced-transparency.

import 'dart:ui';

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

class GlassContainer extends StatelessWidget {
  const GlassContainer({
    super.key,
    required this.child,
    this.elevated = true,
    this.borderRadius = const BorderRadius.all(Radius.circular(20)),
    this.padding,
    this.blurSigma = 20,
  });

  final Widget child;

  /// Floating sheets/dialogs use the stronger [KubeColors.glassElevated]
  /// fill; inline chrome can opt down to [KubeColors.glassSurface].
  final bool elevated;

  final BorderRadius borderRadius;
  final EdgeInsetsGeometry? padding;
  final double blurSigma;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final solid = MediaQuery.highContrastOf(context);

    final content = Container(
      padding: padding,
      decoration: BoxDecoration(
        color: solid
            ? colors.bgElevated
            : (elevated ? colors.glassElevated : colors.glassSurface),
        borderRadius: borderRadius,
        border: Border.all(color: colors.glassBorder),
      ),
      child: child,
    );

    if (solid) return content;

    return ClipRRect(
      borderRadius: borderRadius,
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: blurSigma, sigmaY: blurSigma),
        child: content,
      ),
    );
  }
}
