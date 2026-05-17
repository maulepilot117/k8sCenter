// Bridge from generated KubeThemeColors (string tokens) to Material 3
// ThemeData + a KubeColors ThemeExtension that carries tokens which don't
// fit Material's slot model (accent dim/glow, surface variants, semantic
// dims for success/warning/error). The generator stays Flutter-free so
// `make check-themes` runs without the SDK; this file is the only place
// hex-string parsing happens.

import 'package:flutter/material.dart';

import 'themes.g.dart';

/// Tokens that don't map cleanly onto [ColorScheme]. Carried as a
/// [ThemeExtension] so widgets can read them via
/// `Theme.of(context).extension<KubeColors>()!`.
///
/// ## WCAG 2.2 AA contrast contract
///
/// The contract enforced by `mobile/test/a11y/contrast_test.dart` across
/// all 7 generated themes:
///   - [textPrimary] on [bgBase], [bgSurface], [bgElevated] — ≥4.5:1
///     (Normal text per WCAG 2.2 AA).
///   - [textSecondary] on [bgBase], [bgSurface] — ≥4.5:1 (Normal text).
///
/// Tokens NOT in the contract:
///   - [textMuted] is caption-grade (rendered at fontSize 10–12 in
///     timestamps/bylines); it satisfies WCAG AA Large at 3:1 only.
///   - [accent], [accentSecondary] are typically rendered as button
///     labels on [accentDim] backgrounds (composite contrast — verified
///     visually at design time, not automated).
///   - [success], [warning], [error], [info] are status pill labels
///     rendered on their own `*Dim` companion backgrounds. The composite
///     foreground/background pair determined at runtime, so an
///     automated test against raw [bgSurface] is misleading.
///   - [accentGlow], [borderPrimary], [borderSubtle] are non-text UI
///     components (hairlines, focus rings) — minimum 3:1 by WCAG 1.4.11.
@immutable
class KubeColors extends ThemeExtension<KubeColors> {
  const KubeColors({
    required this.bgBase,
    required this.bgSurface,
    required this.bgElevated,
    required this.bgHover,
    required this.borderPrimary,
    required this.borderSubtle,
    required this.textPrimary,
    required this.textSecondary,
    required this.textMuted,
    required this.accent,
    required this.accentGlow,
    required this.accentDim,
    required this.accentSecondary,
    required this.success,
    required this.successDim,
    required this.warning,
    required this.warningDim,
    required this.error,
    required this.errorDim,
    required this.info,
  });

  /// Canvas background. Scaffold body, app root.
  final Color bgBase;

  /// Card / surface tier. Tab bodies, list rows, dialog backgrounds.
  final Color bgSurface;

  /// Elevated tier. Bottom sheets, drawer, popovers.
  final Color bgElevated;

  /// Hover/pressed state overlay for surface-tier interactive elements.
  final Color bgHover;

  /// Primary divider / outline. Visible at small sizes; 3:1 minimum
  /// against the adjacent surface per WCAG 1.4.11 (non-text contrast).
  final Color borderPrimary;

  /// Subtle separator. Decorative only — no contrast guarantee.
  final Color borderSubtle;

  /// Primary content text. ≥4.5:1 on [bgBase], [bgSurface], [bgElevated]
  /// across all 7 themes (enforced by `test/a11y/contrast_test.dart`).
  final Color textPrimary;

  /// Subtitle / secondary content text. ≥4.5:1 on [bgBase] and
  /// [bgSurface] across all 7 themes (enforced by contrast test).
  final Color textSecondary;

  /// Caption-grade tertiary text. Rendered at fontSize 10–12 in
  /// timestamp/byline contexts. AA Large only (3:1) — NOT in the
  /// 4.5:1 contract.
  final Color textMuted;

  /// Primary accent. Typically paired with [bgSurface] (filled button on
  /// surface) or [accentDim] (tonal button). Composite contrast — not
  /// in the automated 4.5:1 contract.
  final Color accent;

  /// Glow halo for focused / pressed interactive elements. Decorative.
  final Color accentGlow;

  /// Dim companion for [accent]. Used as the BACKGROUND of tonal
  /// affordances where [accent] is the foreground.
  final Color accentDim;

  /// Secondary accent. Used for the "Or" divider tint, secondary chips.
  final Color accentSecondary;

  /// Success state colour. Used as both icon fill (3:1 non-text) and as
  /// pill text on [successDim] background — composite ≥4.5:1 by design,
  /// verified visually rather than via automated test.
  final Color success;

  /// Dim companion for [success]. Used as pill background.
  final Color successDim;

  /// Warning state. Same composite policy as [success].
  final Color warning;

  /// Dim companion for [warning].
  final Color warningDim;

  /// Error state. Same composite policy as [success].
  final Color error;

  /// Dim companion for [error].
  final Color errorDim;

  /// Info state. Same composite policy as [success].
  final Color info;

  @override
  KubeColors copyWith({
    Color? bgBase,
    Color? bgSurface,
    Color? bgElevated,
    Color? bgHover,
    Color? borderPrimary,
    Color? borderSubtle,
    Color? textPrimary,
    Color? textSecondary,
    Color? textMuted,
    Color? accent,
    Color? accentGlow,
    Color? accentDim,
    Color? accentSecondary,
    Color? success,
    Color? successDim,
    Color? warning,
    Color? warningDim,
    Color? error,
    Color? errorDim,
    Color? info,
  }) {
    return KubeColors(
      bgBase: bgBase ?? this.bgBase,
      bgSurface: bgSurface ?? this.bgSurface,
      bgElevated: bgElevated ?? this.bgElevated,
      bgHover: bgHover ?? this.bgHover,
      borderPrimary: borderPrimary ?? this.borderPrimary,
      borderSubtle: borderSubtle ?? this.borderSubtle,
      textPrimary: textPrimary ?? this.textPrimary,
      textSecondary: textSecondary ?? this.textSecondary,
      textMuted: textMuted ?? this.textMuted,
      accent: accent ?? this.accent,
      accentGlow: accentGlow ?? this.accentGlow,
      accentDim: accentDim ?? this.accentDim,
      accentSecondary: accentSecondary ?? this.accentSecondary,
      success: success ?? this.success,
      successDim: successDim ?? this.successDim,
      warning: warning ?? this.warning,
      warningDim: warningDim ?? this.warningDim,
      error: error ?? this.error,
      errorDim: errorDim ?? this.errorDim,
      info: info ?? this.info,
    );
  }

  @override
  KubeColors lerp(ThemeExtension<KubeColors>? other, double t) {
    if (other is! KubeColors) return this;
    return KubeColors(
      bgBase: Color.lerp(bgBase, other.bgBase, t) ?? bgBase,
      bgSurface: Color.lerp(bgSurface, other.bgSurface, t) ?? bgSurface,
      bgElevated: Color.lerp(bgElevated, other.bgElevated, t) ?? bgElevated,
      bgHover: Color.lerp(bgHover, other.bgHover, t) ?? bgHover,
      borderPrimary:
          Color.lerp(borderPrimary, other.borderPrimary, t) ?? borderPrimary,
      borderSubtle:
          Color.lerp(borderSubtle, other.borderSubtle, t) ?? borderSubtle,
      textPrimary: Color.lerp(textPrimary, other.textPrimary, t) ?? textPrimary,
      textSecondary:
          Color.lerp(textSecondary, other.textSecondary, t) ?? textSecondary,
      textMuted: Color.lerp(textMuted, other.textMuted, t) ?? textMuted,
      accent: Color.lerp(accent, other.accent, t) ?? accent,
      accentGlow: Color.lerp(accentGlow, other.accentGlow, t) ?? accentGlow,
      accentDim: Color.lerp(accentDim, other.accentDim, t) ?? accentDim,
      accentSecondary:
          Color.lerp(accentSecondary, other.accentSecondary, t) ??
              accentSecondary,
      success: Color.lerp(success, other.success, t) ?? success,
      successDim: Color.lerp(successDim, other.successDim, t) ?? successDim,
      warning: Color.lerp(warning, other.warning, t) ?? warning,
      warningDim: Color.lerp(warningDim, other.warningDim, t) ?? warningDim,
      error: Color.lerp(error, other.error, t) ?? error,
      errorDim: Color.lerp(errorDim, other.errorDim, t) ?? errorDim,
      info: Color.lerp(info, other.info, t) ?? info,
    );
  }
}

/// Builds a Material 3 [ThemeData] for the named theme id. Falls back to
/// [defaultThemeId] when [id] is not in [kubeThemes].
ThemeData buildKubeTheme(String id) {
  final theme = kubeThemes[id] ?? kubeThemes[defaultThemeId]!;
  final c = theme.colors;

  final colors = KubeColors(
    bgBase: _parseColor(c.bgBase),
    bgSurface: _parseColor(c.bgSurface),
    bgElevated: _parseColor(c.bgElevated),
    bgHover: _parseColor(c.bgHover),
    borderPrimary: _parseColor(c.borderPrimary),
    borderSubtle: _parseColor(c.borderSubtle),
    textPrimary: _parseColor(c.textPrimary),
    textSecondary: _parseColor(c.textSecondary),
    textMuted: _parseColor(c.textMuted),
    accent: _parseColor(c.accent),
    accentGlow: _parseColor(c.accentGlow),
    accentDim: _parseColor(c.accentDim),
    accentSecondary: _parseColor(c.accentSecondary),
    success: _parseColor(c.success),
    successDim: _parseColor(c.successDim),
    warning: _parseColor(c.warning),
    warningDim: _parseColor(c.warningDim),
    error: _parseColor(c.error),
    errorDim: _parseColor(c.errorDim),
    info: _parseColor(c.info),
  );

  final colorScheme = ColorScheme.dark(
    primary: colors.accent,
    onPrimary: colors.bgBase,
    secondary: colors.accentSecondary,
    onSecondary: colors.bgBase,
    error: colors.error,
    onError: colors.bgBase,
    surface: colors.bgSurface,
    onSurface: colors.textPrimary,
    surfaceContainerHighest: colors.bgElevated,
    outline: colors.borderPrimary,
    outlineVariant: colors.borderSubtle,
  );

  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: colorScheme,
    scaffoldBackgroundColor: colors.bgBase,
    canvasColor: colors.bgBase,
    cardColor: colors.bgSurface,
    dividerColor: colors.borderSubtle,
    appBarTheme: AppBarTheme(
      backgroundColor: colors.bgSurface,
      foregroundColor: colors.textPrimary,
      elevation: 0,
      scrolledUnderElevation: 0,
      surfaceTintColor: Colors.transparent,
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: colors.bgElevated,
      surfaceTintColor: Colors.transparent,
    ),
    drawerTheme: DrawerThemeData(
      backgroundColor: colors.bgSurface,
      surfaceTintColor: Colors.transparent,
    ),
    listTileTheme: ListTileThemeData(
      iconColor: colors.textSecondary,
      textColor: colors.textPrimary,
    ),
    textTheme: Typography.material2021(platform: TargetPlatform.iOS)
        .white
        .apply(
          bodyColor: colors.textPrimary,
          displayColor: colors.textPrimary,
        ),
    extensions: [colors],
  );
}

/// Parses '#RRGGBB' or 'rgba(R, G, B, A)' into a [Color]. The generator
/// emits both shapes (hex for opaque tokens, rgba() for translucent dims).
Color _parseColor(String token) {
  final t = token.trim();
  if (t.startsWith('#')) {
    final hex = t.substring(1);
    if (hex.length == 6) {
      return Color(0xFF000000 | int.parse(hex, radix: 16));
    }
    if (hex.length == 8) {
      return Color(int.parse(hex, radix: 16));
    }
    throw FormatException('invalid hex color: $token');
  }
  if (t.startsWith('rgba(')) {
    final inside = t.substring(5, t.length - 1);
    final parts = inside.split(',').map((p) => p.trim()).toList();
    if (parts.length != 4) {
      throw FormatException('invalid rgba(): $token');
    }
    final r = int.parse(parts[0]);
    final g = int.parse(parts[1]);
    final b = int.parse(parts[2]);
    final a = (double.parse(parts[3]) * 255).round().clamp(0, 255);
    return Color.fromARGB(a, r, g, b);
  }
  throw FormatException('unrecognised color token: $token');
}
