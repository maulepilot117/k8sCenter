// Widget tests for the R-8 remote-failure rendering on the diagnostics
// screens. A 501 `unsupported_platform` (remote cluster) must render the
// remote-only state with no Retry; a 502 `unreachable` keeps Retry.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/observability/diagnostics/diagnostics_screen.dart';
import 'package:kubecenter/features/observability/diagnostics/namespace_summary_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../../support/mock_dio_adapter.dart';

ResponseBody _error(int status, String reason) => ResponseBody.fromBytes(
      Uint8List.fromList(utf8.encode(jsonEncode({
        'error': {
          'code': status,
          'message': 'raw backend text',
          'reason': reason,
        },
      }))),
      status,
      headers: {
        Headers.contentTypeHeader: ['application/json'],
      },
    );

Future<void> _pump(
  WidgetTester tester,
  Widget screen,
  String path,
  int status,
  String reason,
) async {
  final mock = MockDioAdapter()..on('GET', path, (_) => _error(status, reason));
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  addTearDown(container.dispose);
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp(theme: buildKubeTheme('liquid-glass'), home: screen),
  ));
  for (var i = 0; i < 5; i++) {
    await tester.pump(const Duration(milliseconds: 50));
  }
}

const _detailPath = '/api/v1/diagnostics/default/Deployment/web';
const _summaryPath = '/api/v1/diagnostics/default/summary';
const _detail =
    DiagnosticsScreen(namespace: 'default', kind: 'Deployment', name: 'web');
const _summary = NamespaceSummaryScreen(namespace: 'default');

void main() {
  group('DiagnosticsScreen', () {
    testWidgets('501 unsupported_platform: remote-only state, no Retry',
        (tester) async {
      await _pump(tester, _detail, _detailPath, 501, 'unsupported_platform');
      expect(find.text('Not available for remote clusters'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('502 unreachable keeps Retry', (tester) async {
      await _pump(tester, _detail, _detailPath, 502, 'unreachable');
      expect(find.text('Cluster unreachable'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });
  });

  group('NamespaceSummaryScreen', () {
    testWidgets('501 unsupported_platform: remote-only state, no Retry',
        (tester) async {
      await _pump(tester, _summary, _summaryPath, 501, 'unsupported_platform');
      expect(find.text('Not available for remote clusters'), findsOneWidget);
      expect(find.text('Retry'), findsNothing);
    });

    testWidgets('502 unreachable keeps Retry', (tester) async {
      await _pump(tester, _summary, _summaryPath, 502, 'unreachable');
      expect(find.text('Cluster unreachable'), findsOneWidget);
      expect(find.text('Retry'), findsOneWidget);
    });
  });
}
