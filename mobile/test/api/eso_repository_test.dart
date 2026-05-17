// EsoRepository tests: endpoint paths, envelope unwrapping, X-Cluster-ID
// forwarding, drift-vs-lastObservedDriftStatus asymmetry, store metrics
// null semantics, and error surfacing.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/eso_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';

import '../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
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
  group('EsoRepository.status', () {
    test('parses detected status with namespace + version', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/status',
        body: {
          'data': {
            'detected': true,
            'namespace': 'external-secrets',
            'version': '0.14.0',
            'lastChecked': '2026-05-12T10:00:00Z',
          },
        },
      );

      final s = await container.read(esoRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.namespace, 'external-secrets');
      expect(s.version, '0.14.0');
    });

    test('503 from status returns EsoDiscoveryStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'discovery offline'},
        }, status: 503),
      );

      final s = await container.read(esoRepositoryProvider).status();
      expect(s.detected, isFalse);
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/status',
        body: {
          'data': {'detected': false},
        },
      );

      await container
          .read(esoRepositoryProvider)
          .status(clusterIdOverride: 'remote-1');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-1');
    });
  });

  group('listExternalSecrets', () {
    test(
      'list endpoint surfaces lastObservedDriftStatus and leaves '
      'driftStatus = notObserved (wire contract)',
      () async {
        final (:container, :mock) = _make();
        addTearDown(container.dispose);

        mock.onJson('GET', '/api/v1/externalsecrets/externalsecrets', body: {
          'data': [
            {
              'name': 'app-token',
              'namespace': 'app',
              'uid': 'es-1',
              'status': 'Synced',
              'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
              'lastObservedDriftStatus': 'InSync',
            },
            {
              'name': 'drifted-token',
              'namespace': 'app',
              'uid': 'es-2',
              'status': 'Drifted',
              'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
              'lastObservedDriftStatus': 'Drifted',
            },
          ],
        });

        final out =
            await container.read(esoRepositoryProvider).listExternalSecrets();
        expect(out, hasLength(2));
        expect(out[0].lastObservedDriftStatus, DriftStatus.inSync);
        expect(out[0].driftStatus, DriftStatus.notObserved);
        expect(out[1].lastObservedDriftStatus, DriftStatus.drifted);
        expect(out[1].driftStatus, DriftStatus.notObserved);
      },
    );

    test('omitted lastObservedDriftStatus → notObserved (no fake Unknown)',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/externalsecrets/externalsecrets', body: {
        'data': [
          {
            'name': 'unobserved',
            'namespace': 'app',
            'uid': 'es-3',
            'status': 'Synced',
            'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
            // lastObservedDriftStatus deliberately omitted — backend
            // poller has never observed this UID.
          },
        ],
      });

      final out =
          await container.read(esoRepositoryProvider).listExternalSecrets();
      expect(out, hasLength(1));
      expect(out[0].lastObservedDriftStatus, DriftStatus.notObserved);
      expect(out[0].effectiveDriftStatus, DriftStatus.notObserved);
    });

    test('namespace filter threads through as query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/externalsecrets/externalsecrets',
          body: <String, Object?>{'data': <Object?>[]});

      await container
          .read(esoRepositoryProvider)
          .listExternalSecrets(namespace: 'kube-system');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.queryParameters['namespace'], 'kube-system');
    });
  });

  group('getExternalSecret', () {
    test(
      'detail endpoint surfaces live driftStatus + driftUnknownReason '
      '(wire contract — opposite of list)',
      () async {
        final (:container, :mock) = _make();
        addTearDown(container.dispose);

        mock.onJson(
          'GET',
          '/api/v1/externalsecrets/externalsecrets/app/app-token',
          body: {
            'data': {
              'name': 'app-token',
              'namespace': 'app',
              'uid': 'es-1',
              'status': 'Synced',
              'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
              'driftStatus': 'Unknown',
              'driftUnknownReason': 'no_synced_rv',
              // detail endpoint omits lastObservedDriftStatus per
              // backend wire contract.
            },
          },
        );

        final es =
            await container.read(esoRepositoryProvider).getExternalSecret(
                  namespace: 'app',
                  name: 'app-token',
                );

        expect(es.driftStatus, DriftStatus.unknown);
        expect(es.driftUnknownReason, DriftUnknownReason.noSyncedRv);
        expect(es.lastObservedDriftStatus, DriftStatus.notObserved);
      },
    );

    test('404 surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/missing',
        (_) => _json({
          'error': {'code': 404, 'message': 'external secret not found'},
        }, status: 404),
      );

      await expectLater(
        container.read(esoRepositoryProvider).getExternalSecret(
              namespace: 'app',
              name: 'missing',
            ),
        throwsA(isA<ApiError>()),
      );
    });
  });

  group('listClusterExternalSecrets', () {
    test('parses namespace lists', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET',
          '/api/v1/externalsecrets/clusterexternalsecrets', body: {
        'data': [
          {
            'name': 'all-app-tokens',
            'uid': 'ces-1',
            'status': 'Synced',
            'storeRef': {'name': 'vault', 'kind': 'ClusterSecretStore'},
            'namespaceSelectors': ['team=app'],
            'namespaces': ['app-1', 'app-2'],
            'provisionedNamespaces': ['app-1'],
            'failedNamespaces': ['app-2'],
          }
        ],
      });

      final out = await container
          .read(esoRepositoryProvider)
          .listClusterExternalSecrets();
      expect(out, hasLength(1));
      expect(out[0].namespaceSelectors, ['team=app']);
      expect(out[0].provisionedNamespaces, ['app-1']);
      expect(out[0].failedNamespaces, ['app-2']);
    });
  });

  group('getStore', () {
    test('parses providerSpec verbatim', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/app/vault-store',
        body: {
          'data': {
            'name': 'vault-store',
            'namespace': 'app',
            'uid': 's-1',
            'scope': 'Namespaced',
            'status': 'Synced',
            'ready': true,
            'provider': 'vault',
            'providerSpec': {
              'server': 'https://vault.example.com',
              'path': 'secret/app',
              'auth': {'kubernetes': {'role': 'app'}},
            },
          },
        },
      );

      final s = await container
          .read(esoRepositoryProvider)
          .getStore(namespace: 'app', name: 'vault-store');
      expect(s.provider, 'vault');
      expect(s.providerSpec['server'], 'https://vault.example.com');
      expect(s.providerSpec['auth'], isA<Map<String, dynamic>>());
    });
  });

  group('getStoreMetrics', () {
    test('null ratePerMin parses as null (not zero)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/app/vault-store/metrics',
        body: {
          'data': {
            'ratePerMin': null,
            'last24h': null,
            'windowEnd': '2026-05-12T10:00:00Z',
          },
        },
      );

      final m = await container
          .read(esoRepositoryProvider)
          .getStoreMetrics(namespace: 'app', name: 'vault-store');
      expect(m.ratePerMin, isNull);
      expect(m.last24h, isNull);
      expect(m.isDegraded, isFalse);
    });

    test('error field surfaces and isDegraded fires', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/app/vault-store/metrics',
        body: {
          'data': {
            'ratePerMin': null,
            'last24h': null,
            'error': 'rate metrics offline',
          },
        },
      );

      final m = await container
          .read(esoRepositoryProvider)
          .getStoreMetrics(namespace: 'app', name: 'vault-store');
      expect(m.error, 'rate metrics offline');
      expect(m.isDegraded, isTrue);
    });

    test('cost block parses when populated; nil when omitted', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/app/aws-store/metrics',
        body: {
          'data': {
            'ratePerMin': 12.5,
            'last24h': 18000.0,
            'cost': {
              'billingProvider': 'aws-secrets-manager',
              'currency': 'USD',
              'usdPerMillion': 0.05,
              'estimated24h': 0.9,
              'lastUpdated': '2026-04-30T00:00:00Z',
            },
            'windowEnd': '2026-05-12T10:00:00Z',
          },
        },
      );

      final m = await container
          .read(esoRepositoryProvider)
          .getStoreMetrics(namespace: 'app', name: 'aws-store');
      expect(m.cost, isNotNull);
      expect(m.cost!.billingProvider, 'aws-secrets-manager');
      expect(m.cost!.estimated24h, 0.9);
    });
  });

  group('cluster store metrics', () {
    test('forwards X-Cluster-ID + parses metrics', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores/global-vault/metrics',
        body: {
          'data': {
            'ratePerMin': 1.5,
            'last24h': 2160.0,
            'windowEnd': '2026-05-12T10:00:00Z',
          },
        },
      );

      final m = await container
          .read(esoRepositoryProvider)
          .getClusterStoreMetrics(name: 'global-vault', clusterIdOverride: 'r1');
      expect(m.ratePerMin, 1.5);
      expect(mock.requests.first.headers['X-Cluster-ID'], 'r1');
    });
  });

  group('pushsecrets', () {
    test('parses storeRefs list', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/externalsecrets/pushsecrets', body: {
        'data': [
          {
            'name': 'export',
            'namespace': 'app',
            'uid': 'ps-1',
            'status': 'Synced',
            'storeRefs': [
              {'name': 'aws', 'kind': 'SecretStore'},
              {'name': 'gcp', 'kind': 'ClusterSecretStore'},
            ],
            'sourceSecretName': 'real-secret',
          },
        ],
      });

      final out =
          await container.read(esoRepositoryProvider).listPushSecrets();
      expect(out, hasLength(1));
      expect(out[0].storeRefs, hasLength(2));
      expect(out[0].storeRefs[1].kind, 'ClusterSecretStore');
    });

    test('getPushSecret fetches detail endpoint and parses storeRefs',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/pushsecrets/app/export',
        body: {
          'data': {
            'name': 'export',
            'namespace': 'app',
            'uid': 'ps-1',
            'status': 'Synced',
            'storeRefs': [
              {'name': 'aws', 'kind': 'SecretStore'},
            ],
            'sourceSecretName': 'real-secret',
            'lastSyncTime': '2026-05-12T10:00:00Z',
          },
        },
      );

      final ps = await container
          .read(esoRepositoryProvider)
          .getPushSecret(namespace: 'app', name: 'export');
      expect(ps.name, 'export');
      expect(ps.storeRefs, hasLength(1));
      expect(ps.lastSyncTime, '2026-05-12T10:00:00Z');
    });

    test('getPushSecret surfaces 404 as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/pushsecrets/app/missing',
        (_) => _json({
          'error': {'code': 404, 'message': 'not found'},
        }, status: 404),
      );

      await expectLater(
        container
            .read(esoRepositoryProvider)
            .getPushSecret(namespace: 'app', name: 'missing'),
        throwsA(isA<ApiError>()),
      );
    });
  });

  // PR-4h-review #12: getClusterStore + listClusterStores had no
  // repository tests at all. These cover the endpoint paths, happy-path
  // parsing, and 404 surfacing so a future _fetchList refactor that
  // drops the cluster scope is caught at the test layer.
  group('cluster-scoped store endpoints', () {
    test('listClusterStores hits /clusterstores and parses items',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores',
        body: {
          'data': [
            {
              'name': 'global-vault',
              'namespace': '',
              'uid': 'cs-1',
              'scope': 'Cluster',
              'status': 'Ready',
              'ready': true,
              'provider': 'vault',
            },
          ],
        },
      );

      final stores =
          await container.read(esoRepositoryProvider).listClusterStores();
      expect(stores, hasLength(1));
      expect(stores[0].name, 'global-vault');
      expect(stores[0].isCluster, isTrue,
          reason: 'isCluster is derived from scope=="Cluster".');
    });

    test('getClusterStore parses by name and surfaces 404', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores/global-vault',
        body: {
          'data': {
            'name': 'global-vault',
            'namespace': '',
            'uid': 'cs-1',
            'scope': 'Cluster',
            'status': 'Ready',
            'ready': true,
            'provider': 'vault',
          },
        },
      );

      final store = await container
          .read(esoRepositoryProvider)
          .getClusterStore(name: 'global-vault');
      expect(store.name, 'global-vault');
      expect(store.isCluster, isTrue);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/clusterstores/missing',
        (_) => _json({
          'error': {'code': 404, 'message': 'not found'},
        }, status: 404),
      );
      await expectLater(
        container
            .read(esoRepositoryProvider)
            .getClusterStore(name: 'missing'),
        throwsA(isA<ApiError>()),
      );
    });
  });

  // PR-4h-review #22: list-path error mapping coverage. The interceptor
  // stack normalises Dio errors to ApiError; this pins that contract for
  // 401 + 403 paths so a list refactor can't drop the mapping silently.
  group('_fetchList error mapping', () {
    test('listExternalSecrets surfaces 401 as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/externalsecrets',
        (_) => _json({
          'error': {'code': 401, 'message': 'auth expired'},
        }, status: 401),
      );

      await expectLater(
        container.read(esoRepositoryProvider).listExternalSecrets(),
        throwsA(isA<ApiError>()),
      );
    });

    test('listStores surfaces 403 as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/externalsecrets/stores',
        (_) => _json({
          'error': {'code': 403, 'message': 'forbidden'},
        }, status: 403),
      );

      await expectLater(
        container.read(esoRepositoryProvider).listStores(),
        throwsA(isA<ApiError>()),
      );
    });

    // PR-5e-review #11: bulk-refresh scope picker now requests a
    // namespace-scoped store list rather than fetching cluster-wide and
    // filtering client-side. Confirm the repo passes the `?namespace=`
    // query param through to the backend.
    test('listStores threads namespace through as ?namespace= query',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores',
        body: <String, Object?>{'data': <Object?>[]},
      );

      await container
          .read(esoRepositoryProvider)
          .listStores(namespace: 'prod');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.queryParameters['namespace'], 'prod');
    });
  });

  // PR-4h-review #23: cluster-pinning header was previously tested only
  // on `status` + `clusterStoreMetrics`. These pin the X-Cluster-ID
  // forwarding on the list + detail paths so a `_fetchList` / `_getOne`
  // refactor that drops the header is caught.
  group('X-Cluster-ID header forwarding', () {
    test('listExternalSecrets forwards clusterIdOverride', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/externalsecrets/externalsecrets',
          body: {'data': <Map<String, dynamic>>[]});

      await container
          .read(esoRepositoryProvider)
          .listExternalSecrets(clusterIdOverride: 'remote-2');

      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-2');
    });

    test('getExternalSecret forwards clusterIdOverride', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/tok',
        body: {
          'data': {
            'name': 'tok',
            'namespace': 'app',
            'uid': 'es-1',
            'status': 'Synced',
            'storeRef': {'name': 'v', 'kind': 'SecretStore'},
            'driftStatus': 'InSync',
          },
        },
      );

      await container.read(esoRepositoryProvider).getExternalSecret(
            namespace: 'app',
            name: 'tok',
            clusterIdOverride: 'remote-3',
          );

      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-3');
    });
  });

  // PR-4h-review #25: defend the empty-string → notObserved parser path.
  // A provider that emits driftStatus: "" (or omits the field, or sends
  // an unknown enum) must collapse to notObserved so the DriftPill renders
  // nothing rather than crashing or defaulting to "red".
  group('drift parser fallbacks', () {
    test('empty driftStatus string parses to notObserved', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/edge',
        body: {
          'data': {
            'name': 'edge',
            'namespace': 'app',
            'uid': 'es-1',
            'status': 'Synced',
            'storeRef': {'name': 'v', 'kind': 'SecretStore'},
            'driftStatus': '',
          },
        },
      );

      final es = await container
          .read(esoRepositoryProvider)
          .getExternalSecret(namespace: 'app', name: 'edge');
      expect(es.driftStatus, DriftStatus.notObserved);
      expect(es.effectiveDriftStatus, DriftStatus.notObserved);
    });

    test('unknown driftStatus string falls back to notObserved', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/edge',
        body: {
          'data': {
            'name': 'edge',
            'namespace': 'app',
            'uid': 'es-1',
            'status': 'Synced',
            'storeRef': {'name': 'v', 'kind': 'SecretStore'},
            'driftStatus': 'TotallyMadeUp',
          },
        },
      );

      final es = await container
          .read(esoRepositoryProvider)
          .getExternalSecret(namespace: 'app', name: 'edge');
      expect(es.driftStatus, DriftStatus.notObserved);
    });
  });

  // PR-5e-review #4: BulkRefreshAction's open-enum parser used to throw
  // ArgumentError on unknown wire values, propagating as an unhandled
  // exception (Sentry crash) when the backend introduces a new variant
  // before mobile catches up. The fallback now returns
  // BulkRefreshAction.unknown — same pattern as every other open-enum
  // parser in this file (EsoStatus, DriftStatus, EsoThresholdSource).
  group('BulkScopeResponse open-enum parsing', () {
    test('unknown action wire value collapses to BulkRefreshAction.unknown',
        () {
      final resp = BulkScopeResponse.fromJson({
        'action': 'refresh_push_secret', // not a recognized mobile variant
        'scopeTarget': 'prod/vault',
        'totalCount': 1,
        'totalNamespaces': 1,
        'visibleCount': 1,
        'restricted': false,
        'targets': [
          {'namespace': 'prod', 'name': 'es1', 'uid': 'u1'},
        ],
        'byNamespace': [
          {'namespace': 'prod', 'count': 1},
        ],
      });
      expect(resp.action, BulkRefreshAction.unknown,
          reason: 'unknown wire enum must collapse to the sentinel — '
              'throwing here would propagate as a Sentry crash');
    });

    test('recognized wire values still parse correctly', () {
      final resp = BulkScopeResponse.fromJson({
        'action': 'refresh_store',
        'scopeTarget': 'prod/vault',
        'totalCount': 0,
        'totalNamespaces': 0,
        'visibleCount': 0,
        'restricted': false,
        'targets': <Object>[],
        'byNamespace': <Object>[],
      });
      expect(resp.action, BulkRefreshAction.store);
    });
  });
}
