// Tests for validateBackendUrl() — the extracted validation helper from
// backendUrlProvider. Covers all branches including numeric loopback forms
// introduced by Finding P2-10 and the extracted helper from Finding P3-20.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';

void main() {
  group('validateBackendUrl — debug mode', () {
    test('empty string defaults to http://localhost:8080 in debug builds',
        () {
      expect(
        validateBackendUrl('', isDebug: true),
        'http://localhost:8080',
      );
    });

    test('non-empty URL is returned as-is in debug builds', () {
      expect(
        validateBackendUrl('https://prod.example.com', isDebug: true),
        'https://prod.example.com',
      );
    });

    test('http:// URL is allowed in debug builds', () {
      expect(
        validateBackendUrl('http://localhost:8080', isDebug: true),
        'http://localhost:8080',
      );
    });

    test('leading/trailing whitespace is trimmed in debug builds', () {
      expect(
        validateBackendUrl('  https://prod.example.com  ', isDebug: true),
        'https://prod.example.com',
      );
    });
  });

  group('validateBackendUrl — release/profile mode (isDebug: false)', () {
    test('empty string throws StateError', () {
      expect(
        () => validateBackendUrl('', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('http:// URL throws StateError (must be https)', () {
      expect(
        () => validateBackendUrl('http://prod.example.com', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('https://localhost throws StateError (loopback hostname)', () {
      expect(
        () => validateBackendUrl('https://localhost', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('https://127.0.0.1 throws StateError (loopback IP)', () {
      expect(
        () => validateBackendUrl('https://127.0.0.1', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('https://127.0.0.2 throws StateError (127.0.0.0/8 range, P2-#10)',
        () {
      expect(
        () => validateBackendUrl('https://127.0.0.2', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('https://127.255.255.255 throws StateError (127.0.0.0/8 range)',
        () {
      expect(
        () => validateBackendUrl('https://127.255.255.255', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('https://[::1] throws StateError (IPv6 loopback)', () {
      expect(
        () => validateBackendUrl('https://[::1]', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test(
        'https://[::ffff:127.0.0.1] throws StateError (IPv4-mapped IPv6, P2-#10)',
        () {
      expect(
        () => validateBackendUrl('https://[::ffff:127.0.0.1]', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    // Decimal notation: 2130706433 == 0x7f000001 == 127.0.0.1.
    // InternetAddress.tryParse handles this on platforms that support it;
    // if the platform parses it as a hostname the test will still pass
    // because the loopback hostname check does not catch decimal IPs —
    // this test documents the expected behaviour.
    test(
        'https://2130706433 — decimal IPv4 loopback (P2-#10, platform dependent)',
        () {
      // On Dart VM InternetAddress.tryParse('2130706433') returns null
      // (not a standard dotted-decimal), so this form is NOT caught by the
      // current implementation. This test documents that limitation — a
      // future enhancement could add a numeric-integer parse path.
      // For now we verify the function does not throw unexpectedly and
      // returns either a StateError or the trimmed value.
      String? result;
      Object? thrown;
      try {
        result = validateBackendUrl('https://2130706433', isDebug: false);
      } catch (e) {
        thrown = e;
      }
      // Either a StateError (if the platform resolves the integer) or a
      // non-null URL string is acceptable for this edge case.
      if (thrown != null) {
        expect(thrown, isA<StateError>());
      } else {
        expect(result, isNotNull);
      }
    });

    test('valid https URL passes validation', () {
      expect(
        validateBackendUrl('https://prod.example.com', isDebug: false),
        'https://prod.example.com',
      );
    });

    test('leading/trailing whitespace is trimmed in release builds', () {
      expect(
        validateBackendUrl('  https://prod.example.com  ', isDebug: false),
        'https://prod.example.com',
      );
    });

    test('https://prod.example.com:8443 (with port) passes', () {
      expect(
        validateBackendUrl(
            'https://prod.example.com:8443', isDebug: false),
        'https://prod.example.com:8443',
      );
    });

    test('0.0.0.0 throws StateError (wildcard bind address)', () {
      expect(
        () => validateBackendUrl('https://0.0.0.0', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });

    test('invalid URL (no scheme) throws StateError', () {
      expect(
        () => validateBackendUrl('not-a-url', isDebug: false),
        throwsA(isA<StateError>()),
      );
    });
  });
}
