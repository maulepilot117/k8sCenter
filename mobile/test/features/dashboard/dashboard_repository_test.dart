// Dashboard repository: happy path + 403 surfaces ApiError.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/features/dashboard/dashboard_repository.dart';

import '../../support/mock_dio_adapter.dart';

ResponseBody _json(Map<String, dynamic> body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

({ProviderContainer container, MockDioAdapter mock}) _make() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

void main() {
  test('fetchSummary parses the canonical envelope', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/cluster/dashboard-summary',
      body: {
        'data': {
          'nodes': {'total': 5, 'ready': 5},
          'pods': {
            'total': 100,
            'running': 95,
            'pending': 3,
            'failed': 2,
          },
          'services': {'total': 30},
          'alerts': {'active': 1, 'critical': 0},
          'cpu': {
            'percentage': 42.5,
            'used': '4.2 cores',
            'total': '10 cores',
            'requests': '6 cores',
            'limits': '12 cores',
          },
          'memory': null,
        },
      },
    );

    final summary =
        await container.read(dashboardRepositoryProvider).fetchSummary();
    expect(summary.nodes.ready, 5);
    expect(summary.pods.running, 95);
    expect(summary.servicesTotal, 30);
    expect(summary.alerts.active, 1);
    expect(summary.cpu?.percentage, 42.5);
    expect(summary.memory, isNull);
  });

  test('fetchSummary surfaces backend 403 as ApiError', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/cluster/dashboard-summary', (_) {
      return _json(
        {
          'error': {
            'code': 403,
            'message': 'forbidden',
          },
        },
        status: 403,
      );
    });

    expect(
      () => container.read(dashboardRepositoryProvider).fetchSummary(),
      throwsA(isA<ApiError>()
          .having((e) => e.statusCode, 'statusCode', 403)
          .having((e) => e.message, 'message', 'forbidden')),
    );
  });

  test('400 with "local cluster" message becomes DashboardLocalOnlyError',
      () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/cluster/dashboard-summary', (_) {
      return _json(
        {
          'error': {
            'code': 400,
            'message':
                'dashboard summary is only available for the local cluster',
          },
        },
        status: 400,
      );
    });

    expect(
      () => container.read(dashboardRepositoryProvider).fetchSummary(),
      throwsA(isA<DashboardLocalOnlyError>()),
    );
  });

  test('400 without "local cluster" message stays as ApiError', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/cluster/dashboard-summary', (_) {
      return _json(
        {
          'error': {'code': 400, 'message': 'bad request'},
        },
        status: 400,
      );
    });

    expect(
      () => container.read(dashboardRepositoryProvider).fetchSummary(),
      throwsA(isA<ApiError>()),
    );
  });

  test('summary refetches when active cluster changes', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    var fetchCount = 0;
    mock.on('GET', '/api/v1/cluster/dashboard-summary', (_) {
      fetchCount++;
      return _json({
        'data': {
          'nodes': {'total': 1, 'ready': 1},
          'pods': {'total': 0, 'running': 0, 'pending': 0, 'failed': 0},
          'services': {'total': 0},
          'alerts': {'active': 0, 'critical': 0},
        },
      });
    });

    await container.read(dashboardSummaryProvider.future);
    expect(fetchCount, 1);

    container.read(activeClusterProvider.notifier).setCluster('prod');
    // ref.watch(activeClusterProvider) inside dashboardSummaryProvider
    // triggers an invalidation; a fresh read fires the next fetch.
    await container.read(dashboardSummaryProvider.future);
    expect(fetchCount, 2);

    final lastReq = mock.requests.last;
    expect(lastReq.headers['X-Cluster-ID'], 'prod');
  });
}
