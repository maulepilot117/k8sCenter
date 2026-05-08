// Widget tests for RepeatingRowGroup. Confirms add/remove behavior and
// that the parent's list shape drives rendering.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/repeating_row_group.dart';

class _Item {
  const _Item(this.label);
  final String label;
}

Widget _harness({
  required List<_Item> initial,
}) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(
      body: _Stateful(initial: initial),
    ),
  );
}

class _Stateful extends StatefulWidget {
  const _Stateful({required this.initial});
  final List<_Item> initial;

  @override
  State<_Stateful> createState() => _StatefulState();
}

class _StatefulState extends State<_Stateful> {
  late List<_Item> _items = [...widget.initial];

  @override
  Widget build(BuildContext context) {
    return RepeatingRowGroup<_Item>(
      items: _items,
      itemBuilder: (ctx, i, item) => Text('row-${item.label}'),
      onAdd: () => setState(() => _items = [..._items, _Item('new${_items.length}')]),
      onRemove: (i) => setState(() {
        final next = [..._items]..removeAt(i);
        _items = next;
      }),
      addLabel: 'Add',
      emptyMessage: 'No items',
    );
  }
}

void main() {
  group('RepeatingRowGroup', () {
    testWidgets('renders empty message when items is empty',
        (tester) async {
      await tester.pumpWidget(_harness(initial: const []));
      expect(find.text('No items'), findsOneWidget);
      expect(find.text('Add'), findsOneWidget);
    });

    testWidgets('renders one row per item', (tester) async {
      await tester.pumpWidget(_harness(
        initial: const [_Item('a'), _Item('b')],
      ));
      expect(find.text('row-a'), findsOneWidget);
      expect(find.text('row-b'), findsOneWidget);
      expect(find.byIcon(Icons.close), findsNWidgets(2));
    });

    testWidgets('Add button appends a new item', (tester) async {
      await tester.pumpWidget(_harness(initial: const [_Item('a')]));
      expect(find.text('row-a'), findsOneWidget);
      await tester.tap(find.text('Add'));
      await tester.pump();
      expect(find.text('row-a'), findsOneWidget);
      expect(find.text('row-new1'), findsOneWidget);
    });

    testWidgets('Remove tap drops the row at index', (tester) async {
      await tester.pumpWidget(_harness(
        initial: const [_Item('a'), _Item('b')],
      ));
      await tester.tap(find.byIcon(Icons.close).first);
      await tester.pump();
      expect(find.text('row-a'), findsNothing);
      expect(find.text('row-b'), findsOneWidget);
    });
  });
}
