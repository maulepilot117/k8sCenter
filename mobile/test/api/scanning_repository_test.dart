// ScanningRepository tests: endpoint paths, envelope unwrapping,
// X-Cluster-ID forwarding, status 5xx → unreachable, severity parsing
// (open enum + UPPER → lower normalisation), namespace param injection,
// 501 surfaced verbatim for Kubescape rows hitting the Trivy-only
// detail endpoint, and stale-scan threshold edge cases.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/scanning_repository.dart';
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
  group('ScanningRepository.status', () {
    test('parses both scanners detected', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/status', body: {
        'data': {
          'detected': 'both',
          'trivy': {'available': true, 'namespace': 'trivy-system'},
          'kubescape': {'available': true, 'namespace': 'kubescape'},
          'lastChecked': '2026-05-15T12:00:00Z',
        },
      });

      final s = await container.read(scanningRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.trivyAvailable, isTrue);
      expect(s.kubescapeAvailable, isTrue);
      expect(s.trivy?.namespace, 'trivy-system');
      expect(s.kubescape?.namespace, 'kubescape');
      expect(s.lastChecked, '2026-05-15T12:00:00Z');
    });

    test('detected=false when neither scanner available', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/status', body: {
        'data': {'detected': ''},
      });

      final s = await container.read(scanningRepositoryProvider).status();
      expect(s.detected, isFalse);
      expect(s.trivyAvailable, isFalse);
      expect(s.kubescapeAvailable, isFalse);
    });

    test('5xx returns ScanningStatus.unreachable', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/scanning/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'discovery offline'},
        }, status: 503),
      );

      final s = await container.read(scanningRepositoryProvider).status();
      expect(s.detected, isFalse);
      expect(s.serviceUnavailable, isTrue,
          reason:
              '5xx must round-trip through `unreachable` so the UI can '
              'distinguish "no scanner installed" from "backend '
              'temporarily unreachable".');
    });

    test('admin-stripped namespace null when backend omits it', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/status', body: {
        'data': {
          'detected': 'trivy',
          'trivy': {'available': true},
          'lastChecked': '2026-05-15T12:00:00Z',
        },
      });

      final s = await container.read(scanningRepositoryProvider).status();
      expect(s.trivy?.namespace, isNull);
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/status', body: {
        'data': {'detected': ''},
      });

      await container
          .read(scanningRepositoryProvider)
          .status(clusterIdOverride: 'remote-1');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-1');
    });
  });

  group('ScanningRepository.listVulnerabilities', () {
    test('parses vulnerabilities array + summary metadata', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/vulnerabilities', body: {
        'data': {
          'vulnerabilities': [
            {
              'namespace': 'app',
              'kind': 'Deployment',
              'name': 'web',
              'images': [
                {
                  'image': 'docker.io/library/nginx:1.21',
                  'severities': {
                    'critical': 2,
                    'high': 4,
                    'medium': 5,
                    'low': 8,
                  },
                },
              ],
              'total': {
                'critical': 2,
                'high': 4,
                'medium': 5,
                'low': 8,
              },
              'lastScanned': '2026-05-15T11:00:00Z',
              'scanner': 'trivy',
            },
            {
              'namespace': 'app',
              'kind': 'Deployment',
              'name': 'cache',
              'images': <Map<String, Object?>>[],
              'total': {
                'critical': 0,
                'high': 0,
                'medium': 0,
                'low': 0,
              },
              'lastScanned': '2026-05-15T11:00:00Z',
              'scanner': 'kubescape',
            },
          ],
          'summary': {
            'total': 2,
            'severity': {
              'critical': 2,
              'high': 4,
              'medium': 5,
              'low': 8,
            },
          },
        },
      });

      final resp = await container
          .read(scanningRepositoryProvider)
          .listVulnerabilities(namespace: 'app');
      expect(resp.vulnerabilities, hasLength(2));
      expect(resp.vulnerabilities[0].name, 'web');
      expect(resp.vulnerabilities[0].scanner, Scanner.trivy);
      expect(resp.vulnerabilities[0].total.total, 19);
      expect(resp.vulnerabilities[0].total.severityScore, 2004,
          reason: '2*1000 + 4 (critical*1000 + high)');
      expect(resp.vulnerabilities[1].scanner, Scanner.kubescape);
      expect(resp.summary.total, 2);
      expect(resp.summary.severity.critical, 2);

      // Namespace param was passed verbatim.
      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.queryParameters['namespace'], 'app');
    });

    test('unknown scanner string falls through to Scanner.unknown',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/vulnerabilities', body: {
        'data': {
          'vulnerabilities': [
            {
              'namespace': 'app',
              'kind': 'Pod',
              'name': 'a',
              'images': <Map<String, Object?>>[],
              'total': {'critical': 0, 'high': 0, 'medium': 0, 'low': 0},
              'lastScanned': '',
              'scanner': 'falco-2027',
            },
          ],
          'summary': {
            'total': 1,
            'severity': {'critical': 0, 'high': 0, 'medium': 0, 'low': 0},
          },
        },
      });

      final resp = await container
          .read(scanningRepositoryProvider)
          .listVulnerabilities(namespace: 'app');
      expect(resp.vulnerabilities[0].scanner, Scanner.unknown,
          reason: 'Future scanner names must not crash the parser.');
    });

    test('missing data envelope yields empty response', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/vulnerabilities', body: const {});

      final resp = await container
          .read(scanningRepositoryProvider)
          .listVulnerabilities(namespace: 'app');
      expect(resp.vulnerabilities, isEmpty);
      expect(resp.summary.total, 0);
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/scanning/vulnerabilities', body: {
        'data': {
          'vulnerabilities': <Map<String, Object?>>[],
          'summary': {
            'total': 0,
            'severity': {'critical': 0, 'high': 0, 'medium': 0, 'low': 0},
          },
        },
      });

      await container
          .read(scanningRepositoryProvider)
          .listVulnerabilities(namespace: 'app', clusterIdOverride: 'remote-1');

      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-1');
    });
  });

  group('ScanningRepository.getVulnerabilityDetail', () {
    test('parses image list + CVE detail with case normalisation', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities/app/Deployment/web',
        body: {
          'data': {
            'namespace': 'app',
            'kind': 'Deployment',
            'name': 'web',
            'scanner': 'trivy',
            'lastScanned': '2026-05-15T11:00:00Z',
            'images': [
              {
                'name': 'docker.io/library/nginx:1.21',
                'container': 'web',
                'vulnerabilities': [
                  {
                    'id': 'CVE-2024-1234',
                    'severity': 'CRITICAL',
                    'cvssScore': 9.8,
                    'package': 'openssl',
                    'installedVersion': '1.1.1k',
                    'fixedVersion': '1.1.1n',
                    'title': 'OpenSSL crash',
                    'primaryLink': 'https://nvd.nist.gov/vuln/detail/CVE-2024-1234',
                  },
                  {
                    'id': 'CVE-2024-9999',
                    'severity': 'unicorn-tier',
                    'cvssScore': null,
                    'package': 'libfoo',
                    'installedVersion': '1.0',
                    'fixedVersion': '',
                    'title': '',
                    'primaryLink': '',
                  },
                ],
              },
            ],
          },
        },
      );

      final d = await container
          .read(scanningRepositoryProvider)
          .getVulnerabilityDetail(
            namespace: 'app',
            kind: 'Deployment',
            name: 'web',
          );
      expect(d.scanner, Scanner.trivy);
      expect(d.images, hasLength(1));
      expect(d.images[0].vulnerabilities, hasLength(2));
      expect(d.images[0].vulnerabilities[0].severity, 'critical',
          reason: 'UPPER severity must lowercase on parse.');
      expect(d.images[0].vulnerabilities[0].cvssScore, 9.8);
      expect(d.images[0].vulnerabilities[1].severity, 'unknown',
          reason: 'Unrecognised severity falls through to unknown.');
      expect(d.images[0].vulnerabilities[1].cvssScore, isNull);
      expect(d.images[0].vulnerabilities[0].hasFix, isTrue);
      expect(d.images[0].vulnerabilities[1].hasFix, isFalse);
      expect(d.summary.critical, 1);
      expect(d.fixableCount, 1);
    });

    test('501 → ApiError surfaced verbatim (Kubescape detail attempt)',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/scanning/vulnerabilities/app/Deployment/cache',
        (_) => _json({
          'error': {
            'code': 501,
            'message': 'CVE-level detail requires Trivy Operator',
          },
        }, status: 501),
      );

      Object? captured;
      try {
        await container
            .read(scanningRepositoryProvider)
            .getVulnerabilityDetail(
              namespace: 'app',
              kind: 'Deployment',
              name: 'cache',
            );
      } on Object catch (e) {
        captured = e;
      }
      expect(captured, isA<ApiError>());
      expect((captured as ApiError).statusCode, 501);
      expect(captured.message, contains('Trivy Operator'));
    });

    test('URL-encodes namespace / kind / name path segments', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // The encoded path differs from the raw arguments only when the
      // input contains characters that need encoding. We register the
      // handler against the encoded form because that's what the Dio
      // request will use.
      mock.onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities/my%2Fns/Deployment/web',
        body: {
          'data': {
            'namespace': 'my/ns',
            'kind': 'Deployment',
            'name': 'web',
            'scanner': 'trivy',
            'lastScanned': '',
            'images': <Map<String, Object?>>[],
          },
        },
      );

      await container
          .read(scanningRepositoryProvider)
          .getVulnerabilityDetail(
            namespace: 'my/ns',
            kind: 'Deployment',
            name: 'web',
          );
      expect(mock.requests.first.path,
          '/api/v1/scanning/vulnerabilities/my%2Fns/Deployment/web');
    });
  });

  // #18 — listVulnerabilities 5xx path must throw, not swallow.
  group('ScanningRepository.listVulnerabilities 5xx', () {
    test('503 throws ApiError (throw-on-5xx contract)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        (_) => _json({
          'error': {'code': 503, 'message': 'backend offline'},
        }, status: 503),
      );

      await expectLater(
        container
            .read(scanningRepositoryProvider)
            .listVulnerabilities(namespace: 'app'),
        throwsA(isA<ApiError>()),
        reason:
            'The list endpoint is a data endpoint — 5xx must propagate '
            'so the list screen banner appears rather than silently '
            'rendering a stale or empty state.',
      );
    });
  });

  group('isScanStale', () {
    test('empty timestamp is not stale', () {
      expect(isScanStale(''), isFalse);
    });

    test('malformed timestamp is not stale', () {
      expect(isScanStale('not-a-date'), isFalse);
    });

    test('1 day old is not stale', () {
      final t = DateTime.now().toUtc().subtract(const Duration(days: 1));
      expect(isScanStale(t.toIso8601String()), isFalse);
    });

    test('8 days old is stale', () {
      final t = DateTime.now().toUtc().subtract(const Duration(days: 8));
      expect(isScanStale(t.toIso8601String()), isTrue);
    });

    // #19 — boundary semantics for isScanStale.
    //
    // The exact-boundary case (diff == kScanStaleThreshold) is not directly
    // testable: by the time isScanStale calls DateTime.now() internally, some
    // microseconds have elapsed since the test's DateTime.now(), so the diff
    // is always strictly greater than the threshold. We lock in the > vs >=
    // contract via the "just under" + "just over" pair instead — those bracket
    // the threshold reliably regardless of clock drift.
    test('1 second under threshold is NOT stale', () {
      final t = DateTime.now()
          .toUtc()
          .subtract(kScanStaleThreshold - const Duration(seconds: 1));
      expect(isScanStale(t.toIso8601String()), isFalse,
          reason: 'Diff under threshold must not be stale (locks in > vs >=).');
    });

    test('1 second over threshold is stale', () {
      final t = DateTime.now()
          .toUtc()
          .subtract(kScanStaleThreshold + const Duration(seconds: 1));
      expect(isScanStale(t.toIso8601String()), isTrue,
          reason: 'One second past the threshold must be stale.');
    });
  });

  group('safeHttpUrl', () {
    test('http and https pass through', () {
      expect(safeHttpUrl('https://nvd.nist.gov/x', 'fb'),
          'https://nvd.nist.gov/x');
      expect(safeHttpUrl('http://example.com', 'fb'), 'http://example.com');
    });

    test('non-http schemes fall back', () {
      expect(safeHttpUrl('javascript:alert(1)', 'fb'), 'fb');
      expect(safeHttpUrl('data:text/html,<script>x</script>', 'fb'), 'fb');
      expect(safeHttpUrl('ftp://nope.example/', 'fb'), 'fb');
    });

    test('empty input falls back', () {
      expect(safeHttpUrl('', 'fb'), 'fb');
    });
  });
}
