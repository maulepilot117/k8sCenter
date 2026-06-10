// Widget tests for MetricsTab — status gate routing, panel grid render
// for the happy path, and the "no curated panels for this kind" state.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/observability/metrics/metrics_tab.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart' show buildKubeTheme;
import 'package:kubecenter/widgets/feature_unavailable_state.dart';

import '../../../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

Future<void> _pumpTab(
  WidgetTester tester, {
  required MockDioAdapter mock,
  required String kind,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendUrlProvider.overrideWithValue('http://test'),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        // Replace the Dio adapters with the mock under test before
        // any provider that depends on dioProvider materializes.
        dioProvider.overrideWith((ref) {
          final dio = Dio(BaseOptions(baseUrl: 'http://test'));
          dio.httpClientAdapter = mock;
          return dio;
        }),
        refreshDioProvider.overrideWith((ref) {
          final dio = Dio(BaseOptions(baseUrl: 'http://test'));
          dio.httpClientAdapter = mock;
          return dio;
        }),
      ],
      child: MaterialApp(
        theme: buildKubeTheme('liquid-glass'),
        home: Scaffold(
          body: MetricsTab(
            clusterId: 'local',
            kind: kind,
            namespace: 'default',
            name: 'web-pod',
          ),
        ),
      ),
    ),
  );
}

void main() {
  group('MetricsTab status gate', () {
    testWidgets('Prometheus not detected → FeatureUnavailableState.monitoring',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'detected': false,
            'prometheus': {'available': false},
            'grafana': {'available': false},
          },
        },
      );

      await _pumpTab(tester, mock: mock, kind: 'pods');
      // Pump until the status future resolves.
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      // The feature-unavailable card carries the canonical wording.
      expect(find.byType(FeatureUnavailableState), findsOneWidget);
      expect(find.textContaining('Prometheus monitoring'), findsOneWidget);
    });

    testWidgets('detected + supported kind renders the panel grid',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'detected': true,
            'prometheus': {'available': true},
          },
        },
      );
      // F#4 — every panel call hits the slug endpoint. Arm one handler per
      // pod slug; each returns an empty matrix so panels land in
      // PanelLoaded(isEmpty) — the "No data" branch.
      for (final slug in const [
        'pods/cpu',
        'pods/memory',
        'pods/network-rx',
        'pods/network-tx',
      ]) {
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) => _json({
            'data': {'resultType': 'matrix', 'result': <Object>[]},
          }),
        );
      }

      await _pumpTab(tester, mock: mock, kind: 'pods');
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 100));

      // The TimeRangePicker's segment labels are unambiguous evidence
      // the body widget rendered.
      expect(find.text('1h'), findsOneWidget);
      expect(find.text('6h'), findsOneWidget);
      // First two pod-kind panel titles render in the visible viewport;
      // the remaining two live below the fold (ListView.builder lazy).
      // Titles updated when panels moved to slug-based references in F#4.
      expect(find.text('CPU usage'), findsOneWidget);
      expect(find.text('Memory working set'), findsOneWidget);
      // The empty-vector mock should route each panel through the
      // _renderResult `result.isEmpty` branch, surfacing the "No data"
      // banner. Without this assertion the empty-state copy could
      // regress silently when _renderResult is refactored.
      expect(find.text('No data for this time range'), findsWidgets);
    });

    testWidgets('kind with no panels renders the "no curated metrics" state',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'detected': true,
            'prometheus': {'available': true},
          },
        },
      );

      await _pumpTab(tester, mock: mock, kind: 'configmaps');
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(
        find.text('No curated metrics for this resource kind'),
        findsOneWidget,
      );
    });
  });
}
