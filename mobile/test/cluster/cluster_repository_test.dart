// Verifies cluster list fetch + degraded-mode fallback.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_repository.dart';

import '../support/mock_dio_adapter.dart';

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
          {'id': 'local', 'name': 'local', 'isLocal': true, 'status': 'ready'},
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

    final clusters = await container.read(clusterRepositoryProvider).list();
    expect(clusters, hasLength(2));
    expect(clusters[0].id, 'local');
    expect(clusters[1].label, 'Production');
    expect(clusters[1].k8sVersion, '1.31.2');
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

    final clusters = await container.read(clusterRepositoryProvider).list();
    expect(clusters.first.id, 'local');
    expect(clusters, hasLength(2));
  });

  test('list degrades to local-only on network failure', () async {
    final (:container, :mock) = _make();
    addTearDown(container.dispose);
    // Adapter returns 404 for unmocked GET — repository should still
    // fall back rather than throw.

    final clusters = await container.read(clusterRepositoryProvider).list();
    expect(clusters, hasLength(1));
    expect(clusters.first.id, 'local');
  });
}
