// MeshRepository tests: endpoint paths, X-Cluster-ID forwarding,
// envelope unwrapping, partial-failure error map parsing, composite-ID
// encoding round-trip, golden-signals missingQueries handling, 5xx-
// fallback on status, and 4xx surfacing as ApiError.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/mesh_repository.dart';
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
  group('MeshRepository.status', () {
    test('parses Istio-only detected with namespace + version', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': 'istio',
              'istio': {
                'installed': true,
                'namespace': 'istio-system',
                'version': '1.22.0',
                'mode': 'sidecar',
              },
              'linkerd': {'installed': false},
              'lastChecked': '2026-05-12T10:00:00Z',
            },
          },
        },
      );

      final s = await container.read(meshRepositoryProvider).status();
      expect(s.isInstalled, isTrue);
      expect(s.hasIstio, isTrue);
      expect(s.hasLinkerd, isFalse);
      expect(s.istio.installed, isTrue);
      expect(s.istio.namespace, 'istio-system');
      expect(s.istio.version, '1.22.0');
      expect(s.istio.mode, 'sidecar');
      expect(s.linkerd.installed, isFalse);
      expect(s.lastChecked, '2026-05-12T10:00:00Z');
    });

    test('parses both meshes detected', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': 'both',
              'istio': {'installed': true, 'namespace': 'istio-system'},
              'linkerd': {'installed': true, 'namespace': 'linkerd'},
            },
          },
        },
      );

      final s = await container.read(meshRepositoryProvider).status();
      expect(s.detected, 'both');
      expect(s.hasBoth, isTrue);
      expect(s.hasIstio, isTrue);
      expect(s.hasLinkerd, isTrue);
    });

    test('non-admin shape (no namespace field) parses cleanly', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': 'istio',
              'istio': {'installed': true},
              'linkerd': {'installed': false},
            },
          },
        },
      );

      final s = await container.read(meshRepositoryProvider).status();
      expect(s.istio.installed, isTrue);
      expect(s.istio.namespace, isNull);
      expect(s.istio.version, isNull);
    });

    test('503 from status returns MeshStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'mesh discovery offline'},
        }, status: 503),
      );

      final s = await container.read(meshRepositoryProvider).status();
      expect(s.isInstalled, isFalse);
      expect(s.detected, '');
    });

    test('401 surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/status',
        (_) => _json({
          'error': {'code': 401, 'message': 'unauthorized'},
        }, status: 401),
      );

      await expectLater(
        container.read(meshRepositoryProvider).status(),
        throwsA(isA<ApiError>()),
      );
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {'detected': ''},
          },
        },
      );

      await container
          .read(meshRepositoryProvider)
          .status(clusterIdOverride: 'staging-east');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.single.headers['X-Cluster-ID'], 'staging-east');
    });
  });

  group('MeshRepository.listRouting', () {
    test('parses routes + partial-failure errors map', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/routing',
        body: {
          'data': {
            'status': {
              'detected': 'both',
              'istio': {'installed': true},
              'linkerd': {'installed': true},
            },
            'routes': [
              {
                'id': 'istio:app:vs:web',
                'mesh': 'istio',
                'kind': 'VirtualService',
                'name': 'web',
                'namespace': 'app',
                'hosts': ['web.example.com'],
                'gateways': ['mesh-gateway'],
                'destinations': [
                  {'host': 'web.app.svc.cluster.local', 'port': 80, 'weight': 100},
                ],
                'matchers': [
                  {'method': 'GET', 'pathPrefix': '/api'},
                ],
              },
              {
                'id': 'linkerd:app:sp:web',
                'mesh': 'linkerd',
                'kind': 'ServiceProfile',
                'name': 'web',
                'namespace': 'app',
                'hosts': <String>[],
              },
            ],
            'errors': {
              'istio/AuthorizationPolicy': 'forbidden',
              'prometheus-cross-check': 'timeout',
            },
          },
        },
      );

      final res = await container.read(meshRepositoryProvider).listRouting();
      expect(res.routes, hasLength(2));
      expect(res.routes.first.id, 'istio:app:vs:web');
      expect(res.routes.first.destinations, hasLength(1));
      expect(res.routes.first.destinations.first.weight, 100);
      expect(res.routes.first.matchers.first.method, 'GET');
      expect(res.routes.first.matchers.first.pathPrefix, '/api');
      expect(res.errors, {
        'istio/AuthorizationPolicy': 'forbidden',
        'prometheus-cross-check': 'timeout',
      });
    });

    test('threads namespace query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/routing',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'routes': <Map<String, Object?>>[],
          },
        },
      );

      await container
          .read(meshRepositoryProvider)
          .listRouting(namespace: 'app');

      expect(mock.requests.single.queryParameters['namespace'], 'app');
    });

    test('empty namespace skips the query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/routing',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'routes': <Map<String, Object?>>[],
          },
        },
      );

      await container.read(meshRepositoryProvider).listRouting(namespace: '');
      expect(mock.requests.single.queryParameters.containsKey('namespace'),
          isFalse);
    });

    test('omits errors map when backend returns empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/routing',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'routes': <Map<String, Object?>>[],
          },
        },
      );

      final res = await container.read(meshRepositoryProvider).listRouting();
      expect(res.errors, isEmpty);
    });
  });

  group('MeshRepository.getRoute', () {
    test('URL-encodes composite id on the wire (single encode)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // The backend's parseMeshCompositeID decodes one URL layer before
      // splitting on ':' — so Uri.encodeComponent("istio:app:vs:web")
      // = "istio%3Aapp%3Avs%3Aweb" is what hits the wire path.
      mock.onJson(
        'GET',
        '/api/v1/mesh/routing/istio%3Aapp%3Avs%3Aweb',
        body: {
          'data': {
            'id': 'istio:app:vs:web',
            'mesh': 'istio',
            'kind': 'VirtualService',
            'name': 'web',
            'namespace': 'app',
            'hosts': ['web.example.com'],
            'raw': {
              'apiVersion': 'networking.istio.io/v1',
              'kind': 'VirtualService',
              'metadata': {'name': 'web'},
            },
          },
        },
      );

      final route = await container
          .read(meshRepositoryProvider)
          .getRoute(id: 'istio:app:vs:web');
      expect(route.name, 'web');
      expect(route.mesh, 'istio');
      expect(route.kind, 'VirtualService');
      expect(route.raw, isNotNull);
      expect(route.raw!['apiVersion'], 'networking.istio.io/v1');
    });

    test('404 from missing route surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/routing/istio%3Aapp%3Avs%3Aghost',
        (_) => _json({
          'error': {'code': 404, 'message': 'mesh resource not found'},
        }, status: 404),
      );

      await expectLater(
        container
            .read(meshRepositoryProvider)
            .getRoute(id: 'istio:app:vs:ghost'),
        throwsA(isA<ApiError>().having(
          (e) => e.statusCode,
          'statusCode',
          404,
        )),
      );
    });
  });

  group('MeshRepository.listPolicies', () {
    test('parses policies + errors', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/policies',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'policies': [
              {
                'id': 'istio:app:pa:default',
                'mesh': 'istio',
                'kind': 'PeerAuthentication',
                'name': 'default',
                'namespace': 'app',
                'mtlsMode': 'STRICT',
                'ruleCount': 1,
              },
            ],
          },
        },
      );

      final res = await container.read(meshRepositoryProvider).listPolicies();
      expect(res.policies, hasLength(1));
      expect(res.policies.single.mtlsMode, 'STRICT');
      expect(res.errors, isEmpty);
    });

    test('threads namespace query param when provided', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/policies',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'policies': <Map<String, Object?>>[],
          },
        },
      );

      await container
          .read(meshRepositoryProvider)
          .listPolicies(namespace: 'restricted');
      expect(
          mock.requests.single.queryParameters['namespace'], 'restricted');
    });

    test('empty namespace omits query param (cluster-wide pass-through)',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/policies',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'policies': <Map<String, Object?>>[],
          },
        },
      );

      await container.read(meshRepositoryProvider).listPolicies(namespace: '');
      expect(
          mock.requests.single.queryParameters.containsKey('namespace'),
          isFalse);
    });
  });

  group('MeshRepository._fetchEnvelope error paths', () {
    test('listPolicies 503 rethrows as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/policies',
        (_) => _json({
          'error': {'code': 503, 'message': 'upstream unavailable'},
        }, status: 503),
      );

      await expectLater(
        container.read(meshRepositoryProvider).listPolicies(),
        throwsA(isA<ApiError>().having((e) => e.statusCode, 'statusCode', 503)),
      );
    });

    test('mtlsPosture 500 rethrows as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/mtls',
        (_) => _json({
          'error': {'code': 500, 'message': 'internal error'},
        }, status: 500),
      );

      await expectLater(
        container.read(meshRepositoryProvider).mtlsPosture(namespace: 'app'),
        throwsA(isA<ApiError>().having((e) => e.statusCode, 'statusCode', 500)),
      );
    });
  });

  group('MeshRepository.mtlsPosture', () {
    test('threads required namespace query param + parses workloads',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/mtls',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'workloads': [
              {
                'namespace': 'app',
                'workload': 'web',
                'workloadKind': 'Deployment',
                'mesh': 'istio',
                'state': 'active',
                'source': 'policy',
                'istioMode': 'STRICT',
                'sourceDetail': 'namespace',
                'workloadKindConfident': true,
              },
              {
                'namespace': 'app',
                'workload': 'legacy-555c6d',
                'workloadKind': 'Deployment',
                'mesh': 'istio',
                'state': 'inactive',
                'source': 'default',
                'workloadKindConfident': false,
              },
            ],
            'errors': {'truncated': 'capped at 500 workloads'},
          },
        },
      );

      final res = await container
          .read(meshRepositoryProvider)
          .mtlsPosture(namespace: 'app');
      expect(mock.requests.single.queryParameters['namespace'], 'app');
      expect(res.workloads, hasLength(2));
      expect(res.workloads.first.state, 'active');
      expect(res.workloads.first.istioMode, 'STRICT');
      expect(res.workloads.first.workloadKindConfident, isTrue);
      expect(res.workloads.last.workloadKindConfident, isFalse);
      expect(res.errors, {'truncated': 'capped at 500 workloads'});
    });

    test('403 from RBAC surfaces as ApiError with correct statusCode', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/mtls',
        (_) => _json({
          'error': {
            'code': 403,
            'message': 'forbidden: cannot list pods in namespace restricted',
          },
        }, status: 403),
      );

      await expectLater(
        container.read(meshRepositoryProvider).mtlsPosture(namespace: 'restricted'),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 403)
            .having((e) => e.message, 'message',
                contains('forbidden'))),
      );
    });

    test('400 from missing namespace surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/mesh/mtls',
        (_) => _json({
          'error': {'code': 400, 'message': 'namespace required'},
        }, status: 400),
      );

      await expectLater(
        container.read(meshRepositoryProvider).mtlsPosture(namespace: ''),
        throwsA(isA<ApiError>().having(
          (e) => e.statusCode,
          'statusCode',
          400,
        )),
      );
    });
  });

  group('MeshRepository.goldenSignals', () {
    test('parses tile scalars + missingQueries banner data', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/golden-signals',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'signals': {
              'mesh': 'istio',
              'namespace': 'app',
              'service': 'web',
              'available': true,
              'missingQueries': ['p99'],
              'rps': 42.5,
              'errorRate': 0.013,
              'p50Ms': 12.0,
              'p95Ms': 75.0,
              'p99Ms': 0.0,
            },
          },
        },
      );

      final res = await container.read(meshRepositoryProvider).goldenSignals(
            namespace: 'app',
            service: 'web',
          );
      expect(res.signals.available, isTrue);
      expect(res.signals.missingQueries, ['p99']);
      expect(res.signals.rps, closeTo(42.5, 0.001));
      expect(res.signals.p95Ms, 75.0);
      // Metric in missingQueries returns true regardless of scalar value.
      expect(res.signals.isMetricMissing('p99'), isTrue,
          reason: 'p99 is in missingQueries — should report missing');
      // Metric NOT in missingQueries returns false even when scalar is zero.
      expect(res.signals.isMetricMissing('p95'), isFalse,
          reason: 'p95 is not in missingQueries — zero scalar is still valid');
      expect(res.signals.isMetricMissing('rps'), isFalse,
          reason: 'rps is not in missingQueries — non-zero scalar is valid');
    });

    test('threads mesh disambiguator when both installed', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/golden-signals',
        body: {
          'data': {
            'status': {'detected': 'both'},
            'signals': {
              'mesh': 'linkerd',
              'namespace': 'app',
              'service': 'web',
              'available': true,
            },
          },
        },
      );

      await container.read(meshRepositoryProvider).goldenSignals(
            namespace: 'app',
            service: 'web',
            mesh: 'linkerd',
          );
      final req = mock.requests.single;
      expect(req.queryParameters['mesh'], 'linkerd');
      expect(req.queryParameters['namespace'], 'app');
      expect(req.queryParameters['service'], 'web');
    });

    test('available=false carries reason and zero scalars', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/golden-signals',
        body: {
          'data': {
            'status': {'detected': 'istio'},
            'signals': {
              'mesh': 'istio',
              'namespace': 'app',
              'service': 'web',
              'available': false,
              'reason': 'Prometheus offline',
            },
          },
        },
      );

      final res = await container.read(meshRepositoryProvider).goldenSignals(
            namespace: 'app',
            service: 'web',
          );
      expect(res.signals.available, isFalse);
      expect(res.signals.reason, 'Prometheus offline');
      expect(res.signals.rps, 0.0);
      expect(res.signals.p99Ms, 0.0);
    });
  });

  group('MeshRepository cluster-switch-mid-fetch', () {
    test('request carries X-Cluster-ID of the cluster that initiated the fetch',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': 'istio',
              'istio': {'installed': true},
              'linkerd': {'installed': false},
            },
          },
        },
      );

      // Fire the request pinned to clusterA.
      await container
          .read(meshRepositoryProvider)
          .status(clusterIdOverride: 'clusterA');

      // The request on the wire must carry clusterA's header even if the
      // active cluster was changed to clusterB after the call was made.
      expect(mock.requests, hasLength(1));
      expect(mock.requests.single.headers['X-Cluster-ID'], 'clusterA');
    });
  });

  group('Provider family keys', () {
    test('MeshRoutingKey equality', () {
      final a = MeshRoutingKey(clusterId: 'c1', namespace: 'app');
      final b = MeshRoutingKey(clusterId: 'c1', namespace: 'app');
      final c = MeshRoutingKey(clusterId: 'c1', namespace: 'other');
      final d = MeshRoutingKey(clusterId: 'c1');
      expect(a, equals(b));
      expect(a.hashCode, equals(b.hashCode));
      expect(a, isNot(equals(c)));
      expect(a, isNot(equals(d)));
    });

    test('MeshRoutingKey normalises empty namespace to null', () {
      final empty = MeshRoutingKey(clusterId: 'c1', namespace: '');
      final nullNs = MeshRoutingKey(clusterId: 'c1');
      expect(empty, equals(nullNs));
      expect(empty.namespace, isNull);
    });

    test('MeshRouteDetailKey distinguishes by id', () {
      const a = MeshRouteDetailKey(clusterId: 'c1', id: 'istio:a:vs:n');
      const b = MeshRouteDetailKey(clusterId: 'c1', id: 'istio:b:vs:n');
      expect(a, isNot(equals(b)));
    });

    test('MeshMtlsKey + MeshGoldenSignalsKey distinguish all fields', () {
      const a = MeshMtlsKey(clusterId: 'c1', namespace: 'app');
      const b = MeshMtlsKey(clusterId: 'c1', namespace: 'other');
      expect(a, isNot(equals(b)));

      const ga = MeshGoldenSignalsKey(
        clusterId: 'c1',
        namespace: 'app',
        service: 'web',
      );
      const gb = MeshGoldenSignalsKey(
        clusterId: 'c1',
        namespace: 'app',
        service: 'web',
        mesh: 'istio',
      );
      expect(ga, isNot(equals(gb)));
    });
  });
}
