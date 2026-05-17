// Controller-level tests for the ESO Force Sync write path.
//
// Coverage:
//   * Happy path — 202 response lands as ForceSyncSuccess and the wire
//     POST carried the correct X-Cluster-ID header.
//   * Idempotent fast-tap — a second invoke while inFlight is a no-op,
//     no duplicate wire POST.
//   * 409 already_refreshing — emits ForceSyncFailure with
//     `alreadyRefreshing: true` and informational copy.
//   * 501 (defensive — UI should have blocked) — emits failure with
//     local-cluster-only copy.
//   * 503 ESO not detected — install-guidance copy.
//   * 403 RBAC denied — permission copy.
//   * 404 not found — deleted-resource copy.
//   * Cluster-pin race postEmission — request succeeded but operator
//     switched cluster mid-flight; emits failure (no invalidate on the
//     wrong slot).
//   * acknowledge() rolls success back to idle so a re-trigger works.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/features/eso/force_sync_controller.dart';

import '../../support/mock_dio_adapter.dart';

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

const _key = ForceSyncKey(
  clusterId: 'local',
  namespace: 'production',
  name: 'db-credentials',
);

const _path =
    '/api/v1/externalsecrets/externalsecrets/production/db-credentials/force-sync';

ProviderSubscription<ForceSyncState> _subscribe(ProviderContainer container) {
  return container.listen<ForceSyncState>(
    forceSyncControllerProvider(_key),
    (_, _) {},
  );
}

Future<void> _settle() => pumpEventQueue(times: 30);

void main() {
  group('ForceSyncController.happy path', () {
    test('202 lands as ForceSyncSuccess with the namespace/name message',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        _path,
        status: 202,
        body: {
          'data': {'status': 'force-syncing'},
        },
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);

      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      expect(sub.read(), isA<ForceSyncIdle>());

      await ctrl.forceSync();
      await _settle();

      final state = sub.read();
      expect(state, isA<ForceSyncSuccess>());
      final success = state as ForceSyncSuccess;
      expect(success.message, contains('production/db-credentials'));
      expect(success.generation, greaterThan(0));
    });

    test('forwards X-Cluster-ID header pinned to the family key', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        _path,
        status: 202,
        body: {'data': {'status': 'force-syncing'}},
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final headers = mock.requests
          .where((r) => r.path == _path)
          .map((r) => r.headers['X-Cluster-ID'])
          .toList();
      expect(headers, contains('local'));
    });

    test('fast double-tap while inFlight does NOT fire a second POST',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        _path,
        status: 202,
        body: {'data': {'status': 'force-syncing'}},
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);

      // Fire two without awaiting between them. The second hits the
      // `state is ForceSyncInFlight` guard and short-circuits.
      final first = ctrl.forceSync();
      final second = ctrl.forceSync();
      await Future.wait([first, second]);
      await _settle();

      final postCount =
          mock.requests.where((r) => r.path == _path).length;
      expect(postCount, 1,
          reason: 'second tap during inFlight must short-circuit');
    });

    test('acknowledge() rolls success back to idle so the action can re-fire',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        _path,
        status: 202,
        body: {'data': {'status': 'force-syncing'}},
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();
      expect(sub.read(), isA<ForceSyncSuccess>());

      ctrl.acknowledge();
      expect(sub.read(), isA<ForceSyncIdle>());
    });
  });

  group('ForceSyncController.error paths', () {
    test('409 already_refreshing emits informational ForceSyncFailure',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        _path,
        (_) => _json({
          'error': {
            'code': 409,
            'message': 'already refreshing',
            'reason': 'already_refreshing',
          },
        }, status: 409),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final state = sub.read();
      expect(state, isA<ForceSyncFailure>());
      final failure = state as ForceSyncFailure;
      expect(failure.alreadyRefreshing, isTrue);
      expect(failure.message.toLowerCase(), contains('progress'));
    });

    test('501 surfaces local-cluster-only copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        _path,
        (_) => _json({
          'error': {
            'code': 501,
            'message': 'ESO write actions are local-cluster only in v1',
          },
        }, status: 501),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final state = sub.read() as ForceSyncFailure;
      expect(state.alreadyRefreshing, isFalse);
      expect(state.message, contains('local cluster'));
    });

    test('503 surfaces ESO-not-detected copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        _path,
        (_) => _json({
          'error': {'code': 503, 'message': 'ESO not detected'},
        }, status: 503),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final state = sub.read() as ForceSyncFailure;
      expect(state.message, contains('ESO'));
    });

    test('403 surfaces permission copy with the resource path', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        _path,
        (_) => _json({
          'error': {'code': 403, 'message': 'access denied'},
        }, status: 403),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final state = sub.read() as ForceSyncFailure;
      expect(state.message, contains('permission'));
      expect(state.message, contains('production/db-credentials'));
    });

    test('404 surfaces deleted-resource copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'POST',
        _path,
        (_) => _json({
          'error': {'code': 404, 'message': 'external secret not found'},
        }, status: 404),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);
      await ctrl.forceSync();
      await _settle();

      final state = sub.read() as ForceSyncFailure;
      expect(state.message, contains('not found'));
    });
  });

  group('ForceSyncController.cluster-pin race', () {
    test('postEmission switch emits failure (no invalidate on wrong slot)',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'POST',
        _path,
        status: 202,
        body: {'data': {'status': 'force-syncing'}},
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl = container.read(forceSyncControllerProvider(_key).notifier);

      // Switch the active cluster mid-flight by mutating the
      // ActiveClusterController immediately after firing.
      final future = ctrl.forceSync();
      container
          .read(activeClusterProvider.notifier)
          .setCluster('prod');
      await future;
      await _settle();

      final state = sub.read();
      expect(state, isA<ForceSyncFailure>(),
          reason: 'postEmission cluster-pin re-check must veto the success '
              'so the snackbar does not lie about which cluster received '
              'the sync');
      final msg = (state as ForceSyncFailure).message;
      expect(msg.toLowerCase(), contains('cluster'));
    });
  });
}
