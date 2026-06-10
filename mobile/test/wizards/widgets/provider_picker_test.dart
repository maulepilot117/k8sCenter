// Widget tests for ProviderPicker and the kSecretStoreProviders registry.
//
// Covers:
//   - kSecretStoreProviders.length == 8 (exactly the web-supported set).
//   - kSecretStoreProviders[0].id == 'vault' (popular-first R10 isomorphism).
//   - findSecretStoreProvider('vault') returns non-null with id == 'vault'.
//   - findSecretStoreProvider('nonexistent') returns null.
//   - ProviderPicker renders all 8 provider tiles (label + description visible).
//   - Tapping a tile fires onChanged with the correct provider id.
//   - Selected tile renders with accent border (visual distinction).

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/provider_picker.dart';

Widget _wrap(Widget child) {
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: Scaffold(
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: child,
      ),
    ),
  );
}

void main() {
  group('kSecretStoreProviders registry', () {
    test('contains exactly 8 providers', () {
      expect(kSecretStoreProviders.length, 8);
    });

    test('first entry is vault (popular-first ordering)', () {
      expect(kSecretStoreProviders[0].id, 'vault');
    });

    test('contains the expected provider ids in order', () {
      final ids = kSecretStoreProviders.map((p) => p.id).toList();
      expect(ids, [
        'vault',
        'aws',
        'awsps',
        'gcpsm',
        'azurekv',
        'kubernetes',
        'doppler',
        'onepassword',
      ]);
    });

    test('findSecretStoreProvider("vault") returns the vault entry', () {
      final p = findSecretStoreProvider('vault');
      expect(p, isNotNull);
      expect(p!.id, 'vault');
      expect(p.label, isNotEmpty);
    });

    test('findSecretStoreProvider("nonexistent") returns null', () {
      expect(findSecretStoreProvider('nonexistent'), isNull);
    });

    test('findSecretStoreProvider finds every registered id', () {
      for (final p in kSecretStoreProviders) {
        expect(findSecretStoreProvider(p.id), isNotNull,
            reason: '${p.id} must be findable');
      }
    });
  });

  group('ProviderPicker widget', () {
    testWidgets('renders all 8 provider tiles with label and description',
        (tester) async {
      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: '',
        onChanged: (_) {},
      )));
      await tester.pumpAndSettle();

      for (final p in kSecretStoreProviders) {
        // Label must be unique per provider tile.
        expect(find.text(p.label), findsOneWidget,
            reason: '${p.label} tile must be visible');
        // Descriptions may be shared (aws and awsps share the same description
        // string), so use findsAtLeastNWidgets(1) rather than findsOneWidget.
        expect(find.text(p.description), findsAtLeastNWidgets(1),
            reason: 'description for ${p.id} must be visible');
      }
    });

    testWidgets('tapping a tile fires onChanged with the correct provider id',
        (tester) async {
      String? picked;

      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: '',
        onChanged: (id) => picked = id,
      )));
      await tester.pumpAndSettle();

      // Tap the AWS Secrets Manager tile.
      await tester.tap(find.text('AWS Secrets Manager'));
      await tester.pumpAndSettle();

      expect(picked, 'aws');
    });

    testWidgets('tapping vault fires onChanged with vault id', (tester) async {
      String? picked;

      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: '',
        onChanged: (id) => picked = id,
      )));
      await tester.pumpAndSettle();

      await tester.tap(find.text('HashiCorp Vault'));
      await tester.pumpAndSettle();

      expect(picked, 'vault');
    });

    testWidgets('selected tile renders radio_button_checked icon; others unchecked',
        (tester) async {
      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: 'gcpsm',
        onChanged: (_) {},
      )));
      await tester.pumpAndSettle();

      // One checked icon for the selected provider.
      expect(find.byIcon(Icons.radio_button_checked), findsOneWidget);
      // Seven unchecked icons for the rest.
      expect(find.byIcon(Icons.radio_button_unchecked),
          findsNWidgets(kSecretStoreProviders.length - 1));
    });

    testWidgets('errorMessage is shown when provided', (tester) async {
      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: '',
        onChanged: (_) {},
        errorMessage: 'Pick a provider before continuing',
      )));
      await tester.pumpAndSettle();

      expect(find.textContaining('Pick a provider before continuing'),
          findsOneWidget);
    });

    testWidgets('no errorMessage shown when null', (tester) async {
      await tester.pumpWidget(_wrap(ProviderPicker(
        selected: '',
        onChanged: (_) {},
      )));
      await tester.pumpAndSettle();

      expect(find.textContaining('Pick a provider before continuing'),
          findsNothing);
    });
  });
}
