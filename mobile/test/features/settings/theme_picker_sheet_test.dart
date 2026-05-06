// Verifies that tapping a theme row in the picker switches the active
// theme and pops the sheet. Mounts the sheet directly (sidesteps the
// auth-gated app shell so we don't need a backend mock).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/settings/theme_picker_sheet.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  testWidgets('selecting Dracula updates the controller and pops the sheet',
      (tester) async {
    SharedPreferences.setMockInitialValues({});
    final prefs = await SharedPreferences.getInstance();

    Widget host(BuildContext context) {
      return Center(
        child: Builder(
          builder: (innerContext) => FilledButton(
            onPressed: () => ThemePickerSheet.show(innerContext),
            child: const Text('open'),
          ),
        ),
      );
    }

    await tester.pumpWidget(
      ProviderScope(
        overrides: [sharedPreferencesProvider.overrideWithValue(prefs)],
        child: MaterialApp(
          theme: buildKubeTheme('nexus'),
          home: Scaffold(body: Builder(builder: host)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.byType(ThemePickerSheet), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('theme-radio-dracula')));
    await tester.pumpAndSettle();

    expect(find.byType(ThemePickerSheet), findsNothing);
    expect(prefs.getString('kc_theme_id'), 'dracula');
  });
}
