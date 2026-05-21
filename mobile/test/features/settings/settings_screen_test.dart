// Settings screen UI contract:
//   - section ordering (Appearance → Crash reporting → About)
//   - Sentry switch reflects controller state
//   - external-link tiles render and route through url_launcher
//   - launchUrl failure surfaces a snackbar
//   - When DSN is unset (the default in tests), the Sentry switch is
//     disabled with an explanatory subtitle. This guards the "ghost
//     opt-in" hazard where flipping the switch silently does nothing
//     because no DSN was wired into the build.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/settings/settings_screen.dart';
import 'package:kubecenter/observability/sentry_init.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher_platform_interface/link.dart';
import 'package:url_launcher_platform_interface/url_launcher_platform_interface.dart';

/// Stub UrlLauncherPlatform so the launch path doesn't try to hit a real
/// device channel. `MockPlatformInterfaceMixin` opts out of the token
/// check, which is the canonical way to register a fake against a
/// `PlatformInterface`-based plugin in tests.
class _StubLauncher extends UrlLauncherPlatform
    with MockPlatformInterfaceMixin {
  _StubLauncher({this.launchResult = true});
  final bool launchResult;
  final List<String> launched = [];

  @override
  LinkDelegate? get linkDelegate => null;

  @override
  Future<bool> canLaunch(String url) async => true;

  @override
  Future<void> closeWebView() async {}

  @override
  Future<bool> launch(
    String url, {
    required bool useSafariVC,
    required bool useWebView,
    required bool enableJavaScript,
    required bool enableDomStorage,
    required bool universalLinksOnly,
    required Map<String, String> headers,
    String? webOnlyWindowName,
  }) async {
    launched.add(url);
    return launchResult;
  }
}

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  Future<void> pumpScreen(
    WidgetTester tester,
    SharedPreferences prefs, {
    UrlLauncherPlatform? launcher,
    TargetPlatform? platform,
  }) async {
    if (launcher != null) {
      UrlLauncherPlatform.instance = launcher;
    }
    final base = buildKubeTheme('nexus');
    final theme = platform != null ? base.copyWith(platform: platform) : base;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          sharedPreferencesProvider.overrideWithValue(prefs),
        ],
        child: MaterialApp(
          theme: theme,
          home: const SettingsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('renders three sections in display order', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await pumpScreen(tester, prefs);

    final appearance = find.text('APPEARANCE');
    final crash = find.text('CRASH REPORTING');
    final about = find.text('ABOUT');
    expect(appearance, findsOneWidget);
    expect(crash, findsOneWidget);
    expect(about, findsOneWidget);

    final appearanceRect = tester.getRect(appearance);
    final crashRect = tester.getRect(crash);
    final aboutRect = tester.getRect(about);
    expect(appearanceRect.top, lessThan(crashRect.top));
    expect(crashRect.top, lessThan(aboutRect.top));
  });

  testWidgets('SwitchListTile reflects sentryControllerProvider state',
      (tester) async {
    SharedPreferences.setMockInitialValues({kSentryOptInPrefsKey: true});
    final prefs = await SharedPreferences.getInstance();
    await pumpScreen(tester, prefs);

    final tile = tester.widget<SwitchListTile>(
      find.byKey(const ValueKey('settings-sentry-opt-in')),
    );
    expect(tile.value, isTrue);
  });

  testWidgets(
      'switch is disabled with explanatory subtitle when DSN is empty',
      (tester) async {
    // In tests kSentryDsn resolves to '' (no --dart-define wired). The
    // switch should be inert and the subtitle should say so, otherwise
    // operators silently flip a no-op toggle.
    final prefs = await SharedPreferences.getInstance();
    await pumpScreen(tester, prefs);

    expect(kSentryDsn, isEmpty);
    final tile = tester.widget<SwitchListTile>(
      find.byKey(const ValueKey('settings-sentry-opt-in')),
    );
    expect(tile.onChanged, isNull, reason: 'switch must be disabled');
    expect(
      find.textContaining('not configured in this build'),
      findsOneWidget,
    );
  });

  testWidgets('Privacy policy / Support / Rate tiles render',
      (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await pumpScreen(tester, prefs);

    expect(find.text('Privacy policy'), findsOneWidget);
    expect(find.text('Support'), findsOneWidget);
    expect(find.text('Rate this app'), findsOneWidget);
  });

  testWidgets('tapping Privacy policy launches the privacy URL',
      (tester) async {
    final prefs = await SharedPreferences.getInstance();
    final launcher = _StubLauncher();
    await pumpScreen(tester, prefs, launcher: launcher);

    await tester.tap(find.text('Privacy policy'));
    await tester.pump();

    expect(launcher.launched, contains(kPrivacyPolicyUrl));
  });

  testWidgets('launchUrl failure surfaces a snackbar', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    final launcher = _StubLauncher(launchResult: false);
    await pumpScreen(tester, prefs, launcher: launcher);

    await tester.tap(find.text('Support'));
    await tester.pump();
    // Snackbar animations need time to settle.
    await tester.pump(const Duration(milliseconds: 250));

    expect(find.textContaining('Could not open'), findsOneWidget);
  });

  // Issue #272 — Rate-this-app tile gates on App Store ID being assigned.
  // Without the guard, tapping the tile on iOS pre-launch opens an invalid
  // App Store URL containing the literal placeholder id0000000000.
  testWidgets('iOS Rate-this-app tile disables with placeholder App Store ID',
      (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await pumpScreen(tester, prefs, platform: TargetPlatform.iOS);

    expect(
      appStoreListingConfigured,
      isFalse,
      reason: 'placeholder id0000000000 should not count as configured',
    );

    expect(
      find.text('Available after public-store launch'),
      findsOneWidget,
    );
    final tile = tester.widget<ListTile>(
      find.ancestor(
        of: find.text('Rate this app'),
        matching: find.byType(ListTile),
      ),
    );
    expect(tile.enabled, isFalse);
    expect(tile.onTap, isNull);
  });

  testWidgets(
      'Android Rate-this-app tile launches the Play URL '
      '(real package id, issue #272)', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    final launcher = _StubLauncher();
    await pumpScreen(
      tester,
      prefs,
      launcher: launcher,
      platform: TargetPlatform.android,
    );

    expect(
      find.text('Available after public-store launch'),
      findsNothing,
      reason: 'Play URL is stable by package id — no placeholder',
    );

    await tester.tap(find.text('Rate this app'));
    await tester.pump();

    expect(launcher.launched.single, kPlayStoreListingUrl);
    expect(
      kPlayStoreListingUrl,
      contains('io.kubecenter.kubecenter'),
      reason:
          'Play URL must use the actual package id (issue #272 regression)',
    );
  });
}
