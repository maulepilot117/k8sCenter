// Onboarding card widget with three static factory methods, one per page of
// the tour.
//
//   OnboardingCard.intro(...)          — public-store strangers: "you need a
//                                        self-hosted backend"; primary CTA
//                                        advances, secondary CTA opens the
//                                        install guide in the system browser.
//   OnboardingCard.clusterPin(...)     — orients new users on the cluster pill.
//   OnboardingCard.notifications(...) — fires the OS push-permission prompt via
//                                        [requestFcmPermission], then advances.
//                                        Primary CTA label is "Get started" per
//                                        the plan spec.
//
// Cards are pure widgets; they expose `onAdvance` and `onSkip` callbacks so
// the screen owns the PageView / completion state and the cards stay testable
// in isolation.
//
// Callback typing: `FutureOr<void> Function()` so async screen callbacks
// (`_advance`, `_complete`) are accepted without a wrapper. Any exception
// thrown by the callback is caught at the card boundary and forwarded to
// [FlutterError.reportError] so it surfaces in crash reports without
// trapping the user on the current page.

import 'dart:async' show FutureOr;

import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../theme/kube_theme_builder.dart';
import '../settings/settings_screen.dart' show kInstallGuideUrl;
import '../../notifications/fcm_registration_helpers.dart'
    show requestFcmPermission;

/// Namespace for the three onboarding card factory methods.  Each factory
/// returns the correctly-configured [Widget] for its page of the tour.
///
/// ```dart
/// OnboardingCard.intro(onAdvance: _advance, onSkip: _complete)
/// OnboardingCard.clusterPin(onAdvance: _advance, onSkip: _complete)
/// OnboardingCard.notifications(onAdvance: _advance, onSkip: _complete)
/// ```
abstract final class OnboardingCard {
  // ── factory: intro ──────────────────────────────────────────────────────────

  static Widget intro({
    Key? key,
    required FutureOr<void> Function() onAdvance,
    required FutureOr<void> Function() onSkip,
  }) =>
      _IntroCard(key: key, onAdvance: onAdvance, onSkip: onSkip);

  // ── factory: clusterPin ─────────────────────────────────────────────────────

  static Widget clusterPin({
    Key? key,
    required FutureOr<void> Function() onAdvance,
    required FutureOr<void> Function() onSkip,
  }) =>
      _SimpleCard(
        key: key,
        icon: Icons.hub_outlined,
        iconSemanticsLabel: 'Cluster icon',
        headline: 'Pick your cluster',
        body: 'The cluster pill at the top of every screen switches between '
            'connected clusters. Tap it any time to add another or to view '
            'a different one.',
        primaryKey: const ValueKey('onboarding-cluster-next'),
        primaryLabel: 'Next',
        onPrimaryPressed: onAdvance,
        onSkip: onSkip,
      );

  // ── factory: notifications ───────────────────────────────────────────────────

  static Widget notifications({
    Key? key,
    required FutureOr<void> Function() onAdvance,
    required FutureOr<void> Function() onSkip,
  }) =>
      _SimpleCard(
        key: key,
        icon: Icons.notifications_outlined,
        iconSemanticsLabel: 'Notifications bell icon',
        headline: 'Stay on top of alerts',
        body: 'Push notifications let k8sCenter wake your phone when a '
            'cluster needs attention. Tap "Get started" to allow notifications '
            '— you can change this later in Settings.',
        primaryKey: const ValueKey('onboarding-notifications-get-started'),
        primaryLabel: 'Get started',
        onPrimaryPressed: () async {
          // requestFcmPermission is defined in fcm_registration_helpers.dart.
          // It swallows all errors so a missing Firebase config in CI never
          // traps the user.
          await requestFcmPermission();
          await onAdvance();
        },
        onSkip: onSkip,
      );
}

// ── _SimpleCard ────────────────────────────────────────────────────────────────
//
// Handles the two cards (ClusterPin, Notifications) that have no secondary CTA.

class _SimpleCard extends StatelessWidget {
  const _SimpleCard({
    super.key,
    required this.icon,
    required this.iconSemanticsLabel,
    required this.headline,
    required this.body,
    required this.primaryKey,
    required this.primaryLabel,
    required this.onPrimaryPressed,
    required this.onSkip,
  });

  final IconData icon;
  final String iconSemanticsLabel;
  final String headline;
  final String body;
  final Key primaryKey;
  final String primaryLabel;
  final FutureOr<void> Function() onPrimaryPressed;
  final FutureOr<void> Function() onSkip;

  @override
  Widget build(BuildContext context) {
    return _OnboardingCardLayout(
      icon: icon,
      iconSemanticsLabel: iconSemanticsLabel,
      headline: headline,
      body: body,
      primary: _PrimaryButton(
        key: primaryKey,
        label: primaryLabel,
        onPressed: onPrimaryPressed,
      ),
      onSkip: onSkip,
    );
  }
}

// ── _IntroCard ─────────────────────────────────────────────────────────────────
//
// Intro is the only card with a secondary "install guide" CTA and the
// associated SnackBar fallback. It keeps its own widget class so the
// `_launchInstallGuide` helper can capture BuildContext without it being passed
// as a constructor parameter.

class _IntroCard extends StatelessWidget {
  const _IntroCard({
    super.key,
    required this.onAdvance,
    required this.onSkip,
  });

  final FutureOr<void> Function() onAdvance;
  final FutureOr<void> Function() onSkip;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return _OnboardingCardLayout(
      icon: Icons.dns_outlined,
      iconSemanticsLabel: 'Self-hosted server icon',
      headline: 'k8sCenter Mobile needs a home',
      body: 'This app is an oncall companion for an existing self-hosted '
          'k8sCenter backend. If you do not have one set up yet, you can '
          'install one first and come back.',
      primary: _PrimaryButton(
        key: const ValueKey('onboarding-intro-have-server'),
        label: 'I have a server',
        onPressed: onAdvance,
      ),
      secondary: _GhostButton(
        key: const ValueKey('onboarding-intro-install-guide'),
        label: 'How to set up your server',
        colors: colors,
        onPressed: () => _launchInstallGuide(context),
      ),
      onSkip: onSkip,
    );
  }

  Future<void> _launchInstallGuide(BuildContext context) async {
    final uri = Uri.parse(kInstallGuideUrl);
    final ok = await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!ok && context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not open $kInstallGuideUrl')),
      );
    }
  }
}

/// Shared visual scaffold for every card. Top-right Skip, centered
/// icon, headline + body, primary CTA at the bottom, optional ghost
/// secondary below it.
///
/// Private: external code (including tests) pumps [OnboardingCard] factory
/// instances rather than this layout directly.
class _OnboardingCardLayout extends StatelessWidget {
  const _OnboardingCardLayout({
    required this.icon,
    required this.iconSemanticsLabel,
    required this.headline,
    required this.body,
    required this.primary,
    this.secondary,
    required this.onSkip,
  });

  final IconData icon;
  final String iconSemanticsLabel;
  final String headline;
  final String body;
  final Widget primary;
  final Widget? secondary;
  final FutureOr<void> Function() onSkip;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Align(
              alignment: Alignment.topRight,
              child: TextButton(
                key: const ValueKey('onboarding-skip'),
                onPressed: () async {
                  try {
                    await onSkip();
                  } catch (e, s) {
                    FlutterError.reportError(FlutterErrorDetails(
                      exception: e,
                      stack: s,
                      library: 'onboarding',
                    ));
                  }
                },
                child: Text(
                  'Skip',
                  style: TextStyle(color: colors.textMuted),
                ),
              ),
            ),
            Expanded(
              flex: 4,
              child: Center(
                child: Icon(
                  icon,
                  size: 120,
                  color: colors.accent,
                  semanticLabel: iconSemanticsLabel,
                ),
              ),
            ),
            Expanded(
              flex: 6,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    headline,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 24,
                      fontWeight: FontWeight.w600,
                      height: 1.2,
                    ),
                  ),
                  const SizedBox(height: 12),
                  Text(
                    body,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 16,
                      height: 1.4,
                    ),
                  ),
                  const Spacer(),
                  primary,
                  if (secondary != null) ...[
                    const SizedBox(height: 8),
                    secondary!,
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PrimaryButton extends StatelessWidget {
  const _PrimaryButton({
    super.key,
    required this.label,
    required this.onPressed,
  });

  final String label;
  final FutureOr<void> Function() onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 48,
      child: FilledButton(
        onPressed: () async {
          try {
            await onPressed();
          } catch (e, s) {
            FlutterError.reportError(FlutterErrorDetails(
              exception: e,
              stack: s,
              library: 'onboarding',
            ));
          }
        },
        child: Text(label),
      ),
    );
  }
}

class _GhostButton extends StatelessWidget {
  const _GhostButton({
    super.key,
    required this.label,
    required this.onPressed,
    required this.colors,
  });

  final String label;
  final VoidCallback onPressed;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 48,
      child: TextButton(
        onPressed: onPressed,
        child: Text(label, style: TextStyle(color: colors.accent)),
      ),
    );
  }
}
