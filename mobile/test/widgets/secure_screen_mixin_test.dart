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

        expect(state.isBlurOverlayInserted, isFalse);

        await state.setSensitive(true);
        await tester.pump();

        // OverlayEntry is in the tree; scrim is not yet painted.
        expect(state.isBlurOverlayInserted, isTrue);
        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
      });
    });

    testWidgets(
        'setSensitive(false) removes the eagerly-inserted OverlayEntry',
        (tester) async {
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));

        await state.setSensitive(true);
        expect(state.isBlurOverlayInserted, isTrue);

        await state.setSensitive(false);
        await tester.pump();

        expect(state.isBlurOverlayInserted, isFalse);
        expect(state.isBlurOverlayShown, isFalse);
        expect(find.byType(BackdropFilter), findsNothing);
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

        expect(state.isBlurOverlayInserted, isFalse);
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

        // Anchor the assertions on our `IgnorePointer(ignoring: true)`,
        // then walk down. MaterialApp/Scaffold inject their own
        // `IgnorePointer(ignoring: false)` and `ExcludeSemantics` widgets
        // so a top-level type lookup is ambiguous.
        final overlayIgnore = find.byWidgetPredicate(
          (w) => w is IgnorePointer && w.ignoring == true,
        );
        expect(overlayIgnore, findsOneWidget);
        expect(
          find.descendant(of: overlayIgnore, matching: find.byType(ExcludeSemantics)),
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
          _fireLifecycle(AppLifecycleState.resumed);
          await tester.pump();
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
      await withPlatform(TargetPlatform.iOS, () async {
        await tester.pumpWidget(const MaterialApp(home: _Host()));
        final state = tester.state<_HostState>(find.byType(_Host));
        await state.setSensitive(true);

        await tester.pumpWidget(const MaterialApp(home: SizedBox()));
        await tester.pump();

        // After dispose, firing inactive must not insert a new overlay
        // or throw. The observer was removed; this exercises the safety
        // net for any lingering scheduler callbacks.
        _fireLifecycle(AppLifecycleState.inactive);
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
