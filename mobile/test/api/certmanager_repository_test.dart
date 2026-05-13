// CertManagerRepository tests: endpoint paths, envelope unwrapping,
// X-Cluster-ID forwarding, threshold attribution parsing, sub-resource
// nesting, error surfacing, and renew/reissue POST bodies.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/certmanager_repository.dart';
import 'package:kubecenter/api/dio_client.dart';
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
  group('CertManagerRepository.status', () {
    test('parses detected status with namespace + version', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/status',
        body: {
          'data': {
            'detected': true,
            'namespace': 'cert-manager',
            'version': '1.14.0',
            'lastChecked': '2026-05-12T10:00:00Z',
          },
        },
      );

      final s = await container.read(certManagerRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.namespace, 'cert-manager');
      expect(s.version, '1.14.0');
      expect(s.lastChecked, '2026-05-12T10:00:00Z');
    });

    test('non-admin shape (no namespace field) parses cleanly', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/status',
        body: {
          'data': {'detected': true},
        },
      );

      final s = await container.read(certManagerRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.namespace, isNull);
      expect(s.version, isNull);
    });

    test('503 from status returns CertManagerStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/certificates/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'discovery offline'},
        }, status: 503),
      );

      final s = await container.read(certManagerRepositoryProvider).status();
      expect(s.detected, isFalse);
    });

    test('401 surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/certificates/status',
        (_) => _json({
          'error': {'code': 401, 'message': 'unauthorized'},
        }, status: 401),
      );

      await expectLater(
        container.read(certManagerRepositoryProvider).status(),
        throwsA(isA<ApiError>()),
      );
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/status',
        body: {
          'data': {'detected': false},
        },
      );

      await container
          .read(certManagerRepositoryProvider)
          .status(clusterIdOverride: 'staging-east');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.single.headers['X-Cluster-ID'], 'staging-east');
    });
  });

  group('CertManagerRepository.listCertificates', () {
    test('parses cert list with resolved threshold attribution', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/certificates',
        body: {
          'data': [
            {
              'name': 'web-tls',
              'namespace': 'app',
              'status': 'Ready',
              'issuerRef': {
                'name': 'letsencrypt-prod',
                'kind': 'ClusterIssuer',
                'group': 'cert-manager.io',
              },
              'secretName': 'web-tls-secret',
              'dnsNames': ['web.example.com'],
              'commonName': 'web.example.com',
              'daysRemaining': 45,
              'warningThresholdDays': 60,
              'criticalThresholdDays': 14,
              'warningThresholdSource': 'issuer',
              'criticalThresholdSource': 'certificate',
              'thresholdSource': 'issuer',
              'thresholdConflict': false,
              'uid': 'abc-123',
            },
            {
              'name': 'broken-tls',
              'namespace': 'app',
              'status': 'Failed',
              'issuerRef': {'name': 'self-signed', 'kind': 'Issuer'},
              'secretName': 'broken-tls-secret',
              'reason': 'IssuerNotReady',
              'message': 'Issuer not ready: pending',
              'uid': 'broken-uid',
            },
          ],
        },
      );

      final certs = await container
          .read(certManagerRepositoryProvider)
          .listCertificates();
      expect(certs, hasLength(2));

      final ready = certs.first;
      expect(ready.name, 'web-tls');
      expect(ready.status, CertStatus.ready);
      expect(ready.daysRemaining, 45);
      expect(ready.warningThresholdDays, 60);
      expect(ready.criticalThresholdDays, 14);
      expect(ready.warningThresholdSource, ThresholdSource.issuer);
      expect(ready.criticalThresholdSource, ThresholdSource.certificate);
      expect(ready.thresholdConflict, isFalse);
      expect(ready.issuerRef.kind, 'ClusterIssuer');
      expect(ready.dnsNames, ['web.example.com']);

      final failed = certs[1];
      expect(failed.status, CertStatus.failed);
      expect(failed.reason, 'IssuerNotReady');
      expect(failed.daysRemaining, isNull);
      expect(failed.warningThresholdDays, isNull);
    });

    test('threads namespace query param when provided', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/certificates',
        body: {'data': <Map<String, Object?>>[]},
      );

      await container
          .read(certManagerRepositoryProvider)
          .listCertificates(namespace: 'app');

      expect(mock.requests.single.queryParameters['namespace'], 'app');
    });

    test('empty namespace omits the query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/certificates',
        body: {'data': <Map<String, Object?>>[]},
      );

      await container
          .read(certManagerRepositoryProvider)
          .listCertificates(namespace: '');

      expect(
        mock.requests.single.queryParameters.containsKey('namespace'),
        isFalse,
      );
    });

    test('unknown thresholdSource string maps to ThresholdSource.unknown',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/certificates',
        body: {
          'data': [
            {
              'name': 'odd',
              'namespace': 'app',
              'status': 'Ready',
              'issuerRef': {'name': 'iss', 'kind': 'Issuer'},
              'secretName': 's',
              'warningThresholdSource': 'something-from-the-future',
              'uid': 'u',
            },
          ],
        },
      );

      final certs = await container
          .read(certManagerRepositoryProvider)
          .listCertificates();
      expect(certs.single.warningThresholdSource, ThresholdSource.unknown);
    });

    test('500 surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/certificates/certificates',
        (_) => _json({
          'error': {'code': 500, 'message': 'failed to fetch certificates'},
        }, status: 500),
      );

      await expectLater(
        container.read(certManagerRepositoryProvider).listCertificates(),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 500)),
      );
    });
  });

  group('CertManagerRepository.getCertificate', () {
    test('parses detail with sub-resources + thresholdConflict', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: {
          'data': {
            'certificate': {
              'name': 'web-tls',
              'namespace': 'app',
              'status': 'Issuing',
              'issuerRef': {'name': 'le', 'kind': 'ClusterIssuer'},
              'secretName': 'web-tls-secret',
              'thresholdConflict': true,
              'warningThresholdDays': 30,
              'criticalThresholdDays': 7,
              'warningThresholdSource': 'default',
              'criticalThresholdSource': 'default',
              'uid': 'cert-uid',
            },
            'certificateRequests': [
              {
                'name': 'web-tls-1',
                'namespace': 'app',
                'status': 'Issuing',
                'issuerRef': {'name': 'le', 'kind': 'ClusterIssuer'},
                'createdAt': '2026-05-10T09:00:00Z',
                'uid': 'cr-uid',
              },
            ],
            'orders': [
              {
                'name': 'web-tls-1-order',
                'namespace': 'app',
                'state': 'pending',
                'createdAt': '2026-05-10T09:01:00Z',
                'uid': 'order-uid',
                'crName': 'web-tls-1',
              },
            ],
            'challenges': [
              {
                'name': 'web-tls-1-challenge',
                'namespace': 'app',
                'type': 'HTTP-01',
                'state': 'pending',
                'dnsName': 'web.example.com',
                'createdAt': '2026-05-10T09:02:00Z',
                'uid': 'chall-uid',
                'orderName': 'web-tls-1-order',
              },
            ],
          },
        },
      );

      final detail = await container
          .read(certManagerRepositoryProvider)
          .getCertificate(namespace: 'app', name: 'web-tls');

      expect(detail.certificate.status, CertStatus.issuing);
      expect(detail.certificate.thresholdConflict, isTrue);
      expect(detail.certificateRequests, hasLength(1));
      expect(detail.certificateRequests.single.status, CertStatus.issuing);
      expect(detail.orders, hasLength(1));
      expect(detail.orders.single.state, 'pending');
      expect(detail.challenges, hasLength(1));
      expect(detail.challenges.single.dnsName, 'web.example.com');
    });

    test('URL-encodes namespace + name', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        // ":" in a namespace isn't legal, but odd cert names with dots
        // are common. Verify both segments encode.
        '/api/v1/certificates/certificates/edge-app/web.example.com',
        body: {
          'data': {
            'certificate': {
              'name': 'web.example.com',
              'namespace': 'edge-app',
              'status': 'Ready',
              'issuerRef': {'name': 'le', 'kind': 'ClusterIssuer'},
              'secretName': 's',
              'uid': 'u',
            },
            'certificateRequests': <Map<String, Object?>>[],
            'orders': <Map<String, Object?>>[],
            'challenges': <Map<String, Object?>>[],
          },
        },
      );

      final detail = await container
          .read(certManagerRepositoryProvider)
          .getCertificate(
            namespace: 'edge-app',
            name: 'web.example.com',
          );
      expect(detail.certificate.name, 'web.example.com');
    });

    test('404 on missing cert surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/certificates/certificates/app/ghost',
        (_) => _json({
          'error': {'code': 404, 'message': 'certificate not found'},
        }, status: 404),
      );

      await expectLater(
        container.read(certManagerRepositoryProvider).getCertificate(
              namespace: 'app',
              name: 'ghost',
            ),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 404)),
      );
    });
  });

  group('CertManagerRepository.listIssuers / listClusterIssuers', () {
    test('parses issuer list with threshold annotations', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/issuers',
        body: {
          'data': [
            {
              'name': 'self-signed',
              'namespace': 'app',
              'scope': 'Namespaced',
              'type': 'SelfSigned',
              'ready': true,
              'warningThresholdDays': 60,
              'criticalThresholdDays': 10,
              'uid': 'iss-uid',
              'updatedAt': '2026-05-12T10:00:00Z',
            },
          ],
        },
      );

      final issuers = await container
          .read(certManagerRepositoryProvider)
          .listIssuers();
      expect(issuers, hasLength(1));
      expect(issuers.single.scope, 'Namespaced');
      expect(issuers.single.type, 'SelfSigned');
      expect(issuers.single.ready, isTrue);
      expect(issuers.single.warningThresholdDays, 60);
      expect(issuers.single.isCluster, isFalse);
    });

    test('cluster issuers parse with empty namespace', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {
          'data': [
            {
              'name': 'letsencrypt-prod',
              'scope': 'Cluster',
              'type': 'ACME',
              'ready': true,
              'acmeEmail': 'ops@example.com',
              'acmeServer': 'https://acme-v02.api.letsencrypt.org/directory',
              'uid': 'le-uid',
              'updatedAt': '2026-05-12T10:00:00Z',
            },
          ],
        },
      );

      final cl = await container
          .read(certManagerRepositoryProvider)
          .listClusterIssuers();
      expect(cl, hasLength(1));
      expect(cl.single.isCluster, isTrue);
      expect(cl.single.namespace, isEmpty);
      expect(cl.single.acmeServer, contains('letsencrypt'));
    });
  });

  group('CertManagerRepository.listExpiring', () {
    test('parses backend-sorted expiring rows with severity', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/expiring',
        body: {
          'data': [
            {
              'namespace': 'app',
              'name': 'critical-cert',
              'uid': 'c-uid',
              'issuerName': 'le',
              'secretName': 'critical-secret',
              'notAfter': '2026-05-15T00:00:00Z',
              'daysRemaining': 2,
              'severity': 'critical',
            },
            {
              'namespace': 'app',
              'name': 'warning-cert',
              'uid': 'w-uid',
              'issuerName': 'le',
              'secretName': 'warning-secret',
              'notAfter': '2026-06-05T00:00:00Z',
              'daysRemaining': 23,
              'severity': 'warning',
            },
          ],
        },
      );

      final rows = await container
          .read(certManagerRepositoryProvider)
          .listExpiring();
      expect(rows, hasLength(2));
      expect(rows.first.severity, 'critical');
      expect(rows.first.daysRemaining, 2);
      expect(rows[1].severity, 'warning');
    });

    test('preserves unknown severity values for forward-compat', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/expiring',
        body: {
          'data': [
            {
              'namespace': 'app',
              'name': 'expired',
              'uid': 'e-uid',
              'issuerName': 'le',
              'secretName': 's',
              'notAfter': '2026-05-01T00:00:00Z',
              'daysRemaining': -3,
              'severity': 'expired',
            },
          ],
        },
      );

      final rows = await container
          .read(certManagerRepositoryProvider)
          .listExpiring();
      expect(rows.single.severity, 'expired');
    });
  });

  group('CertManagerRepository.renew / reissue', () {
    test('renew POSTs to /renew and returns the status verb', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        '/api/v1/certificates/certificates/app/web-tls/renew',
        status: 202,
        body: {
          'data': {'status': 'renewing'},
        },
      );

      final result = await container
          .read(certManagerRepositoryProvider)
          .renew(namespace: 'app', name: 'web-tls');
      expect(result.status, 'renewing');
      expect(mock.requests.single.method, 'POST');
    });

    test('reissue POSTs to /reissue and returns the status verb', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        '/api/v1/certificates/certificates/app/web-tls/reissue',
        status: 202,
        body: {
          'data': {'status': 'reissuing'},
        },
      );

      final result = await container
          .read(certManagerRepositoryProvider)
          .reissue(namespace: 'app', name: 'web-tls');
      expect(result.status, 'reissuing');
    });

    test('renew 403 surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        '/api/v1/certificates/certificates/app/web-tls/renew',
        (_) => _json({
          'error': {'code': 403, 'message': 'access denied'},
        }, status: 403),
      );

      await expectLater(
        container.read(certManagerRepositoryProvider).renew(
              namespace: 'app',
              name: 'web-tls',
            ),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 403)),
      );
    });

    test('reissue body-less response defaults the status verb', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        '/api/v1/certificates/certificates/app/web-tls/reissue',
        status: 202,
        body: <String, Object?>{},
      );

      final result = await container
          .read(certManagerRepositoryProvider)
          .reissue(namespace: 'app', name: 'web-tls');
      // Backend always sets the field; the default protects against a
      // future shape change. The explicit verb→tense map in the repo
      // ensures the fallback is grammatical (NOT "reissueing").
      expect(result.status, 'reissuing');
    });
  });

  group('CertificateListKey equality', () {
    test('empty namespace string normalises to null slot', () {
      final a = CertificateListKey(clusterId: 'c', namespace: '');
      final b = CertificateListKey(clusterId: 'c');
      expect(a, equals(b));
      expect(a.hashCode, equals(b.hashCode));
    });

    test('different namespaces produce distinct slots', () {
      final a = CertificateListKey(clusterId: 'c', namespace: 'app');
      final b = CertificateListKey(clusterId: 'c', namespace: 'kube-system');
      expect(a, isNot(equals(b)));
    });
  });
}
