// Tests for ResourcesData. Validates the JSON shape sent to the
// backend — the form widget itself is thin so most coverage lives at
// the data layer.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/wizards/widgets/resources_form.dart';

void main() {
  group('ResourcesData', () {
    test('toJson returns null when all fields are blank', () {
      const r = ResourcesData();
      expect(r.toJson(), isNull);
      expect(r.isEmpty, true);
    });

    test('toJson includes only set fields', () {
      const r = ResourcesData(requestCpu: '100m', limitMemory: '512Mi');
      expect(r.toJson(), {'requestCpu': '100m', 'limitMemory': '512Mi'});
    });

    test('toJson trims whitespace', () {
      const r = ResourcesData(requestCpu: '  100m  ');
      expect(r.toJson()!['requestCpu'], '100m');
    });
  });
}
