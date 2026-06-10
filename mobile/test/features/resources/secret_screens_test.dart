// Sanitization + redaction guarantees on the Secret detail flow.
// Tests use String.fromCharCode for control characters so the source
// file stays printable and editor-safe.
//
// Widget-level coverage verifies the FLAG_SECURE wiring added in PR-5d:
// revealing a key arms the secure-screen flag, concealing the last key
// clears it, and a still-revealed sibling key keeps the flag armed when
// any one key is hidden.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:flutter_windowmanager_plus/flutter_windowmanager_plus.dart';
import 'package:kubecenter/features/resources/secret_screens.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/secure_screen_mixin.dart';

import '../../support/mock_dio_adapter.dart';
import '../../support/platform_helpers.dart';

void main() {
  group('sanitizeSecretValue', () {
    test('preserves printable ASCII', () {
      expect(sanitizeSecretValue('hello-world_123'), 'hello-world_123');
    });

    test('preserves whitespace (tab, newline, CR)', () {
      expect(sanitizeSecretValue('a\tb\nc\r'), 'a\tb\nc\r');
    });

    test('escapes RIGHT-TO-LEFT OVERRIDE (Trojan-Source vector)', () {
      final input = 'password${String.fromCharCode(0x202E)}suffix';
      final out = sanitizeSecretValue(input);
      expect(out, 'password\\u202esuffix');
      // No raw U+202E character survived.
      expect(out.codeUnits, isNot(contains(0x202E)));
    });

    test('escapes other BiDi control characters', () {
      for (final code in const [0x202A, 0x202B, 0x202D, 0x2066, 0x2069]) {
        final input = String.fromCharCode(code);
        final escaped = sanitizeSecretValue(input);
        expect(escaped, contains('\\u'));
        expect(escaped.codeUnits, isNot(contains(code)));
      }
    });

    test('escapes zero-width characters', () {
      final input = 'a${String.fromCharCode(0x200B)}b';
      expect(sanitizeSecretValue(input), 'a\\u200bb');
    });

    test('escapes ESC and other C0 controls (except tab/newline/CR)', () {
      // ESC (0x1B) followed by a typical ANSI color code.
      final input = '${String.fromCharCode(0x1B)}[31mred';
      expect(sanitizeSecretValue(input), '\\u001b[31mred');
    });

    test('escapes DEL (0x7F)', () {
      final input = String.fromCharCode(0x7F);
      expect(sanitizeSecretValue(input), '\\u007f');
    });

    test('handles multi-byte UTF-8 (emoji) without escaping', () {
      // U+1F512 LOCK emoji — outside any escape range.
      const input = 'pwd-🔒';
      expect(sanitizeSecretValue(input), input);
    });
  });

  group('SecretDetailScreen FLAG_SECURE wiring (PR-5d)', () {
    const namespace = 'default';
    const name = 'app-credentials';

    // Two-key Secret. Values are base64 placeholders — the screen masks
    // them anyway; only the reveal endpoint surfaces plaintext.
    final secretBody = {
      'data': {
        'kind': 'Secret',
        'type': 'Opaque',
        'metadata': {
          'name': name,
          'namespace': namespace,
          'uid': 'sec-uid-1',
          'creationTimestamp': '2026-04-01T00:00:00Z',
        },
        'data': {
          'username': 'YWRtaW4=', // "admin"
          'password': 'aHVudGVyMg==', // "hunter2"
        },
      },
    };

    void mockSecret(MockDioAdapter mock) {
      mock.onJson(
        'GET',
        '/api/v1/resources/secrets/$namespace/$name',
        body: secretBody,
      );
    }

    void mockReveal(
      MockDioAdapter mock,
      String key,
      String value,
    ) {
      mock.onJson(
        'GET',
        '/api/v1/resources/secrets/$namespace/$name/reveal/$key',
        body: {
          'data': {'value': value},
        },
      );
    }

    ({ProviderContainer container, MockDioAdapter mock}) makeContainer() {
      final mock = MockDioAdapter();
      final container = ProviderContainer(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
      );
      container.read(dioProvider).httpClientAdapter = mock;
      container.read(refreshDioProvider).httpClientAdapter = mock;
      return (container: container, mock: mock);
    }

    Widget harness(ProviderContainer container) {
      return UncontrolledProviderScope(
        container: container,
        child: MaterialApp(
          theme: buildKubeTheme('liquid-glass'),
          home: const SecretDetailScreen(
            namespace: namespace,
            name: name,
          ),
        ),
      );
    }

    /// Stubs [SecureScreenMixin]'s secure-flag seam and returns the captured
    /// calls list for assertions. The seam is used instead of mocking the
    /// `flutter_windowmanager_plus` MethodChannel directly because the plugin
    /// self-guards on `dart:io` `Platform.isAndroid` (false under `flutter
    /// test`) and would never reach the channel. Synthesizes `MethodCall`s so
    /// the per-test assertions (method name + `flags` argument) are unchanged.
    /// Caller is responsible for nothing — `addTearDown` cleanup is registered
    /// here.
    List<MethodCall> mockWindowManager() {
      final calls = <MethodCall>[];
      SecureScreenMixin.addSecureFlags = (flags) async {
        calls.add(MethodCall('addFlags', <String, dynamic>{'flags': flags}));
        return true;
      };
      SecureScreenMixin.clearSecureFlags = (flags) async {
        calls.add(MethodCall('clearFlags', <String, dynamic>{'flags': flags}));
        return true;
      };
      addTearDown(() {
        SecureScreenMixin.addSecureFlags = FlutterWindowManagerPlus.addFlags;
        SecureScreenMixin.clearSecureFlags = FlutterWindowManagerPlus.clearFlags;
      });
      return calls;
    }

    testWidgets(
      'revealing a key adds FLAG_SECURE; concealing the last key clears it',
      (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          final calls = mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          mockReveal(mock, 'username', 'admin');

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          // Initial render: nothing revealed, no FLAG_SECURE traffic yet.
          expect(calls, isEmpty);

          // Tap the Reveal button for the `username` key.
          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();

          // FLAG_SECURE added once.
          final adds = calls.where((c) => c.method == 'addFlags').toList();
          expect(adds, hasLength(1));
          expect(adds.single.arguments['flags'], 0x00002000);

          // Now Hide the key — last revealed key concealed → clear.
          calls.clear();
          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();

          final clears = calls.where((c) => c.method == 'clearFlags').toList();
          expect(clears, hasLength(1));
          expect(clears.single.arguments['flags'], 0x00002000);
        });
      },
    );

    testWidgets(
      'FLAG_SECURE stays added while any other key remains revealed',
      (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          final calls = mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          mockReveal(mock, 'username', 'admin');
          mockReveal(mock, 'password', 'hunter2');

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          // Reveal both keys.
          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();
          await tester.tap(find.byKey(const ValueKey('secret-toggle-password')));
          await tester.pumpAndSettle();

          // First reveal adds; second is idempotent — no second addFlags.
          expect(calls.where((c) => c.method == 'addFlags'), hasLength(1));
          expect(calls.where((c) => c.method == 'clearFlags'), isEmpty);

          // Conceal `username` only — `password` still revealed, must
          // NOT clear FLAG_SECURE.
          calls.clear();
          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();

          expect(calls.where((c) => c.method == 'clearFlags'), isEmpty);
          expect(calls.where((c) => c.method == 'addFlags'), isEmpty);

          // Conceal the last revealed key — now it clears.
          await tester.tap(find.byKey(const ValueKey('secret-toggle-password')));
          await tester.pumpAndSettle();

          expect(calls.where((c) => c.method == 'clearFlags'), hasLength(1));
        });
      },
    );

    testWidgets(
      'route disposal while a key is revealed clears FLAG_SECURE',
      (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          final calls = mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          mockReveal(mock, 'username', 'admin');

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();
          expect(calls.where((c) => c.method == 'addFlags'), hasLength(1));

          // Replace the entire app with an empty shell — disposes the
          // detail State, which clears via the mixin's dispose path.
          calls.clear();
          await tester.pumpWidget(const MaterialApp(home: SizedBox()));
          await tester.pump();

          final clears = calls.where((c) => c.method == 'clearFlags').toList();
          expect(clears, isNotEmpty);
          expect(clears.first.arguments['flags'], 0x00002000);
        });
      },
    );

    testWidgets(
      'reveal failure (500) does NOT arm FLAG_SECURE',
      (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          final calls = mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          // Reveal endpoint returns 500 — Dio's default validateStatus
          // rejects 5xx → ApiError flows into the screen's catch block.
          mock.onJson(
            'GET',
            '/api/v1/resources/secrets/$namespace/$name/reveal/username',
            status: 500,
            body: {
              'error': {'code': 500, 'message': 'reveal failed'},
            },
          );

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();

          // No platform-channel traffic — sensitivity must stay false.
          expect(calls.where((c) => c.method == 'addFlags'), isEmpty);
          expect(calls.where((c) => c.method == 'clearFlags'), isEmpty);

          // The failure snackbar should appear and the toggle button
          // should still read 'Reveal' (state never advanced to revealed).
          expect(find.textContaining('Reveal failed'), findsOneWidget);
        });
      },
    );

    testWidgets(
      'rapid reveal/conceal/reveal toggles leave consistent state',
      (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          final calls = mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          mockReveal(mock, 'username', 'admin');

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          // Three reveal/conceal cycles. Mixin's _inflightPlatformCall
          // serializes the platform side; the screen must not leak
          // orphaned addFlags/clearFlags calls.
          for (var i = 0; i < 3; i++) {
            await tester.tap(
              find.byKey(const ValueKey('secret-toggle-username')),
            );
            await tester.pumpAndSettle();
            await tester.tap(
              find.byKey(const ValueKey('secret-toggle-username')),
            );
            await tester.pumpAndSettle();
          }

          // Final state: nothing revealed → FLAG_SECURE cleared.
          // Each cycle contributes one addFlags + one clearFlags; mixin
          // collapses no-op re-entries, so the totals must match.
          final adds = calls.where((c) => c.method == 'addFlags').toList();
          final clears = calls.where((c) => c.method == 'clearFlags').toList();
          expect(adds, hasLength(3));
          expect(clears, hasLength(3));
        });
      },
    );

    testWidgets(
      'iOS lifecycle: inactive while revealed inserts blur overlay; '
      'resumed removes it',
      (tester) async {
        await withPlatform(TargetPlatform.iOS, () async {
          // Channel mock is unused on iOS — the mixin's iOS path uses
          // the overlay, not platform channels. Registering anyway is
          // safe and keeps teardown consistent across platform branches.
          mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);
          mockReveal(mock, 'username', 'admin');

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          // Reveal a key — arms sensitivity (iOS path arms the lifecycle
          // observer but does not insert overlay until backgrounded).
          await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
          await tester.pumpAndSettle();
          expect(find.byType(BackdropFilter), findsNothing);

          TestWidgetsFlutterBinding.instance
              .handleAppLifecycleStateChanged(AppLifecycleState.inactive);
          await tester.pump();

          expect(find.byType(BackdropFilter), findsOneWidget);

          TestWidgetsFlutterBinding.instance
              .handleAppLifecycleStateChanged(AppLifecycleState.resumed);
          await tester.pump();

          expect(find.byType(BackdropFilter), findsNothing);
        });
      },
    );

    testWidgets(
      'iOS lifecycle: inactive while NOTHING revealed inserts no overlay',
      (tester) async {
        await withPlatform(TargetPlatform.iOS, () async {
          mockWindowManager();
          final (:container, :mock) = makeContainer();
          addTearDown(container.dispose);

          mockSecret(mock);

          await tester.pumpWidget(harness(container));
          await tester.pumpAndSettle();

          // Background the app without revealing anything — the screen
          // has no plaintext to protect, so the mixin must not insert
          // a blur overlay.
          TestWidgetsFlutterBinding.instance
              .handleAppLifecycleStateChanged(AppLifecycleState.inactive);
          await tester.pump();

          expect(find.byType(BackdropFilter), findsNothing);
        });
      },
    );

    group('copy-confirm dialog', () {
      /// Captures Clipboard.setData calls and serves Clipboard.getData
      /// from the last value set, so the 30s best-effort wipe (which
      /// reads before clearing) sees realistic state.
      List<MethodCall> mockClipboard() {
        final calls = <MethodCall>[];
        String? clipText;
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(SystemChannels.platform, (call) async {
          if (call.method == 'Clipboard.setData') {
            calls.add(call);
            clipText = (call.arguments as Map)['text'] as String?;
            return null;
          }
          if (call.method == 'Clipboard.getData') {
            return <String, dynamic>{'text': clipText};
          }
          return null;
        });
        addTearDown(() {
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
              .setMockMethodCallHandler(SystemChannels.platform, null);
        });
        return calls;
      }

      /// Pumps the screen, reveals `username`, and opens the dialog.
      Future<void> openDialog(WidgetTester tester) async {
        final (:container, :mock) = makeContainer();
        addTearDown(container.dispose);
        mockSecret(mock);
        mockReveal(mock, 'username', 'admin');

        await tester.pumpWidget(harness(container));
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('secret-toggle-username')));
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('secret-copy-username')));
        await tester.pumpAndSettle();

        expect(find.text('Copy secret to clipboard?'), findsOneWidget);
      }

      testWidgets('cancel dismisses without touching the clipboard',
          (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          mockWindowManager();
          final clipboardCalls = mockClipboard();
          await openDialog(tester);

          await tester.tap(find.text('Cancel'));
          await tester.pumpAndSettle();

          expect(find.text('Copy secret to clipboard?'), findsNothing);
          expect(clipboardCalls, isEmpty);
        });
      });

      testWidgets('barrier dismiss does not touch the clipboard',
          (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          mockWindowManager();
          final clipboardCalls = mockClipboard();
          await openDialog(tester);

          // Tap the scrim well outside the centered dialog.
          await tester.tapAt(const Offset(10, 10));
          await tester.pumpAndSettle();

          expect(find.text('Copy secret to clipboard?'), findsNothing);
          expect(clipboardCalls, isEmpty);
        });
      });

      testWidgets(
          'confirm copies the value, shows the snackbar, and wipes after 30s',
          (tester) async {
        await withPlatform(TargetPlatform.android, () async {
          mockWindowManager();
          final clipboardCalls = mockClipboard();
          await openDialog(tester);

          await tester.tap(find.widgetWithText(FilledButton, 'Copy'));
          await tester.pumpAndSettle();

          expect(clipboardCalls, hasLength(1));
          expect(
            (clipboardCalls.single.arguments as Map)['text'],
            'admin',
          );
          expect(
            find.text('Copied. Clipboard will clear in 30 seconds.'),
            findsOneWidget,
          );

          // Advance past the wipe delay: the clipboard still holds the
          // copied value, so the best-effort wipe must clear it.
          await tester.pump(const Duration(seconds: 31));
          await tester.pumpAndSettle();

          expect(clipboardCalls, hasLength(2));
          expect((clipboardCalls.last.arguments as Map)['text'], '');
        });
      });
    });
  });
}
