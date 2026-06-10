// Settings entry point. Reachable from the navigation drawer.
//
// Two sections in display order:
//   1. Crash reporting — Sentry opt-in toggle.
//   2. About           — version + build, Privacy Policy, Rate this app.
//
// No "Appearance" section since the Liquid Glass redesign — the app ships
// a single design language, so there is no theme to pick.
//
// No "Security" section in M5 — biometric authentication is not
// implemented (no local_auth dep, no Settings → Security UI). Adding
// the section without the feature would be misleading metadata under
// App Store guideline 2.3.
//
// FCM device-token revoke happens automatically on logout (Phase 5
// audit finding P2-9, wired in [AuthRepository.logout]). No dedicated
// settings tile — silent revoke matches the "user signed out, push
// stops" mental model.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../observability/sentry_init.dart' show kSentryDsn;
import '../../theme/kube_theme_builder.dart';
import 'sentry_controller.dart';

/// Public so these URLs can be referenced elsewhere (PR-5g onboarding
/// cards, PR-5j App Privacy doc). All external URL constants live here so
/// there is a single place to update when domains change.
const String kInstallGuideUrl = 'https://kubecenter.io/install';
const String kPrivacyPolicyUrl = 'https://kubecenter.io/privacy';
const String kSupportUrl = 'https://github.com/maulepilot117/k8sCenter/issues';

/// Marker in [kAppStoreListingUrl] that means "Apple hasn't assigned a
/// numeric App Store ID yet". The Rate-this-app tile gates on its
/// absence — see [appStoreListingConfigured] and PR-5j issue #272. Swap
/// to the real ID at PR-5j App Store Connect record creation.
const String _kAppStorePlaceholderMarker = 'id0000000000';
const String kAppStoreListingUrl =
    'https://apps.apple.com/app/k8scenter/$_kAppStorePlaceholderMarker';
const String kPlayStoreListingUrl =
    'https://play.google.com/store/apps/details?id=io.kubecenter.kubecenter';

/// True only when [kAppStoreListingUrl] points at a real Apple-assigned
/// listing. iOS "Rate this app" tile disables itself when this is false —
/// without the guard, tapping it on a pre-launch build opens an invalid
/// store URL. Play Store URL is stable by package name so has no
/// equivalent guard. Issue #272.
bool get appStoreListingConfigured =>
    !kAppStoreListingUrl.contains(_kAppStorePlaceholderMarker);

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final optedIn = ref.watch(sentryControllerProvider);
    // Issue #272 — gate iOS "Rate this app" until Apple assigns the
    // real App Store ID. Play Store URL is stable by package name.
    final isIOS = Theme.of(context).platform == TargetPlatform.iOS;
    final rateEnabled = !isIOS || appStoreListingConfigured;

    return Scaffold(
      appBar: AppBar(
        // Explicit back affordance. The drawer reaches this screen via
        // `context.push`, so the default pop returns to the originating
        // page. When there is nothing to pop — a deep link or cold start
        // straight to /settings — fall back to the dashboard so the user
        // is never stranded with no way back (the AppBar's automatic
        // leading only appears when the route can be popped).
        leading: BackButton(
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        title: const Text('Settings'),
      ),
      body: ListView(
        children: [
          _SectionHeader(label: 'Crash reporting', colors: colors),
          SwitchListTile(
            key: const ValueKey('settings-sentry-opt-in'),
            secondary: Icon(Icons.bug_report_outlined, color: colors.accent),
            title: const Text('Send crash reports'),
            subtitle: Text(
              kSentryDsn.isEmpty
                  ? 'Crash reporting is not configured in this build.'
                  : 'Send anonymous crash reports to help us fix bugs. '
                      'Resource names, namespaces, and credentials are stripped '
                      'before reports leave the device.',
            ),
            value: optedIn,
            // Disable the switch entirely when no DSN was wired into the
            // build — otherwise toggling silently does nothing and the
            // operator thinks crash reporting is on.
            onChanged: kSentryDsn.isEmpty
                ? null
                : (v) =>
                    ref.read(sentryControllerProvider.notifier).setOptIn(v),
          ),
          const Divider(height: 1),

          _SectionHeader(label: 'About', colors: colors),
          const _VersionTile(),
          ListTile(
            leading: Icon(Icons.privacy_tip_outlined, color: colors.accent),
            title: const Text('Privacy policy'),
            trailing: Icon(Icons.open_in_new, color: colors.textMuted, size: 18),
            onTap: () => _launchExternal(context, kPrivacyPolicyUrl),
          ),
          ListTile(
            leading: Icon(Icons.help_outline, color: colors.accent),
            title: const Text('Support'),
            trailing: Icon(Icons.open_in_new, color: colors.textMuted, size: 18),
            onTap: () => _launchExternal(context, kSupportUrl),
          ),
          ListTile(
            leading: Icon(Icons.star_outline, color: colors.accent),
            title: const Text('Rate this app'),
            subtitle: rateEnabled
                ? null
                : const Text('Available after public-store launch'),
            trailing: rateEnabled
                ? Icon(Icons.open_in_new, color: colors.textMuted, size: 18)
                : null,
            enabled: rateEnabled,
            onTap: rateEnabled ? () => _launchStoreListing(context) : null,
          ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.label, required this.colors});
  final String label;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 24, 16, 8),
      child: Text(
        label.toUpperCase(),
        style: TextStyle(
          color: colors.textMuted,
          fontSize: 11,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

/// Reads PackageInfo asynchronously. Shows a placeholder while loading
/// so the row layout doesn't jump.
class _VersionTile extends StatefulWidget {
  const _VersionTile();

  @override
  State<_VersionTile> createState() => _VersionTileState();
}

class _VersionTileState extends State<_VersionTile> {
  PackageInfo? _info;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final info = await PackageInfo.fromPlatform();
      if (mounted) setState(() => _info = info);
    } catch (error) {
      // PackageInfo plugin not registered (e.g., golden-test
      // environments). Leave the placeholder in place. debugPrint so
      // operators see something in `flutter logs` if it fails on a real
      // device — silent failure here is too easy to miss.
      debugPrint('PackageInfo.fromPlatform failed: $error');
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final info = _info;
    final value = info == null ? '—' : '${info.version} (${info.buildNumber})';
    return ListTile(
      leading: Icon(Icons.info_outline, color: colors.accent),
      title: const Text('Version'),
      subtitle: Text(value, style: TextStyle(color: colors.textSecondary)),
    );
  }
}

Future<void> _launchExternal(BuildContext context, String url) async {
  final uri = Uri.parse(url);
  final ok = await launchUrl(uri, mode: LaunchMode.externalApplication);
  if (!ok && context.mounted) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Could not open $url')),
    );
  }
}

Future<void> _launchStoreListing(BuildContext context) async {
  // We pick the right listing URL per platform at launch time. On
  // desktop / web (currently unsupported but inevitable for goldens),
  // fall through to the Play Store URL — the OS routes to a browser.
  final theme = Theme.of(context);
  final platform = theme.platform;
  final url = platform == TargetPlatform.iOS
      ? kAppStoreListingUrl
      : kPlayStoreListingUrl;
  await _launchExternal(context, url);
}
