// Tests for GitOpsRepository: endpoint paths, X-Cluster-ID header
// threading, wire-type parsing, status-on-5xx fallback, composite-ID
// URL encoding round-trip.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/gitops_repository.dart';
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
  group('GitOpsRepository.status', () {
    test('parses argocd-only detection with appSetsAvailable', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/status',
        body: {
          'data': {
            'detected': 'argocd',
            'argocd': {
              'available': true,
              'namespace': 'argocd',
              'appSetsAvailable': true,
            },
            'fluxcd': {'available': false},
            'lastChecked': '2026-05-12T10:00:00Z',
          },
        },
      );

      final s = await container.read(gitOpsRepositoryProvider).status();
      expect(s.isInstalled, isTrue);
      expect(s.hasArgo, isTrue);
      expect(s.hasFlux, isFalse);
      expect(s.argoCD.available, isTrue);
      expect(s.argoCD.appSetsAvailable, isTrue);
      expect(s.fluxCD.available, isFalse);
      expect(s.lastChecked, '2026-05-12T10:00:00Z');
    });

    test('parses both engines detected', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/status',
        body: {
          'data': {
            'detected': 'both',
            'argocd': {'available': true, 'appSetsAvailable': false},
            'fluxcd': {
              'available': true,
              'controllers': ['source', 'kustomize', 'helm'],
            },
          },
        },
      );

      final s = await container.read(gitOpsRepositoryProvider).status();
      expect(s.detected, 'both');
      expect(s.hasArgo, isTrue);
      expect(s.hasFlux, isTrue);
      expect(s.argoCD.appSetsAvailable, isFalse);
      expect(s.fluxCD.controllers, ['source', 'kustomize', 'helm']);
    });

    test('503 from status endpoint returns GitOpsStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/gitops/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'gitops not configured'},
        }, status: 503),
      );

      final s = await container.read(gitOpsRepositoryProvider).status();
      expect(s.isInstalled, isFalse);
      expect(s.detected, '');
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/status',
        body: {
          'data': {'detected': ''},
        },
      );

      await container
          .read(gitOpsRepositoryProvider)
          .status(clusterIdOverride: 'staging-east');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.single.headers['X-Cluster-ID'], 'staging-east');
    });
  });

  group('GitOpsRepository.listApplications', () {
    test('parses normalized apps + summary', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/applications',
        body: {
          'data': {
            'applications': [
              {
                'id': 'argo:argocd:my-app',
                'name': 'my-app',
                'namespace': 'argocd',
                'tool': 'argocd',
                'kind': 'Application',
                'syncStatus': 'synced',
                'healthStatus': 'healthy',
                'source': {
                  'repoURL': 'https://github.com/me/repo',
                  'path': 'apps/web',
                  'targetRevision': 'main',
                },
                'managedResourceCount': 7,
              },
              {
                'id': 'flux-hr:flux-system:my-release',
                'name': 'my-release',
                'namespace': 'flux-system',
                'tool': 'fluxcd',
                'kind': 'HelmRelease',
                'syncStatus': 'outofsync',
                'healthStatus': 'degraded',
                'source': {'chartName': 'redis', 'chartVersion': '17.4.0'},
                'managedResourceCount': 0,
                'suspended': true,
              },
            ],
            'summary': {
              'total': 2,
              'synced': 1,
              'outOfSync': 1,
              'degraded': 1,
              'progressing': 0,
              'suspended': 1,
            },
          },
        },
      );

      final res =
          await container.read(gitOpsRepositoryProvider).listApplications();
      expect(res.applications, hasLength(2));
      expect(res.applications.first.id, 'argo:argocd:my-app');
      expect(res.applications.first.source.path, 'apps/web');
      expect(res.applications.last.suspended, isTrue);
      expect(res.summary.synced, 1);
      expect(res.summary.outOfSync, 1);
    });

    test('surfaces 5xx as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/gitops/applications',
        (_) => _json({
          'error': {'code': 500, 'message': 'internal error'},
        }, status: 500),
      );

      await expectLater(
        container.read(gitOpsRepositoryProvider).listApplications(),
        throwsA(isA<ApiError>()),
      );
    });
  });

  group('GitOpsRepository.getApplication', () {
    test('URL-encodes composite id on the wire (single encode)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // Backend's parseCompositeID does ONE URL-decode of the segment
      // before splitting on ':'. So Uri.encodeComponent("argo:argocd:my-app")
      // = "argo%3Aargocd%3Amy-app" is what the wire path must contain.
      mock.onJson(
        'GET',
        '/api/v1/gitops/applications/argo%3Aargocd%3Amy-app',
        body: {
          'data': {
            'app': {
              'id': 'argo:argocd:my-app',
              'name': 'my-app',
              'namespace': 'argocd',
              'tool': 'argocd',
              'kind': 'Application',
              'syncStatus': 'synced',
              'healthStatus': 'healthy',
              'source': {'repoURL': 'https://github.com/me/repo'},
            },
            'resources': [
              {
                'kind': 'Deployment',
                'namespace': 'default',
                'name': 'web',
                'status': 'Synced',
                'health': 'Healthy',
              },
            ],
            'history': [
              {
                'revision': 'abcdef1234567890',
                'status': 'Synced',
                'message': 'bump web image',
                'deployedAt': '2026-05-11T12:00:00Z',
              },
            ],
          },
        },
      );

      final detail = await container
          .read(gitOpsRepositoryProvider)
          .getApplication(id: 'argo:argocd:my-app');
      expect(detail.app.name, 'my-app');
      expect(detail.resources, hasLength(1));
      expect(detail.history, hasLength(1));
      expect(detail.history!.single.revision, 'abcdef1234567890');
    });

    test('HelmRelease detail with omitted resources + history', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/applications/flux-hr%3Aflux-system%3Amy-release',
        body: {
          'data': {
            'app': {
              'id': 'flux-hr:flux-system:my-release',
              'name': 'my-release',
              'namespace': 'flux-system',
              'tool': 'fluxcd',
              'kind': 'HelmRelease',
              'syncStatus': 'synced',
              'healthStatus': 'healthy',
              'source': {'chartName': 'redis'},
            },
            // resources + history intentionally omitted (Flux HelmRelease).
          },
        },
      );

      final detail = await container
          .read(gitOpsRepositoryProvider)
          .getApplication(id: 'flux-hr:flux-system:my-release');
      expect(detail.app.hidesResourcesAndHistory, isTrue);
      // Null is the signal the tab should be hidden — "empty list"
      // would mean "tab visible, no rows".
      expect(detail.resources, isNull);
      expect(detail.history, isNull);
    });
  });

  group('GitOpsRepository.status 4xx rethrow', () {
    test('401 from status endpoint surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/gitops/status',
        (_) => _json({
          'error': {'code': 401, 'message': 'unauthorized'},
        }, status: 401),
      );

      await expectLater(
        container.read(gitOpsRepositoryProvider).status(),
        throwsA(isA<ApiError>()),
      );
    });
  });

  group('GitOpsRepository.listApplicationSets', () {
    test('parses normalized appsets from camelCase envelope key', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // The handler emits `applicationSets` (camelCase) in the JSON.
      // The lowercase form is only the URL segment.
      mock.onJson(
        'GET',
        '/api/v1/gitops/applicationsets',
        body: {
          'data': {
            'applicationSets': [
              {
                'id': 'argo-as:argocd:my-set',
                'name': 'my-set',
                'namespace': 'argocd',
                'tool': 'argocd',
                'generatorTypes': ['list'],
                'templateSource': {'repoURL': 'https://github.com/me/repo'},
                'templateDestination': 'in-cluster/default',
                'status': 'Healthy',
                'generatedAppCount': 3,
                'preserveOnDeletion': false,
                'createdAt': '2026-05-01T00:00:00Z',
              },
            ],
            'total': 1,
          },
        },
      );

      final sets = await container
          .read(gitOpsRepositoryProvider)
          .listApplicationSets();
      expect(sets, hasLength(1));
      expect(sets.single.id, 'argo-as:argocd:my-set');
      expect(sets.single.generatorTypes, ['list']);
      expect(sets.single.generatedAppCount, 3);
    });

    test('empty when envelope is missing the camelCase key', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/applicationsets',
        body: {
          'data': {'total': 0},
        },
      );

      final sets = await container
          .read(gitOpsRepositoryProvider)
          .listApplicationSets();
      expect(sets, isEmpty);
    });
  });

  group('GitOpsRepository.listApplicationSets errors', () {
    test('5xx surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/gitops/applicationsets',
        (_) => _json({
          'error': {'code': 503, 'message': 'service unavailable'},
        }, status: 503),
      );

      await expectLater(
        container.read(gitOpsRepositoryProvider).listApplicationSets(),
        throwsA(isA<ApiError>()),
      );
    });
  });

  group('GitOpsRepository.getApplicationSet', () {
    test('parses appset + generators + conditions + applications',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
        body: {
          'data': {
            'appSet': {
              'id': 'argo-as:argocd:my-set',
              'name': 'my-set',
              'namespace': 'argocd',
              'tool': 'argocd',
              'generatorTypes': ['git'],
              'templateSource': {'repoURL': 'https://example.com/r'},
              'templateDestination': 'in-cluster/default',
              'status': 'Healthy',
            },
            'generators': [
              {
                'git': {
                  'repoURL': 'https://example.com/r',
                  'revision': 'HEAD',
                },
              },
            ],
            'conditions': [
              {
                'type': 'ErrorOccurred',
                'status': 'False',
                'reason': 'NoErrors',
              },
            ],
            'applications': [
              {
                'id': 'argo:argocd:child-app',
                'name': 'child-app',
                'namespace': 'argocd',
                'tool': 'argocd',
                'kind': 'Application',
                'syncStatus': 'synced',
                'healthStatus': 'healthy',
                'source': {'repoURL': 'https://example.com/r'},
              },
            ],
          },
        },
      );

      final detail = await container
          .read(gitOpsRepositoryProvider)
          .getApplicationSet(id: 'argo-as:argocd:my-set');
      expect(detail.appSet.name, 'my-set');
      expect(detail.generators, hasLength(1));
      expect(detail.generators.single.containsKey('git'), isTrue);
      expect(detail.conditions, hasLength(1));
      expect(detail.conditions.single.isError, isFalse);
      expect(detail.applications, hasLength(1));
      expect(detail.applications.single.id, 'argo:argocd:child-app');
    });

    test('5xx response surfaces as ApiError', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
        (_) => _json({
          'error': {'code': 500, 'message': 'internal error'},
        }, status: 500),
      );

      await expectLater(
        container
            .read(gitOpsRepositoryProvider)
            .getApplicationSet(id: 'argo-as:argocd:my-set'),
        throwsA(isA<ApiError>()),
      );
    });
  });

  group('AppSetCondition.isError', () {
    test('true when status is True and type contains error', () {
      const c = AppSetCondition(
        type: 'ErrorOccurred',
        status: 'True',
        message: 'git repo not found',
        reason: 'GitRepoNotFound',
      );
      expect(c.isError, isTrue);
    });

    test('false when status is False even if type contains error', () {
      const c = AppSetCondition(
        type: 'ErrorOccurred',
        status: 'False',
        reason: 'NoErrors',
      );
      expect(c.isError, isFalse);
    });

    test('false when type does not contain error', () {
      const c = AppSetCondition(
        type: 'ResourcesUpToDate',
        status: 'True',
      );
      expect(c.isError, isFalse);
    });
  });

  group('NormalizedApp.hidesResourcesAndHistory', () {
    test('only true for flux-hr prefix', () {
      const argo = NormalizedApp(
        id: 'argo:argocd:my-app',
        name: 'my-app',
        namespace: 'argocd',
        tool: 'argocd',
        kind: 'Application',
        syncStatus: 'synced',
        healthStatus: 'healthy',
        source: AppSource(),
      );
      const fluxKs = NormalizedApp(
        id: 'flux-ks:flux-system:my-ks',
        name: 'my-ks',
        namespace: 'flux-system',
        tool: 'fluxcd',
        kind: 'Kustomization',
        syncStatus: 'synced',
        healthStatus: 'healthy',
        source: AppSource(),
      );
      const fluxHr = NormalizedApp(
        id: 'flux-hr:flux-system:my-release',
        name: 'my-release',
        namespace: 'flux-system',
        tool: 'fluxcd',
        kind: 'HelmRelease',
        syncStatus: 'synced',
        healthStatus: 'healthy',
        source: AppSource(),
      );
      expect(argo.hidesResourcesAndHistory, isFalse);
      expect(fluxKs.hidesResourcesAndHistory, isFalse);
      expect(fluxHr.hidesResourcesAndHistory, isTrue);
    });
  });
}
