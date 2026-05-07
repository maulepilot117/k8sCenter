// Verifies cluster list fetch + degraded-mode fallback + error
// differentiation. The repository must distinguish true network failure
// (degrade to local-only) from authoritative HTTP errors (propagate as
// ApiError so the picker can surface the real cause).

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_repository.dart';

import '../support/mock_dio_adapter.dart';

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
  test('list returns clusters from /v1/clusters', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/clusters',
      body: {
        'data': [
          {
            'id': 'local',
            'name': 'local',
            'isLocal': true,
            'status': 'connected',
          },
          {
            'id': 'prod',
            'name': 'prod',
            'displayName': 'Production',
            'isLocal': false,
            'status': 'ready',
            'k8sVersion': '1.31.2',
          },
        ],
      },
    );

    final result = await container.read(clusterRepositoryProvider).list();
    expect(result.degraded, isFalse);
    expect(result.clusters, hasLength(2));
    expect(result.clusters[0].id, 'local');
    expect(result.clusters[1].label, 'Production');
    expect(result.clusters[1].k8sVersion, '1.31.2');
  });

  test('list inserts implicit local when backend omits it', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/clusters',
      body: {
        'data': [
          {'id': 'prod', 'name': 'prod', 'isLocal': false, 'status': 'ready'},
        ],
      },
    );

    final result = await container.read(clusterRepositoryProvider).list();
    expect(result.degraded, isFalse);
    expect(result.clusters.first.id, 'local');
    expect(result.clusters, hasLength(2));
  });

  test('list degrades to local-only on connection error', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/clusters', (req) {
      throw DioException.connectionError(
        requestOptions: req,
        reason: 'Failed host lookup',
      );
    });

    final result = await container.read(clusterRepositoryProvider).list();
    expect(result.degraded, isTrue);
    expect(result.clusters, hasLength(1));
    expect(result.clusters.first.id, 'local');
  });

  test('list propagates HTTP 5xx as ApiError (not silent degrade)', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/clusters', (_) {
      return _json(
        {
          'error': {'code': 503, 'message': 'service unavailable'},
        },
        status: 503,
      );
    });

    expect(
      () => container.read(clusterRepositoryProvider).list(),
      throwsA(isA<ApiError>()
          .having((e) => e.statusCode, 'statusCode', 503)),
    );
  });

  test('list propagates HTTP 401 as ApiError (auth failure visible)',
      () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/clusters', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'unauthorized'},
        },
        status: 401,
      );
    });

    expect(
      () => container.read(clusterRepositoryProvider).list(),
      throwsA(isA<ApiError>()),
    );
  });
}
