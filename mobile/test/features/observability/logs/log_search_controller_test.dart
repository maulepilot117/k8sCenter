// Tests for LogSearchController — happy path, 403 namespace missing,
// 400 backend tokenizer reject, 4096-char client gate, volume best-
// effort error fallthrough, cluster-pin postEmission, race protection.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/features/observability/logs/log_search_controller.dart';
import 'package:kubecenter/widgets/time_range_picker.dart';

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

ResponseBody _streams(List<Map<String, dynamic>> streams) {
  return _json({
    'data': {
      'data': {
        'resultType': 'streams',
        'result': streams,
      },
    },
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
  container.read(activeClusterProvider.notifier).setCluster(activeClusterId);
  return (container: container, mock: mock);
}

Future<void> _pumpAsync(ProviderContainer container) async {
  await pumpEventQueue(times: 30);
}

LogSearchParams _params({
  String? namespace = 'app',
  String query = '{namespace="app"}',
  TimePreset preset = TimePreset.last1h,
}) {
  return LogSearchParams(
    namespace: namespace,
    query: query,
    range: timeRangeFromPreset(preset),
  );
}

void main() {
  group('LogSearchController', () {
    test('initial state is idle (no panels visible)', () {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      final state = container.read(logSearchControllerProvider('local'));
      expect(state.result, isA<LogQueryIdle>());
      expect(state.volume, isA<LogVolumeHidden>());
      expect(state.params, isNull);
      // No requests fire until submit is called.
      expect(mock.requests, isEmpty);
    });

    test('happy path: submit fires query + volume in parallel', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/logs/query', body: {
        'data': {
          'data': {
            'resultType': 'streams',
            'result': [
              {
                'stream': {'namespace': 'app', 'pod': 'web', 'container': 'web'},
                'values': [
                  ['1700000000000000000', 'hello world'],
                ],
              },
            ],
          },
        },
      });
      mock.onJson('GET', '/api/v1/logs/volume', body: {
        'data': {
          'data': {
            'resultType': 'matrix',
            'result': [
              {
                'metric': {'namespace': 'app'},
                'values': [
                  [1700000000.0, '1'],
                ],
              },
            ],
          },
        },
      });

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);
      await container
          .read(logSearchControllerProvider('local').notifier)
          .submit(_params());
      await _pumpAsync(container);

      final s = sub.read();
      expect(s.result, isA<LogQueryLoaded>());
      expect((s.result as LogQueryLoaded).result.lines, hasLength(1));
      expect(s.volume, isA<LogVolumeLoaded>());
    });

    test('4096-char gate marks result as failed without firing requests',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);

      final huge = '{${'x' * 5000}="y"}';
      await container
          .read(logSearchControllerProvider('local').notifier)
          .submit(_params(query: huge));
      await _pumpAsync(container);

      final s = sub.read();
      expect(s.result, isA<LogQueryFailed>());
      expect(
        (s.result as LogQueryFailed).message,
        contains('exceeds'),
      );
      // No HTTP calls — the gate fires before submit issues the
      // first request.
      expect(mock.requests, isEmpty);
    });

    test('403 missing-namespace surfaces operator-facing copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/query',
        (_) => _json({
          'error': {
            'code': 403,
            'message': 'namespace access denied',
            'detail': 'namespace parameter required for non-admin users',
          },
        }, status: 403),
      );
      // Volume mirrors the same error path.
      mock.on(
        'GET',
        '/api/v1/logs/volume',
        (_) => _json({
          'error': {'code': 403, 'message': 'namespace access denied'},
        }, status: 403),
      );

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);

      await container
          .read(logSearchControllerProvider('local').notifier)
          .submit(_params(namespace: null, query: '{job="kubelet"}'));
      await _pumpAsync(container);

      final s = sub.read();
      expect(s.result, isA<LogQueryFailed>());
      expect(
        (s.result as LogQueryFailed).message,
        contains('Namespace required'),
      );
      // Volume failure hides the panel rather than surfacing a
      // duplicate banner.
      expect(s.volume, isA<LogVolumeHidden>());
    });

    test('400 backend tokenizer reject surfaces verbatim', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/query',
        (_) => _json({
          'error': {
            'code': 400,
            'message': 'parse error: unexpected token at position 5',
          },
        }, status: 400),
      );
      mock.on(
        'GET',
        '/api/v1/logs/volume',
        (_) => _json({
          'error': {'code': 400, 'message': 'parse error'},
        }, status: 400),
      );

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);

      await container
          .read(logSearchControllerProvider('local').notifier)
          .submit(_params(query: '{this is not logql}'));
      await _pumpAsync(container);

      final s = sub.read();
      expect(s.result, isA<LogQueryFailed>());
      // Verbatim backend message — operators composing LogQL need
      // the original error to fix the query.
      expect(
        (s.result as LogQueryFailed).message,
        contains('parse error: unexpected token at position 5'),
      );
    });

    test('volume 5xx hides panel without failing the results panel',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/logs/query', body: {
        'data': {
          'data': {'resultType': 'streams', 'result': <Object>[]},
        },
      });
      mock.on(
        'GET',
        '/api/v1/logs/volume',
        (_) => _json({
          'error': {'code': 502, 'message': 'volume backend timeout'},
        }, status: 502),
      );

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);

      await container
          .read(logSearchControllerProvider('local').notifier)
          .submit(_params());
      await _pumpAsync(container);

      final s = sub.read();
      // Results panel succeeds (with an empty result set).
      expect(s.result, isA<LogQueryLoaded>());
      expect((s.result as LogQueryLoaded).result.isEmpty, isTrue);
      // Volume panel is hidden — best-effort surface.
      expect(s.volume, isA<LogVolumeHidden>());
    });

    test('cluster-pin postEmission mismatch routes result to LogQueryFailed',
        () async {
      final (:container, :mock) = _make(activeClusterId: 'cluster-a');
      addTearDown(container.dispose);

      mock.on('GET', '/api/v1/logs/query', (_) {
        container
            .read(activeClusterProvider.notifier)
            .setCluster('cluster-b');
        return _streams(const []);
      });
      mock.onJson('GET', '/api/v1/logs/volume', body: {
        'data': {
          'data': {'resultType': 'matrix', 'result': <Object>[]},
        },
      });

      // Persistent subscription so the autoDispose family doesn't
      // tear the controller down between submit() and the post-pump
      // read — without this, the await rebuilds the provider and
      // wipes the failure state.
      final sub = container.listen(
        logSearchControllerProvider('cluster-a'),
        (_, _) {},
      );
      addTearDown(sub.close);

      await container
          .read(logSearchControllerProvider('cluster-a').notifier)
          .submit(_params());
      await _pumpAsync(container);

      final s = sub.read();
      expect(s.result, isA<LogQueryFailed>());
      final msg = (s.result as LogQueryFailed).message;
      expect(msg, contains('Cluster changed'));
      expect(msg, contains('cluster-a'));
    });

    test('refresh() replays the last submitted params', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // First submission fails with 502; refresh replays and succeeds.
      mock.on(
        'GET',
        '/api/v1/logs/query',
        (_) => _json({
          'error': {'code': 502, 'message': 'Loki upstream timeout'},
        }, status: 502),
      );
      mock.on(
        'GET',
        '/api/v1/logs/volume',
        (_) => _json({
          'error': {'code': 502, 'message': 'volume backend timeout'},
        }, status: 502),
      );
      // Second batch (after refresh) returns success.
      mock.on('GET', '/api/v1/logs/query', (_) => _streams([
            {
              'stream': {'namespace': 'app'},
              'values': [
                ['1700000000000000000', 'recovered'],
              ],
            },
          ]));
      mock.onJson('GET', '/api/v1/logs/volume', body: {
        'data': {
          'data': {'resultType': 'matrix', 'result': <Object>[]},
        },
      });

      final sub = container.listen(
        logSearchControllerProvider('local'),
        (_, _) {},
      );
      addTearDown(sub.close);

      final notifier =
          container.read(logSearchControllerProvider('local').notifier);
      await notifier.submit(_params());
      await _pumpAsync(container);
      expect(sub.read().result, isA<LogQueryFailed>());

      await notifier.refresh();
      await _pumpAsync(container);
      final s = sub.read();
      expect(s.result, isA<LogQueryLoaded>());
      expect((s.result as LogQueryLoaded).result.lines.first.line,
          'recovered');
    });

    test('refresh() before any submit is a no-op', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      await container
          .read(logSearchControllerProvider('local').notifier)
          .refresh();
      await _pumpAsync(container);
      // No requests; state stays Idle.
      expect(mock.requests, isEmpty);
      expect(
        container.read(logSearchControllerProvider('local')).result,
        isA<LogQueryIdle>(),
      );
    });
  });

  group('chooseVolumeStep', () {
    test('1h range picks 30s (densest step within cap)', () {
      // 3600 / 30 = 120 buckets, exactly at the 120 cap. Picker
      // returns the smallest step that fits so the histogram is as
      // dense as the cap allows.
      expect(chooseVolumeStep(3600), '30s');
    });

    test('24h range picks 15m', () {
      // 86400 / 900 = 96 buckets, under cap.
      expect(chooseVolumeStep(86400), '15m');
    });

    test('7d range picks 6h', () {
      // 604800 / 3600 = 168 buckets > 120; falls to 6h (28 buckets).
      // The step picker walks the allowlist looking for the first
      // step that fits, so the answer is the smallest valid step.
      expect(chooseVolumeStep(604800), '6h');
    });

    test('30d range picks 6h (still within cap)', () {
      // 2592000 / 21600 = 120 buckets, exactly at cap. 1d would
      // suffice too but 6h gives more resolution.
      expect(chooseVolumeStep(30 * 86400), '6h');
    });

    test('range too wide to fit in 6h falls back to 1d cap', () {
      // 200 days × 86400s. 1d step → 200 buckets, over cap, but the
      // picker falls through to the last entry rather than throwing.
      expect(chooseVolumeStep(200 * 86400), '1d');
    });

    test('short 15m range uses 15s', () {
      // 900 / 15 = 60 buckets.
      expect(chooseVolumeStep(900), '15s');
    });
  });
}
