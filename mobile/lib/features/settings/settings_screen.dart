// Settings entry point. Reachable from the navigation drawer.
//
// Three sections in display order:
//   1. Appearance      — opens the existing theme_picker_sheet.
//   2. Crash reporting — Sentry opt-in toggle.
//   3. About           — version + build, Privacy Policy, Rate this app.
//
// No "Security" section in M5 — biometric authentication is not
// implemented (no local_auth dep, no Settings → Security UI). Adding
// the section without the feature would be misleading metadata under
// App Store guideline 2.3.
//
// FCM device-token revoke tile is deferred from PR-5a — see
// docs/OBSERVABILITY.md for the rationale.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../observability/sentry_init.dart' show kSentryDsn;
import '../../theme/kube_theme_builder.dart';
import 'sentry_controller.dart';
import 'theme_picker_sheet.dart';

/// Public so the privacy policy and store-listing URLs can be referenced
/// elsewhere (PR-5g onboarding intro card, PR-5j App Privacy doc).
const String kPrivacyPolicyUrl = 'https://kubecenter.io/privacy';
const String kSupportUrl = 'https://github.com/kubecenter-io/k8scenter/issues';
const String kAppStoreListingUrl =
    'https://apps.apple.com/app/k8scenter/id0000000000';
const String kPlayStoreListingUrl =
    'https://play.google.com/store/apps/details?id=io.kubecenter.app';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final optedIn = ref.watch(sentryControllerProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          _SectionHeader(label: 'Appearance', colors: colors),
          ListTile(
            leading: Icon(Icons.palette_outlined, color: colors.accent),
            title: const Text('Theme'),
            subtitle: const Text('Switch between built-in themes'),
            trailing: Icon(Icons.chevron_right, color: colors.textMuted),
            onTap: () => ThemePickerSheet.show(context),
          ),
          const Divider(height: 1),

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
            trailing: Icon(Icons.open_in_new, color: colors.textMuted, size: 18),
            onTap: () => _launchStoreListing(context),
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
