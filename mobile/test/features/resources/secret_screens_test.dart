// Sanitization + redaction guarantees on the Secret detail flow.
// Tests use String.fromCharCode for control characters so the source
// file stays printable and editor-safe.
//
// Widget-level coverage verifies the FLAG_SECURE wiring added in PR-5d:
// revealing a key arms the secure-screen flag, concealing the last key
// clears it, and a still-revealed sibling key keeps the flag armed when
// any one key is hidden.

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/resources/secret_screens.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

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
          theme: buildKubeTheme('nexus'),
          home: const SecretDetailScreen(
            namespace: namespace,
            name: name,
          ),
        ),
      );
    }

    /// Runs [body] with the target platform overridden — needed because
    /// the FLAG_SECURE path only fires on Android. Restores the prior
    /// override in a `finally` block so the test framework's invariant
    /// check sees the original state.
    Future<void> withPlatform(
      TargetPlatform platform,
      Future<void> Function() body,
    ) async {
      final prior = debugDefaultTargetPlatformOverride;
      debugDefaultTargetPlatformOverride = platform;
      try {
        await body();
      } finally {
        debugDefaultTargetPlatformOverride = prior;
      }
    }

    /// Mocks the `flutter_windowmanager` MethodChannel and returns the
    /// captured calls list for assertions. Caller is responsible for
    /// `addTearDown` cleanup (registered here).
    List<MethodCall> mockWindowManager() {
      final calls = <MethodCall>[];
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(
        const MethodChannel('flutter_windowmanager'),
        (call) async {
          calls.add(call);
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

          expect(calls.any((c) => c.method == 'clearFlags'), isTrue);
        });
      },
    );
  });
}
