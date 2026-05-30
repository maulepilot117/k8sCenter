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

  test(
      'AuthInterceptor: transient refresh failure (connectionTimeout, '
      'no response) keeps the refresh token', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('stale-access');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('valid-refresh');

    mock.on('GET', '/api/v1/protected', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'expired'},
        },
        status: 401,
      );
    });
    // A momentary connectivity blip: the refresh POST never reaches a
    // server, so the DioException carries no response. The destructive
    // delete/clear branch must NOT fire — the server-side-valid refresh
    // token has to survive for the next retry.
    mock.on('POST', '/api/v1/auth/refresh', (req) {
      throw DioException(
        requestOptions: req,
        type: DioExceptionType.connectionTimeout,
      );
    });

    await expectLater(
      container.read(dioProvider).get<dynamic>('/api/v1/protected'),
      throwsA(isA<DioException>()),
    );

    // Contrast with the '401 clears tokens' test above: a transient
    // failure must leave both the access holder and the stored refresh
    // token intact.
    expect(container.read(authTokenHolderProvider).accessToken, 'stale-access');
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      'valid-refresh',
    );
  });

  test(
      'AuthInterceptor: transient refresh failure (5xx with response) '
      'keeps the refresh token', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('stale-access');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('valid-refresh');

    mock.on('GET', '/api/v1/protected', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'expired'},
        },
        status: 401,
      );
    });
    // A gateway error: the refresh POST reaches a server that replies
    // 503. Unlike the connectionTimeout case above, this DioException
    // carries a NON-null response (e.response?.statusCode == 503). The
    // _refresh() guard only clears tokens on 401/403, so a 5xx must NOT
    // fire the destructive delete/clear branch — the server-side-valid
    // refresh token has to survive for the next retry. This guards
    // against a future narrowing of the guard (e.g. to `status != null`)
    // that would silently log users out on gateway errors.
    mock.on('POST', '/api/v1/auth/refresh', (_) {
      return _json(
        {
          'error': {'code': 503, 'message': 'upstream unavailable'},
        },
        status: 503,
      );
    });

    await expectLater(
      container.read(dioProvider).get<dynamic>('/api/v1/protected'),
      throwsA(isA<DioException>()),
    );

    // Contrast with the '401 clears tokens' test above: a 5xx is a
    // transient server-side failure and must leave both the access
    // holder and the stored refresh token intact.
    expect(container.read(authTokenHolderProvider).accessToken, 'stale-access');
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      'valid-refresh',
    );
  });

  // Issue #275 — P1 regression test. N concurrent requests that 401 must
  // share a single /v1/auth/refresh call, not fire N independent refreshes.
  // Without singleflight, every concurrent request's 401 handler races to
  // refresh; the first wins (rotates the refresh token), the rest see
  // "refresh token not found" and the user is logged out across N-1 tabs.
  //
  // PR-5b's 1h OIDC refresh TTL cap turns this from a once-a-week edge
  // case into an hourly event for OIDC users with multiple open tabs.
  test(
      'AuthInterceptor: N concurrent 401s share one /v1/auth/refresh '
      '(issue #275 singleflight regression)', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('stale-access');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('valid-refresh');

    // Protected endpoint mock: 401 with stale, 200 with fresh.
    mock.on('GET', '/api/v1/protected', (req) {
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

    // Refresh endpoint mock: count calls. MockDioAdapter handlers are
    // sync, so we can't inject a delay — but the singleflight check in
    // _attemptRefresh fires before any awaits, so concurrent onError
    // handlers reliably observe the in-flight Completer.
    var refreshHits = 0;
    mock.on('POST', '/api/v1/auth/refresh', (_) {
      refreshHits++;
      return _json({
        'data': {
          'accessToken': 'fresh-access',
          'refreshToken': 'fresh-refresh',
          'expiresIn': 900,
        },
      });
    });

    // Fire 8 concurrent GETs. Each will hit /protected with the stale
    // token, get 401, kick onError → _attemptRefresh. Singleflight
    // requires exactly one refresh HTTP call to fire across all 8.
    const concurrentRequests = 8;
    final results = await Future.wait(
      List.generate(
        concurrentRequests,
        (_) => container.read(dioProvider).get<dynamic>('/api/v1/protected'),
      ),
    );

    for (var i = 0; i < concurrentRequests; i++) {
      expect(
        results[i].statusCode,
        200,
        reason: 'request $i should have succeeded after singleflight refresh',
      );
    }
    expect(
      refreshHits,
      1,
      reason: '$concurrentRequests concurrent 401s must collapse to ONE '
          '/v1/auth/refresh call (issue #275). Got $refreshHits refresh '
          'calls — singleflight is broken.',
    );
    expect(
      container.read(authTokenHolderProvider).accessToken,
      'fresh-access',
    );
  });

  // Issue #275 follow-up: after a refresh completes, the singleflight
  // slot must reset so a SUBSEQUENT 401 (later in the session, e.g. when
  // the next access token expires) can trigger a fresh refresh cycle.
  // Without the finally-clear, the second refresh would deadlock on a
  // stale completed Completer.
  test(
      'AuthInterceptor: singleflight slot resets after refresh completes',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('access-1');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('refresh-1');

    // Two distinct refresh windows: first call rotates access-1 →
    // access-2; second call (later) rotates access-2 → access-3.
    var refreshHits = 0;
    mock.on('POST', '/api/v1/auth/refresh', (_) {
      refreshHits++;
      return _json({
        'data': {
          'accessToken': 'access-${refreshHits + 1}',
          'refreshToken': 'refresh-${refreshHits + 1}',
          'expiresIn': 900,
        },
      });
    });

    // /protected accepts access-2 and access-3; rejects access-1.
    mock.on('GET', '/api/v1/protected', (req) {
      final auth = req.headers['Authorization'] as String?;
      if (auth == 'Bearer access-1') {
        return _json(
          {
            'error': {'code': 401, 'message': 'expired'},
          },
          status: 401,
        );
      }
      return _json({'data': 'ok'});
    });

    // First request triggers refresh 1.
    final r1 = await container.read(dioProvider).get<dynamic>('/api/v1/protected');
    expect(r1.statusCode, 200);
    expect(refreshHits, 1);
    expect(container.read(authTokenHolderProvider).accessToken, 'access-2');

    // Simulate access-2 expiring later in the session by forcing it
    // back to access-1 (would-be expired token). A new 401 must
    // trigger a SECOND refresh, not block on a stale completed slot.
    container.read(authTokenHolderProvider).set('access-1');

    final r2 = await container.read(dioProvider).get<dynamic>('/api/v1/protected');
    expect(r2.statusCode, 200);
    expect(
      refreshHits,
      2,
      reason: 'second 401 must trigger a fresh refresh cycle '
          '(singleflight slot must reset after completion)',
    );
    expect(container.read(authTokenHolderProvider).accessToken, 'access-3');
  });
}
