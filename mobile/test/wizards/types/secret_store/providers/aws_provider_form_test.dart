// Widget tests for AWSProviderForm (the `awsProviderForm` builder).
//
// Covers:
//   - Default auth method with empty spec is 'jwt' (IRSA — modern cluster pattern).
//   - Switch from jwt to secretRef: emitted spec replaces auth slate, jwt key gone.
//   - The region text field is present and surfaces an errorText when errors['region'] set.
//   - Pre-existing spec with auth.secretRef restores 'secretRef' selection on rebuild.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/types/secret_store/providers/aws_provider_form.dart';
import 'package:kubecenter/wizards/types/secret_store/providers/provider_form.dart';

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
  group('AWSProviderForm', () {
    testWidgets('default auth method with empty spec is jwt (IRSA)', (tester) async {
      await tester.pumpWidget(_wrap(awsProviderForm(
        ProviderFormProps(
          spec: const {},
          errors: const {},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      // The IAM/IRSA auth block should be visible (service account reference section).
      expect(find.textContaining('IAM / IRSA'), findsWidgets);
      // Static credentials block should NOT be visible.
      expect(find.textContaining('Access Key ID Secret reference'), findsNothing);
    });

    testWidgets('switch from jwt to secretRef: emitted auth has only secretRef key',
        (tester) async {
      Map<String, dynamic>? emitted;

      final initialSpec = <String, dynamic>{
        'region': 'us-east-1',
        'auth': {
          'jwt': {
            'serviceAccountRef': {'name': 'my-sa'}
          },
        },
      };

      await tester.pumpWidget(_wrap(awsProviderForm(
        ProviderFormProps(
          spec: initialSpec,
          errors: const {},
          onUpdateSpec: (s) => emitted = s,
        ),
      )));
      await tester.pumpAndSettle();

      // Tap "Static credentials" chip.
      await tester.tap(find.text('Static credentials'));
      await tester.pumpAndSettle();

      expect(emitted, isNotNull);
      final auth = emitted!['auth'] as Map<String, dynamic>;
      expect(auth.containsKey('secretRef'), isTrue);
      expect(auth.containsKey('jwt'), isFalse,
          reason: 'Switching to secretRef must wipe stale jwt.serviceAccountRef');
      // Top-level region field preserved.
      expect(emitted!['region'], 'us-east-1');
    });

    testWidgets('region field is present and surfaces errorText', (tester) async {
      await tester.pumpWidget(_wrap(awsProviderForm(
        ProviderFormProps(
          spec: const {},
          errors: const {'region': 'is required'},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      // The AWS Region label should be visible.
      expect(find.text('AWS Region'), findsOneWidget);
      // The error text should surface.
      expect(find.textContaining('is required'), findsOneWidget);
    });

    testWidgets(
        'pre-existing secretRef spec: _detectMethod restores secretRef selection',
        (tester) async {
      final specWithSecretRef = <String, dynamic>{
        'region': 'us-west-2',
        'auth': {
          'secretRef': {
            'accessKeyIDSecretRef': {'name': 'foo', 'key': 'access-key-id'},
            'secretAccessKeySecretRef': {'name': 'foo', 'key': 'secret-access-key'},
          },
        },
      };

      await tester.pumpWidget(_wrap(awsProviderForm(
        ProviderFormProps(
          spec: specWithSecretRef,
          errors: const {},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      // Static credentials block should be visible (secretRef was detected).
      expect(find.textContaining('Access Key ID Secret reference'), findsOneWidget);
      // IRSA service account section should NOT be visible.
      expect(find.textContaining('IAM / IRSA — service account reference'),
          findsNothing);
    });

    testWidgets('both auth-method chips render', (tester) async {
      await tester.pumpWidget(_wrap(awsProviderForm(
        ProviderFormProps(
          spec: const {},
          errors: const {},
          onUpdateSpec: (_) {},
        ),
      )));
      await tester.pumpAndSettle();

      expect(find.text('IAM / IRSA'), findsOneWidget);
      expect(find.text('Static credentials'), findsOneWidget);
    });
  });
}
