// Verifies the theme controller: defaults to Liquid Glass when no
// preference is stored, persists changes through SharedPreferences, and
// survives a fresh ProviderContainer (simulating an app restart with the
// same prefs).

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:kubecenter/theme/themes.g.dart';
import 'package:shared_preferences/shared_preferences.dart';

ProviderContainer _container(SharedPreferences prefs) {
  return ProviderContainer(
    overrides: [sharedPreferencesProvider.overrideWithValue(prefs)],
  );
}

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('defaults to Liquid Glass when nothing stored', () async {
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    expect(container.read(themeControllerProvider), defaultThemeId);
    expect(container.read(themeControllerProvider), 'liquid-glass');
  });

  test('setTheme writes to SharedPreferences', () async {
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    await container
        .read(themeControllerProvider.notifier)
        .setTheme('liquid-glass');

    expect(container.read(themeControllerProvider), 'liquid-glass');
    expect(prefs.getString('kc_theme_id'), 'liquid-glass');
  });

  test('setTheme is a no-op for unknown ids', () async {
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    await container
        .read(themeControllerProvider.notifier)
        .setTheme('definitely-not-a-theme');

    expect(container.read(themeControllerProvider), defaultThemeId);
    expect(prefs.getString('kc_theme_id'), isNull);
  });

  test('persistence survives a fresh container (simulating cold start)',
      () async {
    SharedPreferences.setMockInitialValues({'kc_theme_id': 'liquid-glass'});
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    expect(container.read(themeControllerProvider), 'liquid-glass');
  });

  test('stored retired theme id falls back to default', () async {
    // Users upgrading from the multi-theme era may still have e.g. 'nexus'
    // persisted; the controller must fall back rather than crash.
    SharedPreferences.setMockInitialValues({'kc_theme_id': 'nexus'});
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    expect(container.read(themeControllerProvider), defaultThemeId);
  });
}
