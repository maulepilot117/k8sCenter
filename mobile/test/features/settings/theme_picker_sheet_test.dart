// Verifies that tapping a theme row in the picker switches the active
// theme and pops the sheet.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/app.dart';
import 'package:kubecenter/features/settings/theme_picker_sheet.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:shared_preferences/shared_preferences.dart';

Future<SharedPreferences> _emptyPrefs() async {
  SharedPreferences.setMockInitialValues({});
  return SharedPreferences.getInstance();
}

void main() {
  testWidgets('selecting Dracula updates the controller and pops the sheet',
      (tester) async {
    final prefs = await _emptyPrefs();

    await tester.pumpWidget(
      ProviderScope(
        overrides: [sharedPreferencesProvider.overrideWithValue(prefs)],
        child: const KubeCenterApp(),
      ),
    );
    await tester.pumpAndSettle();

    // Open theme picker.
    await tester.tap(find.byTooltip('Theme'));
    await tester.pumpAndSettle();

    expect(find.byType(ThemePickerSheet), findsOneWidget);

    // Tap the Dracula radio row.
    await tester.tap(find.byKey(const ValueKey('theme-radio-dracula')));
    await tester.pumpAndSettle();

    expect(find.byType(ThemePickerSheet), findsNothing);
    expect(prefs.getString('kc_theme_id'), 'dracula');
  });
}
