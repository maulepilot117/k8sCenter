// Unit tests for serverIndexForRow — the pure helper that maps a
// display-row index to the server/stripped index the backend keys
// `ports[N]` errors against (empty rows are dropped before send).
// Shared by the Service and Deployment wizards.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/wizards/widgets/repeating_row_index.dart';

void main() {
  // A predicate over an explicit emptiness list keeps the test free of
  // any wizard row type.
  bool Function(int) emptyAt(List<bool> empties) => (i) => empties[i];

  group('serverIndexForRow', () {
    test('returns null when the row at displayIndex is empty', () {
      final empties = [true, false];
      expect(
        serverIndexForRow(empties.length, emptyAt(empties), 0),
        isNull,
      );
    });

    test('[filled, empty, filled]: display 2 maps to server 1', () {
      final empties = [false, true, false];
      expect(
        serverIndexForRow(empties.length, emptyAt(empties), 2),
        1,
      );
    });

    test('returns null when displayIndex is out of range', () {
      final empties = [false, false];
      expect(
        serverIndexForRow(empties.length, emptyAt(empties), 2),
        isNull,
      );
      expect(
        serverIndexForRow(empties.length, emptyAt(empties), -1),
        isNull,
      );
    });

    test('all-filled: display N maps to server N', () {
      final empties = [false, false, false, false];
      for (var i = 0; i < empties.length; i++) {
        expect(serverIndexForRow(empties.length, emptyAt(empties), i), i);
      }
    });

    test('only-empty rows: every index maps to null', () {
      final empties = [true, true, true];
      for (var i = 0; i < empties.length; i++) {
        expect(
          serverIndexForRow(empties.length, emptyAt(empties), i),
          isNull,
        );
      }
    });
  });
}
