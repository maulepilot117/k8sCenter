// Widget tests for [OIDCProviderButton]. Verifies the three render
// modes (default / loading / disabled) the login screen drives via the
// OIDC controller state.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/oidc_widgets.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

const _authelia = AuthProvider(
  id: 'authelia',
  name: 'Corp Authelia',
  kind: 'oidc',
);

Widget _harness({
  required Widget child,
}) {
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: Scaffold(body: Center(child: child)),
  );
}

void main() {
  testWidgets('default render: label visible, button enabled', (tester) async {
    var tapped = 0;
    await tester.pumpWidget(_harness(
      child: OIDCProviderButton(
        provider: _authelia,
        onTap: () => tapped++,
      ),
    ));

    expect(find.text('Sign in with Corp Authelia'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);

    await tester.tap(find.byKey(const ValueKey('login-oidc-authelia')));
    await tester.pump();
    expect(tapped, 1);
  });

  testWidgets('isLoading: spinner replaces label, taps suppressed',
      (tester) async {
    var tapped = 0;
    await tester.pumpWidget(_harness(
      child: OIDCProviderButton(
        provider: _authelia,
        onTap: () => tapped++,
        isLoading: true,
      ),
    ));

    expect(find.text('Sign in with Corp Authelia'), findsNothing);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);

    await tester.tap(
      find.byKey(const ValueKey('login-oidc-authelia')),
      warnIfMissed: false,
    );
    await tester.pump();
    expect(tapped, 0);
  });

  testWidgets('isDisabled: tap is a no-op (onPressed is null)',
      (tester) async {
    var tapped = 0;
    await tester.pumpWidget(_harness(
      child: OIDCProviderButton(
        provider: _authelia,
        onTap: () => tapped++,
        isDisabled: true,
      ),
    ));

    final button = tester.widget<FilledButton>(find.byType(FilledButton));
    expect(button.onPressed, isNull);

    await tester.tap(
      find.byKey(const ValueKey('login-oidc-authelia')),
      warnIfMissed: false,
    );
    await tester.pump();
    expect(tapped, 0);
  });
}
