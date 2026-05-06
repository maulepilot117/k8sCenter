// Verifies the interceptor stack:
//   - ClusterInterceptor injects X-Cluster-ID
//   - CSRFInterceptor adds X-Requested-With on non-GETs only
//   - AuthInterceptor on 401 attempts a refresh, retries the original
//     request once with the new token, and propagates the original 401
//     when refresh itself fails.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/auth_token_holder.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';

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

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
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
  test('ClusterInterceptor injects X-Cluster-ID from active cluster', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(activeClusterProvider.notifier).setCluster('remote-prod');
    mock.onJson('GET', '/api/v1/ping', body: {'data': 'ok'});

    await container.read(dioProvider).get<dynamic>('/api/v1/ping');

    final last = mock.requests.last;
    expect(last.headers['X-Cluster-ID'], 'remote-prod');
  });

  test('CSRFInterceptor sets X-Requested-With on non-GETs only', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson('GET', '/api/v1/get', body: {'data': 'ok'});
    mock.onJson('POST', '/api/v1/post', body: {'data': 'ok'});

    await container.read(dioProvider).get<dynamic>('/api/v1/get');
    await container
        .read(dioProvider)
        .post<dynamic>('/api/v1/post', data: <String, Object>{});

    final getReq = mock.requests[0];
    final postReq = mock.requests[1];
    expect(getReq.headers['X-Requested-With'], isNull);
    expect(postReq.headers['X-Requested-With'], 'XMLHttpRequest');
  });

  test('AuthInterceptor: 401 triggers refresh, retries original request once',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('stale-access');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('valid-refresh');

    // First call returns 401 with the stale token; after refresh, second
    // call returns 200.
    var protectedHits = 0;
    mock.on('GET', '/api/v1/protected', (req) {
      protectedHits++;
      final auth = req.headers['Authorization'] as String?;
      if (auth == 'Bearer stale-access') {
        return _json(
          {
            'error': {'code': 401, 'message': 'expired'},
          },
          status: 401,
        );
      }
      return _json({'data': 'ok'});
    });

    mock.onJson(
      'POST',
      '/api/v1/auth/refresh',
      body: {
        'data': {
          'accessToken': 'fresh-access',
          'refreshToken': 'fresh-refresh',
          'expiresIn': 900,
        },
      },
    );

    final res =
        await container.read(dioProvider).get<dynamic>('/api/v1/protected');
    expect(res.statusCode, 200);
    expect(protectedHits, 2);
    expect(container.read(authTokenHolderProvider).accessToken, 'fresh-access');
  });

  test('AuthInterceptor: refresh failure clears tokens and propagates 401',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('stale-access');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('stale-refresh');

    mock.on('GET', '/api/v1/protected', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'expired'},
        },
        status: 401,
      );
    });
    mock.on('POST', '/api/v1/auth/refresh', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'rotated'},
        },
        status: 401,
      );
    });

    await expectLater(
      container.read(dioProvider).get<dynamic>('/api/v1/protected'),
      throwsA(isA<DioException>()),
    );

    expect(container.read(authTokenHolderProvider).accessToken, isNull);
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      isNull,
    );
  });
}
