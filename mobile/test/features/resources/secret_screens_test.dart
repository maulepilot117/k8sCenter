// Sanitization + redaction guarantees on the Secret detail flow.
// Tests use String.fromCharCode for control characters so the source
// file stays printable and editor-safe.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/resources/secret_screens.dart';

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
}
