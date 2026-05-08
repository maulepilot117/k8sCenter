// duration_input tests — unit-level on the parser. The widget itself
// is a thin TextFormField wrapper; coverage of the parser is the
// load-bearing assertion.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/wizards/widgets/duration_input.dart';

void main() {
  group('validateDuration', () {
    test('empty is OK (field is optional)', () {
      expect(validateDuration(''), null);
      expect(validateDuration('   '), null);
    });

    test('canonical values parse', () {
      expect(validateDuration('30m'), null);
      expect(validateDuration('24h'), null);
      expect(validateDuration('7d'), null);
      expect(validateDuration('0s'), null);
      expect(validateDuration('1h30m'), null);
      expect(validateDuration('168h'), null);
    });

    test('decimal values parse', () {
      expect(validateDuration('1.5h'), null);
    });

    test('rejects values without unit', () {
      expect(validateDuration('60'), isNotNull);
    });

    test('rejects letters-only', () {
      expect(validateDuration('xyz'), isNotNull);
      expect(validateDuration('foo24h'), isNotNull);
    });

    test('rejects unknown units', () {
      expect(validateDuration('1y'), isNotNull);
      expect(validateDuration('1mo'), isNotNull);
    });
  });
}
