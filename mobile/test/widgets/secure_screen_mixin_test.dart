import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/widgets/secure_screen_mixin.dart';

import '../support/platform_helpers.dart';

void main() {
  // `kIgnoreDebugForTests` defaults to `kDebugMode`, which is `true` in
  // `flutter test`, so the production code path runs without any setup
  // here. The debug-gate test below explicitly flips it back to `false`
  // for that one case.

  group('SecureScreenMixin Android', () {
    late List<MethodCall> calls;

    setUp(() {
      calls = <MethodCall>[];
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(
        const MethodChannel('flutter_windowmanager'),
        (call) async {
          calls.add(call);
          return true;
        },
      );
    });

    tearDown(() {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(
        const MethodChannel('flutter_windowmanager'),
        null,
      );
    });

    testWidgets('setSensitive(true) adds FLAG_SECURE', (tester) async {
      await withPlatform(TargetPlatform.android, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await tester.pump();

        expect(calls, hasLength(1));
        expect(calls.single.method, 'addFlags');
        expect(calls.single.arguments['flags'], 0x00002000); // FLAG_SECURE
      });
    });

    testWidgets('setSensitive(false) clears FLAG_SECURE', (tester) async {
      await withPlatform(TargetPlatform.android, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        calls.clear();
        await state.setSensitive(false);

        expect(calls.single.method, 'clearFlags');
        expect(calls.single.arguments['flags'], 0x00002000);
      });
    });

    testWidgets('idempotent — repeat setSensitive(true) is a no-op',
        (tester) async {
      await withPlatform(TargetPlatform.android, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await state.setSensitive(true);
        await state.setSensitive(true);

        expect(calls.where((c) => c.method == 'addFlags'), hasLength(1));
      });
    });

    testWidgets('dispose clears FLAG_SECURE if sensitive was true',
        (tester) async {
      await withPlatform(TargetPlatform.android, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        calls.clear();

        await tester.pumpWidget(const MaterialApp(home: SizedBox()));
        await tester.pump();

        expect(calls.any((c) => c.method == 'clearFlags'), isTrue);
      });
    });
  });

  group('SecureScreenMixin iOS lifecycle', () {
    testWidgets(
        'setSensitive(true) eagerly inserts the OverlayEntry (#271)',
        (tester) async {
      // Pre-#271 behaviour deferred the OverlayEntry to the lifecycle
      // handler; iOS could snapshot the app-switcher before Flutter
      // materialized the entry. The eager pattern keeps the entry in the
      // tree (hidden) so only a ValueNotifier flip is needed on lifecycle.
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsNothing);

        await state.setSensitive(true);
        await tester.pump();

        // OverlayEntry is in the tree; scrim is not yet painted.
        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsOneWidget);
        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets(
        'eager insertion happens BEFORE the platform-channel await (#271)',
        (tester) async {
      // The PR's central invariant: _insertBlurOverlay runs synchronously
      // before setSensitive awaits the platform channel. A regression that
      // moved the insertion to AFTER the await would still pass the
      // "eagerly inserts" test because that test fully awaits setSensitive
      // before asserting. This test starts setSensitive WITHOUT awaiting,
      // pumps once to land the OverlayState rebuild, then asserts the
      // entry is present, finally awaits the platform call.
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        final pending = state.setSensitive(true);
        // One pump lands the synchronous OverlayEntry insertion + its
        // first build. The platform-channel mock above always resolves
        // synchronously in tests, so this assertion is meaningful only
        // because we have not yet awaited the future returned by
        // setSensitive — the pre-await invariant is what we are pinning.
        await tester.pump();
        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsOneWidget);
        await pending;
      });
    });

    testWidgets(
        'setSensitive(false) removes the eagerly-inserted OverlayEntry',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await tester.pump();
        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsOneWidget);

        await state.setSensitive(false);
        await tester.pump();

        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsNothing);
        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets(
        'iOS setSensitive(true) is idempotent — second call does not stack overlays',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await state.setSensitive(true);
        await state.setSensitive(true);
        _fireLifecycle(AppLifecycleState.inactive);
        await tester.pump();

        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsOneWidget);
        expect(find.byType(BackdropFilter), findsOneWidget);
      });
    });

    testWidgets(
        'rapid true → false → true awaited sequence preserves the overlay (#271 C1)',
        (tester) async {
      // Regression test for the rapid-toggle bug surfaced by the code
      // review: post-await teardown must read the current _sensitive
      // field, not the local arg captured at call time. Otherwise the
      // middle call's tail removes the overlay the third call relies on.
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        // Issue all three calls without awaiting between — overlap the
        // chained platform-channel futures.
        final c1 = state.setSensitive(true);
        final c2 = state.setSensitive(false);
        final c3 = state.setSensitive(true);
        await Future.wait([c1, c2, c3]);
        _fireLifecycle(AppLifecycleState.inactive);
        await tester.pump();

        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsOneWidget);
        expect(find.byType(BackdropFilter), findsOneWidget);
      });
    });

    testWidgets(
        'AppLifecycleState.inactive paints the eager overlay when sensitive',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        expect(state.isBlurOverlayShown, isFalse);

        _fireLifecycle(AppLifecycleState.inactive);
        await tester.pump();

        expect(state.isBlurOverlayShown, isTrue);
        expect(find.byType(BackdropFilter), findsOneWidget);
      });
    });

    testWidgets(
        'AppLifecycleState.resumed removes the blur overlay',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        _fireLifecycle(AppLifecycleState.inactive);
        await tester.pump();

        _fireLifecycle(AppLifecycleState.resumed);
        await tester.pump();

        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets('no blur when sensitive == false and app backgrounded',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        _fireLifecycle(AppLifecycleState.inactive);
        await tester.pump();

        expect(find.byKey(SecureScreenMixin.blurOverlayKey), findsNothing);
        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets(
        'overlay is non-interactive and excluded from semantics tree',
        (tester) async {
      // The eager overlay sits on top of the sensitive screen while
      // hidden — confirm it neither swallows pointer events nor pollutes
      // the a11y tree of the screen below. MaterialApp/Scaffold inject
      // their own `IgnorePointer(ignoring: false)` widgets; filter to the
      // single overlay one with `ignoring: true`.
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await tester.pump();

        // Anchor on the overlay's ValueKey; MaterialApp/Scaffold inject
        // their own IgnorePointer/ExcludeSemantics widgets so a top-level
        // type lookup is ambiguous and the `ignoring: true` predicate is
        // fragile across Flutter versions.
        final overlayRoot = find.byKey(SecureScreenMixin.blurOverlayKey);
        expect(overlayRoot, findsOneWidget);
        final overlayIgnore = find.descendant(
          of: overlayRoot,
          matching: find.byType(IgnorePointer),
        );
        expect(overlayIgnore, findsOneWidget);
        expect(
          tester.widget<IgnorePointer>(overlayIgnore).ignoring,
          isTrue,
        );
        expect(
          find.descendant(of: overlayRoot, matching: find.byType(ExcludeSemantics)),
          findsOneWidget,
        );
      });
    });

    testWidgets(
        'rapid inactive-resumed-inactive cycles leave no orphans',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        for (var i = 0; i < 5; i++) {
          _fireLifecycle(AppLifecycleState.inactive);
          await tester.pump();
          // OverlayEntry must remain mounted across every cycle — the
          // PR's eager-overlay invariant is that the entry lives for
          // the duration of the sensitive session, not per lifecycle.
          expect(
            find.byKey(SecureScreenMixin.blurOverlayKey),
            findsOneWidget,
            reason: 'overlay disappeared on inactive cycle $i',
          );
          _fireLifecycle(AppLifecycleState.resumed);
          await tester.pump();
          expect(
            find.byKey(SecureScreenMixin.blurOverlayKey),
            findsOneWidget,
            reason: 'overlay disappeared on resumed cycle $i',
          );
        }

        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets('paused fires the same insertion as inactive', (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        _fireLifecycle(AppLifecycleState.paused);
        await tester.pump();

        expect(state.isBlurOverlayShown, isTrue);
      });
    });

    testWidgets('hidden fires the same insertion as inactive',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        _fireLifecycle(AppLifecycleState.hidden);
        await tester.pump();

        expect(state.isBlurOverlayShown, isTrue);
      });
    });

    testWidgets(
        'lifecycle event after dispose does not insert overlay or throw',
        (tester) async {
      // Capture the observer BEFORE dispose so we can actually exercise
      // the post-dispose safety net. The framework's removeObserver in
      // dispose() means _fireLifecycle would no longer route through
      // this State otherwise, making the test vacuous. Invoking
      // didChangeAppLifecycleState on the observer directly simulates
      // the scenario where a queued scheduler callback fires against
      // a disposed mixin (the disposed-ValueNotifier safety net).
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));
        await state.setSensitive(true);
        final observer = state.debugLifecycleObserver;
        expect(observer, isNotNull);

        await tester.pumpWidget(const MaterialApp(home: SizedBox()));
        await tester.pump();

        // State is disposed; ValueNotifier is disposed. Direct invocation
        // of didChangeAppLifecycleState must take the no-overlay-mounted
        // early-return path (added by review finding #3) without
        // touching the disposed notifier.
        expect(
          () => observer!.didChangeAppLifecycleState(
            AppLifecycleState.inactive,
          ),
          returnsNormally,
        );
        await tester.pump();

        expect(find.byType(BackdropFilter), findsNothing);
      });
    });
  });

  group('SecureScreenMixin debug-mode gate', () {
    testWidgets(
        'in debug build, setSensitive(true) is a no-op (no platform call)',
        (tester) async {
      // Re-enable the production gate for this test only.
      SecureScreenMixin.kIgnoreDebugForTests = false;
      // Re-enable at the end so the suite-level tearDown still applies
      // its reset cleanly; this guards against assertion order surprises.
      addTearDown(() => SecureScreenMixin.kIgnoreDebugForTests = true);

      if (!kDebugMode) {
        // Skipped under AOT/profile/release test runs — the gate only
        // matters in debug builds.
        return;
      }

      await withPlatform(TargetPlatform.android, () async {
        final platformCalls = <MethodCall>[];
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(
          const MethodChannel('flutter_windowmanager'),
          (call) async {
            platformCalls.add(call);
            return true;
          },
        );
        addTearDown(() {
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
              .setMockMethodCallHandler(
            const MethodChannel('flutter_windowmanager'),
            null,
          );
        });

        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        await tester.pump();

        expect(state.isSensitive, isFalse);
        expect(platformCalls, isEmpty);
      });
    });
  });
}

void _fireLifecycle(AppLifecycleState state) {
  TestWidgetsFlutterBinding.instance.handleAppLifecycleStateChanged(state);
}

class _Host extends StatefulWidget {
  const _Host();

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> with SecureScreenMixin<_Host> {
  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}
