// Three onboarding card widgets, one per page of the tour.
//
//   IntroCard         — public-store strangers: "you need a self-hosted
//                       backend"; primary CTA advances, secondary CTA
//                       opens the install guide in the system browser.
//   ClusterPinCard    — orients new users on the cluster pill.
//   NotificationsCard — fires the OS push-permission prompt, advances
//                       regardless of the user's choice.
//
// Cards are pure widgets; they expose `onAdvance` and `onSkip`
// callbacks so the screen owns the PageView / completion state and the
// cards stay testable in isolation.

import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart' show debugPrint, kIsWeb;
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../theme/kube_theme_builder.dart';

/// Public-marketing landing page for users who installed the app
/// without a server to point it at. PR-5j may move this to a versioned
/// URL; for M5 the path is stable.
const String kInstallGuideUrl = 'https://kubecenter.io/install';

class IntroCard extends StatelessWidget {
  const IntroCard({
    super.key,
    required this.onAdvance,
    required this.onSkip,
  });

  final VoidCallback onAdvance;
  final VoidCallback onSkip;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return OnboardingCardLayout(
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

class ClusterPinCard extends StatelessWidget {
  const ClusterPinCard({
    super.key,
    required this.onAdvance,
    required this.onSkip,
  });

  final VoidCallback onAdvance;
  final VoidCallback onSkip;

  @override
  Widget build(BuildContext context) {
    return OnboardingCardLayout(
      icon: Icons.hub_outlined,
      iconSemanticsLabel: 'Cluster icon',
      headline: 'Pick your cluster',
      body: 'The cluster pill at the top of every screen switches between '
          'connected clusters. Tap it any time to add another or to view '
          'a different one.',
      primary: _PrimaryButton(
        key: const ValueKey('onboarding-cluster-next'),
        label: 'Next',
        onPressed: onAdvance,
      ),
      onSkip: onSkip,
    );
  }
}

class NotificationsCard extends StatelessWidget {
  const NotificationsCard({
    super.key,
    required this.onAdvance,
    required this.onSkip,
  });

  final VoidCallback onAdvance;
  final VoidCallback onSkip;

  @override
  Widget build(BuildContext context) {
    return OnboardingCardLayout(
      icon: Icons.notifications_outlined,
      iconSemanticsLabel: 'Notifications bell icon',
      headline: 'Stay on top of alerts',
      body: 'Push notifications let k8sCenter wake your phone when a '
          'cluster needs attention. You can change this later in Settings.',
      primary: _PrimaryButton(
        key: const ValueKey('onboarding-notifications-enable'),
        label: 'Enable notifications',
        onPressed: () async {
          await _requestNotificationPermission();
          onAdvance();
        },
      ),
      onSkip: onSkip,
    );
  }

  /// Surfaces the OS push-permission prompt by initialising Firebase
  /// and calling `requestPermission`. Any failure (missing
  /// google-services.json / GoogleService-Info.plist, web or desktop
  /// build, sandboxed CI) is swallowed — the card still advances so a
  /// user without a configured FCM build isn't trapped on the last
  /// page.
  Future<void> _requestNotificationPermission() async {
    if (kIsWeb) return;
    try {
      if (!Platform.isIOS && !Platform.isAndroid) return;
    } catch (_) {
      return;
    }
    try {
      await Firebase.initializeApp();
      await FirebaseMessaging.instance.requestPermission(
        alert: true,
        badge: true,
        sound: true,
      );
    } catch (e) {
      debugPrint('[onboarding] notifications init skipped: $e');
    }
  }
}

/// Shared visual scaffold for every card. Top-right Skip, centered
/// icon, headline + body, primary CTA at the bottom, optional ghost
/// secondary below it. Exposed (non-private) so widget tests can pump
/// the layout standalone without a Firebase/url_launcher detour.
@visibleForTesting
class OnboardingCardLayout extends StatelessWidget {
  const OnboardingCardLayout({
    super.key,
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
  final VoidCallback onSkip;

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
                onPressed: onSkip,
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
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 48,
      child: FilledButton(
        onPressed: onPressed,
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
