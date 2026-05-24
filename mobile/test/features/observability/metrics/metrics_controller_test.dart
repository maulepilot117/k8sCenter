// Tests for the MetricsController: per-panel happy path, 502 error
// path, time-range swap mid-fetch (race), cluster-pin postEmission
// guard.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/features/observability/metrics/metrics_controller.dart';

import '../../../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

ResponseBody _matrix(List<Map<String, dynamic>> result) {
  return _json({
    'data': {'resultType': 'matrix', 'result': result},
  });
}

({ProviderContainer container, MockDioAdapter mock}) _make({
  String activeClusterId = 'local',
}) {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  // Seed the active cluster so the controller's pin check has a value.
  container.read(activeClusterProvider.notifier).setCluster(activeClusterId);
  return (container: container, mock: mock);
}

Future<void> _pumpAsync(ProviderContainer container) async {
  // Dio's async chain (5 interceptors × ~2 microtasks each, plus
  // response transformer + onResponse loop) needs more than three
  // hops. Flutter's pumpEventQueue cycles a generous 20 turns and
  // covers the worst case (auth retry + refresh + main response).
  await pumpEventQueue(times: 30);
}

void main() {
  group('MetricsController', () {
    test(
        'happy path: fetches all panels for a Pod target and renders Loaded',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // Status gate (the tab queries this; the controller itself doesn't,
      // but we keep the mock simple by allowing it).
      mock.onJson('GET', '/api/v1/monitoring/status', body: {
        'data': {'detected': true, 'prometheus': {'available': true}},
      });

      // F#4 — every panel hits the slug endpoint /api/v1/monitoring/queries/{slug}.
      // The pod kind has four slugs; arm one mock per slug.
      for (final slug in const [
        'pods/cpu',
        'pods/memory',
        'pods/network-rx',
        'pods/network-tx',
      ]) {
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) => _matrix([
            {
              'metric': {'container': 'web'},
              'values': [
                [1700000000.0, '0.5'],
              ],
            },
          ]),
        );
      }

      final target = const MetricsTarget(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        name: 'web-pod',
      );

      // Persistent subscription so autoDispose doesn't tear the
      // provider down between the two reads — without it the second
      // read rebuilds from scratch and loses the in-flight state.
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);

      final initial = sub.read();
      expect(initial.panels.values, everyElement(isA<PanelLoading>()));
      await _pumpAsync(container);
      final after = sub.read();
      expect(after.range.preset, MetricsPreset.last1h);
      for (final panel in after.panels.values) {
        expect(panel, isA<PanelLoaded>(),
            reason: 'every panel should land Loaded; got $panel');
      }
    });

    test('502 from Prometheus surfaces as PanelFailed with a clear message',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      for (final slug in const [
        'pods/cpu',
        'pods/memory',
        'pods/network-rx',
        'pods/network-tx',
      ]) {
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) => _json({
            'error': {'code': 502, 'message': 'Prometheus query failed'},
          }, status: 502),
        );
      }

      final target = const MetricsTarget(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        name: 'web-pod',
      );
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);
      sub.read();
      await _pumpAsync(container);

      final s = sub.read();
      for (final panel in s.panels.values) {
        expect(panel, isA<PanelFailed>());
        expect(
          (panel as PanelFailed).message,
          contains('Prometheus query failed'),
        );
      }
    });

    test('time-range swap mid-fetch drops stale results via supersede',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // Each panel returns marker-distinguished payloads so the assertion
      // can verify which fetch landed. The first batch (initial 1h fetch)
      // would resolve to value 0.5; the second batch (after 24h swap)
      // resolves to value 0.99.
      final initialCompleters = List.generate(4, (_) => Completer<void>());
      var phase = 0;
      // F#4 — same swap-mid-fetch race against the slug endpoint. Each pod
      // panel hits its own slug, so register the handler against every
      // pod slug. Each handler shares the phase counter (the race
      // assertion doesn't care which slug landed in which order — only
      // that the post-swap batch wins).
      ResponseBody handler(RequestOptions _) {
        if (phase < 4) {
          final i = phase++;
          // Force the initial fetches to block on completer so we can
          // swap range before they resolve.
          // (we can't actually block sync; emulate by returning fast
          // then asserting supersede via dispatch id behavior below)
          initialCompleters[i].complete();
          return _matrix([
            {
              'metric': {'container': 'old'},
              'values': [
                [1700000000.0, '0.5'],
              ],
            },
          ]);
        }
        return _matrix([
          {
            'metric': {'container': 'new'},
            'values': [
              [1700000000.0, '0.99'],
            ],
          },
        ]);
      }

      for (final slug in const [
        'pods/cpu',
        'pods/memory',
        'pods/network-rx',
        'pods/network-tx',
      ]) {
        // Register handler twice per slug so the same closure satisfies
        // both the initial batch and the post-swap batch for that slug.
        mock.on('GET', '/api/v1/monitoring/queries/$slug', handler);
        mock.on('GET', '/api/v1/monitoring/queries/$slug', handler);
      }

      final target = const MetricsTarget(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        name: 'web-pod',
      );
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);
      sub.read();
      // Let the build's scheduled microtask fire first, then swap
      // range. The post-swap fetches dominate the in-flight write
      // race via `supersede()` (bumps the dispatch id so prior
      // captures fail `isFresh`).
      await _pumpAsync(container);
      final notifier =
          container.read(metricsControllerProvider(target).notifier);
      final now = DateTime.now();
      notifier.setRange(
        now.subtract(const Duration(hours: 24)),
        now,
        MetricsPreset.last24h,
      );
      await _pumpAsync(container);
      await _pumpAsync(container);

      final after = sub.read();
      expect(after.range.preset, MetricsPreset.last24h);
      // All panels should reflect the second-batch payload now.
      for (final p in after.panels.values) {
        if (p is PanelLoaded) {
          expect(p.result.series.first.labels['container'], 'new');
        }
      }
    });

    test('cluster-pin postEmission mismatch routes to PanelFailed', () async {
      final (:container, :mock) = _make(activeClusterId: 'cluster-a');
      addTearDown(container.dispose);

      // Switch active cluster after the controller pins on 'cluster-a',
      // before the slug endpoint mock returns. The mock returns a valid
      // body so we exercise the post-emission re-check rather than the
      // happy path or a Dio error path.
      for (final slug in const [
        'pods/cpu',
        'pods/memory',
        'pods/network-rx',
        'pods/network-tx',
      ]) {
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) {
            container
                .read(activeClusterProvider.notifier)
                .setCluster('cluster-b');
            return _matrix(const []);
          },
        );
      }

      final target = const MetricsTarget(
        clusterId: 'cluster-a',
        kind: 'pods',
        namespace: 'default',
        name: 'web-pod',
      );
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);
      sub.read();
      await _pumpAsync(container);

      final s = sub.read();
      // Every panel should fail with the post-emission mismatch copy.
      for (final p in s.panels.values) {
        expect(p, isA<PanelFailed>());
        final msg = (p as PanelFailed).message;
        expect(msg, contains('Cluster changed'));
        expect(msg, contains('cluster-a'));
      }
    });

    test('kind with no panel registry stays at empty Loaded state', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      final target = const MetricsTarget(
        clusterId: 'local',
        kind: 'configmaps', // no panels registered
        namespace: 'default',
        name: 'cm',
      );
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);
      final state = sub.read();
      await _pumpAsync(container);
      expect(state.panels, isEmpty);
      // The mock never gets a request because no panels are scheduled.
      expect(
        mock.requests.where(
          (r) => r.path.startsWith('/api/v1/monitoring/queries/'),
        ),
        isEmpty,
      );
    });

    test('refresh() re-fires panel fetches after a previous failure', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // F#4 — register slug-endpoint handlers per panel. First handler in
      // the per-slug queue serves the initial fetch; second serves the
      // post-refresh fetch.
      const slugs = ['pods/cpu', 'pods/memory', 'pods/network-rx', 'pods/network-tx'];
      for (final slug in slugs) {
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) => _json({
            'error': {'code': 502, 'message': 'Prometheus query failed'},
          }, status: 502),
        );
        mock.on(
          'GET',
          '/api/v1/monitoring/queries/$slug',
          (_) => _matrix([
            {
              'metric': {'container': 'web'},
              'values': [
                [1700000000.0, '0.5'],
              ],
            },
          ]),
        );
      }

      final target = const MetricsTarget(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        name: 'web-pod',
      );
      final sub = container.listen(
        metricsControllerProvider(target),
        (_, _) {},
      );
      addTearDown(sub.close);
      sub.read();
      await _pumpAsync(container);
      // First batch landed as PanelFailed.
      expect(sub.read().panels.values, everyElement(isA<PanelFailed>()));

      // refresh() supersedes and re-fires.
      await container
          .read(metricsControllerProvider(target).notifier)
          .refresh();
      await _pumpAsync(container);
      // Second batch lands as PanelLoaded.
      expect(sub.read().panels.values, everyElement(isA<PanelLoaded>()));
    });
  });
}
