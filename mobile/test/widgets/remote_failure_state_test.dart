// Widget tests for the shared R-8 remote-failure states.
//
// The contract under test: a state that retrying cannot fix (the view is
// not available for remote clusters, the feature is not installed, the
// stored credentials were rejected) renders no Retry; a transient one
// (cluster unreachable, registry unavailable) keeps Retry.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/empty_states.dart';
import 'package:kubecenter/widgets/feature_unavailable_state.dart';
import 'package:kubecenter/widgets/remote_failure_state.dart';

ApiError _err(int status, String? reason, [String message = 'raw message']) =>
    ApiError(statusCode: status, code: status, message: message, reason: reason);

Widget _wrap(Widget child) => MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Scaffold(body: child),
    );

void main() {
  group('ApiErrorStateView', () {
    testWidgets('unsupported_platform renders the remote-only state with no '
        'Retry', (tester) async {
      var retries = 0;
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(501, 'unsupported_platform'),
        onRetry: () => retries++,
      )));

      expect(find.text('Not available for remote clusters'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('discovery_missing renders not-installed with no Retry',
        (tester) async {
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(404, 'discovery_missing'),
        onRetry: () {},
      )));

      expect(find.text('Not installed on this cluster'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('credentials_invalid renders with no Retry', (tester) async {
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(502, 'credentials_invalid'),
        onRetry: () {},
      )));

      expect(find.text('Credentials no longer valid'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('unreachable keeps Retry and Retry fires', (tester) async {
      var retries = 0;
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(502, 'unreachable'),
        onRetry: () => retries++,
      )));

      expect(find.text('Cluster unreachable'), findsOneWidget);
      await tester.tap(find.text('Retry'));
      expect(retries, 1);
    });

    testWidgets('db_unavailable keeps Retry', (tester) async {
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(503, 'db_unavailable'),
        onRetry: () {},
      )));

      expect(find.text('Cluster registry unavailable'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });

    testWidgets('an error without a reason keeps the generic message and Retry',
        (tester) async {
      await tester.pumpWidget(_wrap(ApiErrorStateView(
        error: _err(500, null, 'Server exploded'),
        onRetry: () {},
      )));

      expect(find.text('Server exploded'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });
  });

  group('ListErrorShell', () {
    testWidgets('unsupported_platform has no Retry', (tester) async {
      await tester.pumpWidget(_wrap(ListErrorShell(
        title: 'Failed to load stores',
        error: _err(501, 'unsupported_platform'),
        onRetry: () async {},
      )));

      expect(find.text('Not available for remote clusters'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('unreachable keeps Retry', (tester) async {
      await tester.pumpWidget(_wrap(ListErrorShell(
        title: 'Failed to load stores',
        error: _err(502, 'unreachable'),
        onRetry: () async {},
      )));

      expect(find.text('Cluster unreachable'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });
  });

  group('FeatureAbsentState (KTD5 status payload)', () {
    testWidgets('no reason renders the feature not-installed card',
        (tester) async {
      await tester.pumpWidget(_wrap(FeatureAbsentState(
        reason: null,
        notInstalled: FeatureUnavailableState.gitops(),
        onRetry: () {},
      )));

      expect(find.text('is not installed on this cluster'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('discovery_missing renders the feature not-installed card',
        (tester) async {
      await tester.pumpWidget(_wrap(FeatureAbsentState(
        reason: 'discovery_missing',
        notInstalled: FeatureUnavailableState.mesh(),
        onRetry: () {},
      )));

      expect(find.text('is not installed on this cluster'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('unreachable is not reported as not installed and keeps Retry',
        (tester) async {
      await tester.pumpWidget(_wrap(FeatureAbsentState(
        reason: 'unreachable',
        notInstalled: FeatureUnavailableState.eso(),
        onRetry: () {},
      )));

      expect(find.text('is not installed on this cluster'), findsNothing);
      expect(find.text('Cluster unreachable'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });
  });
}
