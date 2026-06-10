// Widget tests for VaultProviderForm (the `vaultProviderForm` builder).
//
// Covers:
//   - Default auth method with empty spec is 'token' (_detectMethod first-match).
//   - Tapping Kubernetes chip calls onUpdateSpec with auth: {kubernetes: {}}
//     and no 'token' key surviving — stale auth fields are wiped.
//   - Tapping AppRole after token data was set wipes prior auth; only appRole key present.
//   - Top-level fields (server, path, version, namespace) survive auth-method switch.
//   - Version dropdown defaults to 'v2' when spec.version is unset.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/types/secret_store/providers/provider_form.dart';
import 'package:kubecenter/wizards/types/secret_store/providers/vault_provider_form.dart';

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
  group('VaultProviderForm', () {
    testWidgets('default auth method with empty spec is token', (tester) async {
      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: const {},
          errors: const {},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      // The token auth-method chip/box should be selected by default.
      // The Token auth section renders a "Token Secret reference" box.
      expect(find.textContaining('Token Secret reference'), findsOneWidget);
    });

    testWidgets('tapping Kubernetes chip emits auth: {kubernetes: {}}, removes token key',
        (tester) async {
      Map<String, dynamic>? emitted;

      // Start with a token-auth spec so we can confirm it gets wiped.
      final initialSpec = <String, dynamic>{
        'server': 'https://vault.example.com',
        'auth': {
          'token': {
            'tokenSecretRef': {'name': 'vault-token', 'key': 'token'}
          }
        },
      };

      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: initialSpec,
          errors: const {},
          onUpdateSpec: (s) => emitted = s,
        ),
      )));
      await tester.pumpAndSettle();

      // Tap the 'Kubernetes' auth chip.
      await tester.tap(find.text('Kubernetes'));
      await tester.pumpAndSettle();

      expect(emitted, isNotNull);
      final auth = emitted!['auth'] as Map<String, dynamic>;
      // Only 'kubernetes' key survives.
      expect(auth.containsKey('kubernetes'), isTrue);
      expect(auth.containsKey('token'), isFalse,
          reason: 'Switching auth must wipe stale token fields');
      // Top-level server field is preserved.
      expect(emitted!['server'], 'https://vault.example.com');
    });

    testWidgets('tapping AppRole after token data wipes prior auth', (tester) async {
      Map<String, dynamic>? emitted;

      final initialSpec = <String, dynamic>{
        'auth': {
          'token': {
            'tokenSecretRef': {'name': 'my-token', 'key': 'token'}
          }
        },
      };

      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: initialSpec,
          errors: const {},
          onUpdateSpec: (s) => emitted = s,
        ),
      )));
      await tester.pumpAndSettle();

      await tester.tap(find.text('AppRole'));
      await tester.pumpAndSettle();

      expect(emitted, isNotNull);
      final auth = emitted!['auth'] as Map<String, dynamic>;
      expect(auth.containsKey('appRole'), isTrue);
      expect(auth.containsKey('token'), isFalse,
          reason: 'Prior token auth must not survive an AppRole switch');
    });

    testWidgets('top-level server, path, namespace survive auth-method switch',
        (tester) async {
      Map<String, dynamic>? emitted;

      final initialSpec = <String, dynamic>{
        'server': 'https://vault.example.com:8200',
        'path': 'secret',
        'namespace': 'admin/dev',
        'auth': {
          'token': {
            'tokenSecretRef': {'name': 'tok', 'key': 'token'}
          }
        },
      };

      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: initialSpec,
          errors: const {},
          onUpdateSpec: (s) => emitted = s,
        ),
      )));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Kubernetes'));
      await tester.pumpAndSettle();

      expect(emitted, isNotNull);
      expect(emitted!['server'], 'https://vault.example.com:8200');
      expect(emitted!['path'], 'secret');
      expect(emitted!['namespace'], 'admin/dev');
    });

    testWidgets('version dropdown defaults to v2 when spec.version is unset',
        (tester) async {
      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: const {},
          errors: const {},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      // The _VersionDropdown should render 'v2 (recommended)' as selected.
      expect(find.text('v2 (recommended)'), findsOneWidget);
    });

    testWidgets('errors surface on relevant fields', (tester) async {
      await tester.pumpWidget(_wrap(vaultProviderForm(
        ProviderFormProps(
          spec: const {
            'auth': <String, dynamic>{'kubernetes': <String, dynamic>{}},
          },
          errors: const {
            'auth.kubernetes.role': 'role is required',
            'server': 'server is required',
          },
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      expect(find.textContaining('role is required'), findsOneWidget);
      expect(find.textContaining('server is required'), findsOneWidget);
    });
  });
}
