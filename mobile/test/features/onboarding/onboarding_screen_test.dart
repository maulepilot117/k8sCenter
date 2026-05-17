// OnboardingScreen widget contract:
//   - 3 cards in the documented order (Intro → ClusterPin → Notifications).
//   - "Next" advances; the last card's "Get started" completes even if
//     the FCM permission call throws (it does in tests).
//   - "Skip" on any card flips `onboarded_v1` and navigates to /login.
//   - Page indicator semantics announce "Step N of 3".
//
// The screen pulls in `Firebase.initializeApp` and `launchUrl` via the
// notification + intro cards. Both are caught defensively inside the
// card code, so the widget tree never sees the throw — we just assert
// the user-visible outcome.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/features/onboarding/onboarding_controller.dart';
import 'package:kubecenter/features/onboarding/onboarding_screen.dart';
import 'package:kubecenter/providers/shared_preferences_provider.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher_platform_interface/link.dart';
import 'package:url_launcher_platform_interface/url_launcher_platform_interface.dart';

class _StubLauncher extends UrlLauncherPlatform
    with MockPlatformInterfaceMixin {
  _StubLauncher({this.launchResult = true});

  /// Control whether [launch] reports success or failure.
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

class _LoginPlaceholder extends StatelessWidget {
  const _LoginPlaceholder();
  @override
  Widget build(BuildContext context) => const Scaffold(
        body: Center(child: Text('LOGIN_PLACEHOLDER')),
      );
}

GoRouter _routerFor() => GoRouter(
      initialLocation: '/onboarding',
      routes: [
        GoRoute(
          path: '/onboarding',
          builder: (context, state) => const OnboardingScreen(),
        ),
        GoRoute(
          path: '/login',
          builder: (context, state) => const _LoginPlaceholder(),
        ),
      ],
    );

Future<void> _pump(
  WidgetTester tester,
  SharedPreferences prefs, {
  UrlLauncherPlatform? launcher,
}) async {
  // Restore the previous launcher instance after the test to prevent
  // cross-test pollution.
  final prevLauncher = UrlLauncherPlatform.instance;
  addTearDown(() => UrlLauncherPlatform.instance = prevLauncher);

  if (launcher != null) {
    UrlLauncherPlatform.instance = launcher;
  }
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        sharedPreferencesProvider.overrideWithValue(prefs),
      ],
      child: MaterialApp.router(
        theme: buildKubeTheme('nexus'),
        routerConfig: _routerFor(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets('renders intro card first', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    expect(find.text('k8sCenter Mobile needs a home'), findsOneWidget);
    expect(find.byKey(const ValueKey('onboarding-intro-have-server')),
        findsOneWidget);
    expect(find.byKey(const ValueKey('onboarding-skip')), findsOneWidget);
  });

  testWidgets('"I have a server" advances to cluster card', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();

    expect(find.text('Pick your cluster'), findsOneWidget);
  });

  testWidgets('full tour: Intro → Cluster → Notifications → /login',
      (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();
    expect(find.text('Pick your cluster'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('onboarding-cluster-next')));
    await tester.pumpAndSettle();
    expect(find.text('Stay on top of alerts'), findsOneWidget);

    await tester.tap(
        find.byKey(const ValueKey('onboarding-notifications-get-started')));
    await tester.pumpAndSettle();

    expect(find.text('LOGIN_PLACEHOLDER'), findsOneWidget);
    expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
  });

  testWidgets('Skip on card 1 completes and routes to /login', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-skip')));
    await tester.pumpAndSettle();

    expect(find.text('LOGIN_PLACEHOLDER'), findsOneWidget);
    expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
  });

  testWidgets('Skip on card 2 completes and routes to /login', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('onboarding-skip')));
    await tester.pumpAndSettle();

    expect(find.text('LOGIN_PLACEHOLDER'), findsOneWidget);
    expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
  });

  testWidgets('Skip on card 3 completes and routes to /login', (tester) async {
    // Navigate to card 3, then verify Skip works from there.
    // PageView keeps offscreen pages in the tree; find.byKey finds the
    // Skip button in the active card because they all share the same key
    // and tester.tap hits the first-found (rendered) widget.
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('onboarding-cluster-next')));
    await tester.pumpAndSettle();
    expect(find.text('Stay on top of alerts'), findsOneWidget);

    // Tap Skip on card 3 (Notifications).
    await tester.tap(find.byKey(const ValueKey('onboarding-skip')).last);
    await tester.pumpAndSettle();

    expect(find.text('LOGIN_PLACEHOLDER'), findsOneWidget);
    expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
  });

  testWidgets('Notifications "Get started" still completes when FCM throws',
      (tester) async {
    // Firebase.initializeApp() throws in widget tests (no platform
    // channels). The card swallows the error and advances. We assert
    // the user-visible outcome — flag set, navigated to /login.
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('onboarding-cluster-next')));
    await tester.pumpAndSettle();
    await tester.tap(
        find.byKey(const ValueKey('onboarding-notifications-get-started')));
    await tester.pumpAndSettle();

    expect(find.text('LOGIN_PLACEHOLDER'), findsOneWidget);
    expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
  });

  testWidgets('install-guide CTA routes through url_launcher', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    final launcher = _StubLauncher();
    await _pump(tester, prefs, launcher: launcher);

    await tester
        .tap(find.byKey(const ValueKey('onboarding-intro-install-guide')));
    await tester.pump();

    expect(launcher.launched, contains('https://kubecenter.io/install'));
    // External link does NOT complete onboarding — flag still absent.
    expect(prefs.containsKey(kOnboardedPrefsKey), isFalse);
  });

  testWidgets('install-guide CTA shows snackbar on launch failure',
      (tester) async {
    final prefs = await SharedPreferences.getInstance();
    final launcher = _StubLauncher(launchResult: false);
    await _pump(tester, prefs, launcher: launcher);

    await tester
        .tap(find.byKey(const ValueKey('onboarding-intro-install-guide')));
    await tester.pump(); // trigger async
    await tester.pump(const Duration(milliseconds: 100)); // settle snackbar

    expect(find.textContaining('Could not open'), findsOneWidget);
  });

  testWidgets('page indicator announces "Step N of 3"', (tester) async {
    // `addTearDown(handle.dispose)` fires AFTER
    // `_verifySemanticsHandlesWereDisposed`, so the handle is disposed
    // inline before returning.
    final handle = tester.ensureSemantics();

    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    expect(find.bySemanticsLabel('Step 1 of 3'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('onboarding-intro-have-server')));
    await tester.pumpAndSettle();

    expect(find.bySemanticsLabel('Step 2 of 3'), findsOneWidget);

    handle.dispose();
  });

  testWidgets('icons carry accessibility labels', (tester) async {
    final prefs = await SharedPreferences.getInstance();
    await _pump(tester, prefs);

    // Intro card icon.
    final introIcon = tester.widget<Icon>(find.byIcon(Icons.dns_outlined));
    expect(introIcon.semanticLabel, isNotNull);
    expect(introIcon.semanticLabel, isNotEmpty);
  });
}
