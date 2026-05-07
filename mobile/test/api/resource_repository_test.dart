// Resource repository: list + get happy paths and error propagation.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/resource_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';

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
  test('list parses items + total from canonical envelope', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/resources/pods',
      body: {
        'data': [
          {
            'metadata': {'name': 'p1', 'namespace': 'default'},
            'status': {'phase': 'Running'},
          },
          {
            'metadata': {'name': 'p2', 'namespace': 'default'},
            'status': {'phase': 'Pending'},
          },
        ],
        'metadata': {'total': 2},
      },
    );

    final res = await container
        .read(resourceRepositoryProvider)
        .list(kind: 'pods');
    expect(res.items, hasLength(2));
    expect(res.total, 2);
    expect(res.items[0]['metadata']['name'], 'p1');
  });

  test('list with namespace appends to URL', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/resources/pods/kube-system',
      body: {'data': <Object>[], 'metadata': {'total': 0}},
    );

    await container
        .read(resourceRepositoryProvider)
        .list(kind: 'pods', namespace: 'kube-system');

    expect(mock.requests.last.path, '/api/v1/resources/pods/kube-system');
  });

  test('list propagates 403 as ApiError', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/resources/pods', (_) {
      return _json(
        {
          'error': {'code': 403, 'message': 'forbidden'},
        },
        status: 403,
      );
    });

    expect(
      () => container.read(resourceRepositoryProvider).list(kind: 'pods'),
      throwsA(isA<ApiError>()
          .having((e) => e.statusCode, 'statusCode', 403)),
    );
  });

  test('get for namespaced resource', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/resources/pods/default/p1',
      body: {
        'data': {
          'metadata': {'name': 'p1', 'namespace': 'default'},
          'status': {'phase': 'Running'},
        },
      },
    );

    final res = await container.read(resourceRepositoryProvider).get(
          kind: 'pods',
          namespace: 'default',
          name: 'p1',
        );
    expect(res['metadata']['name'], 'p1');
  });

  test('get for cluster-scoped resource omits namespace segment', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/resources/nodes/node-1',
      body: {
        'data': {
          'metadata': {'name': 'node-1'},
        },
      },
    );

    final res = await container.read(resourceRepositoryProvider).get(
          kind: 'nodes',
          namespace: '',
          name: 'node-1',
        );
    expect(res['metadata']['name'], 'node-1');
    expect(mock.requests.last.path, '/api/v1/resources/nodes/node-1');
  });

  test('get propagates 404 as ApiError', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);

    mock.on('GET', '/api/v1/resources/pods/default/missing', (_) {
      return _json(
        {
          'error': {'code': 404, 'message': 'not found'},
        },
        status: 404,
      );
    });

    expect(
      () => container.read(resourceRepositoryProvider).get(
            kind: 'pods',
            namespace: 'default',
            name: 'missing',
          ),
      throwsA(isA<ApiError>()
          .having((e) => e.statusCode, 'statusCode', 404)),
    );
  });
}
