// Tests for KindPicker — verifies selection state, onChanged
// emission, and that errorMessage renders inline.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/kind_picker.dart';

Widget _wrap(Widget child) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(
      body: Padding(padding: const EdgeInsets.all(16), child: child),
    ),
  );
}

void main() {
  group('KindPicker', () {
    testWidgets('renders one ChoiceChip per option with correct selected state',
        (tester) async {
      var picked = '';
      await tester.pumpWidget(_wrap(KindPicker(
        options: const [
          KindPickerOption(value: 'A', label: 'Option A'),
          KindPickerOption(value: 'B', label: 'Option B'),
          KindPickerOption(value: 'C', label: 'Option C'),
        ],
        selected: 'B',
        onChanged: (v) => picked = v,
      )));

      expect(find.byType(ChoiceChip), findsNWidgets(3));
      // Selected chip is 'B'.
      final bChip = tester
          .widget<ChoiceChip>(find.widgetWithText(ChoiceChip, 'Option B'));
      expect(bChip.selected, isTrue);
      final aChip = tester
          .widget<ChoiceChip>(find.widgetWithText(ChoiceChip, 'Option A'));
      expect(aChip.selected, isFalse);

      // Tap A -> onChanged emits 'A'.
      await tester.tap(find.widgetWithText(ChoiceChip, 'Option A'));
      await tester.pump();
      expect(picked, 'A');
    });

    testWidgets('errorMessage renders below the chips', (tester) async {
      await tester.pumpWidget(_wrap(KindPicker(
        options: const [KindPickerOption(value: 'X', label: 'X')],
        selected: '',
        onChanged: (_) {},
        errorMessage: 'Pick a kind',
      )));
      expect(find.text('Pick a kind'), findsOneWidget);
    });

    testWidgets('label renders above the chip row when set', (tester) async {
      await tester.pumpWidget(_wrap(KindPicker(
        options: const [KindPickerOption(value: 'X', label: 'X')],
        selected: 'X',
        onChanged: (_) {},
        label: 'Target kind',
      )));
      expect(find.text('Target kind'), findsOneWidget);
    });
  });
}
