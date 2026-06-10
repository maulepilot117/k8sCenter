// Widget tests for KeyValueTable.
//
// Covers the core UX promises:
//   - A trailing empty row is always rendered for the operator to type
//     into without an explicit "Add" button.
//   - Filling a row triggers the trailing-empty refill on the next build.
//   - Removing a row drops it from the parent's list.
//   - Empty trailing rows are stripped from the value bubbled up via
//     [onChanged].

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/key_value_table.dart';

Widget _harness({
  required List<KeyValuePair> initial,
  required ValueChanged<List<KeyValuePair>> onChanged,
}) {
  // KeyValueTable reads `Theme.of(context).extension<KubeColors>()!` for
  // its error footer color. Tests must supply the kube theme or the
  // build crashes on the first render.
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: Scaffold(
      body: _Stateful(initial: initial, onChanged: onChanged),
    ),
  );
}

class _Stateful extends StatefulWidget {
  const _Stateful({required this.initial, required this.onChanged});
  final List<KeyValuePair> initial;
  final ValueChanged<List<KeyValuePair>> onChanged;

  @override
  State<_Stateful> createState() => _StatefulState();
}

class _StatefulState extends State<_Stateful> {
  late List<KeyValuePair> _pairs = widget.initial;

  @override
  Widget build(BuildContext context) {
    return KeyValueTable(
      pairs: _pairs,
      onChanged: (next) {
        setState(() => _pairs = next);
        widget.onChanged(next);
      },
    );
  }
}

void main() {
  group('KeyValueTable', () {
    testWidgets('starts with one empty row when initial pairs is empty',
        (tester) async {
      List<KeyValuePair>? last;
      await tester.pumpWidget(_harness(
        initial: const [],
        onChanged: (next) => last = next,
      ));

      // Two TextFields per row (key + value).
      expect(find.byType(TextField), findsNWidgets(2));
      // Nothing has been typed yet — onChanged shouldn't fire.
      expect(last, isNull);
    });

    testWidgets('shows pre-filled rows plus a trailing empty row',
        (tester) async {
      await tester.pumpWidget(_harness(
        initial: const [KeyValuePair(key: 'a', value: '1')],
        onChanged: (_) {},
      ));

      // 1 filled + 1 trailing empty = 2 rows = 4 TextFields.
      expect(find.byType(TextField), findsNWidgets(4));
    });

    testWidgets(
        'typing into the trailing empty row strips empty rows when '
        'bubbling up to onChanged', (tester) async {
      List<KeyValuePair>? last;
      await tester.pumpWidget(_harness(
        initial: const [],
        onChanged: (next) => last = next,
      ));

      // Find the first key field and type into it.
      final keyFields = find.byType(TextField);
      await tester.enterText(keyFields.first, 'foo');
      await tester.pump();

      // After the first edit, onChanged emits a single non-empty row.
      // The trailing-empty sentinel is added back in didUpdateWidget.
      expect(last, isNotNull);
      expect(last!.length, 1);
      expect(last!.first.key, 'foo');
    });

    testWidgets(
        'remove icon on a filled row drops it from the parent list',
        (tester) async {
      List<KeyValuePair>? last;
      await tester.pumpWidget(_harness(
        initial: const [
          KeyValuePair(key: 'a', value: '1'),
          KeyValuePair(key: 'b', value: '2'),
        ],
        onChanged: (next) => last = next,
      ));

      // Three remove buttons — one per visible row (including trailing
      // empty). Tap the first row's remove.
      final removes = find.byIcon(Icons.close);
      expect(removes, findsNWidgets(3));
      await tester.tap(removes.first);
      await tester.pump();

      expect(last, isNotNull);
      expect(last!.length, 1);
      expect(last!.first.key, 'b');
    });
  });
}
