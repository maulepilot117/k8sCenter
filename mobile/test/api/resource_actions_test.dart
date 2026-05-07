// Verifies the action verb wire format + RBAC filtering. Mirrors the
// invariants in `frontend/lib/action-handlers.ts`:
//   - scale POSTs `{replicas: <int>}` to /scale
//   - restart POSTs to /restart with no body fields
//   - suspend POSTs `{suspend: <bool>}` to /suspend
//   - trigger POSTs to /trigger and reads the new Job's name
//   - delete uses HTTP DELETE on the base path (no body)
//   - rollback POSTs `{revision: <int>}` to /rollback
//   - getVisibleActions filters to verbs the RBAC summary allows.
//
// Backend contract: `backend/internal/server/routes.go:758-762` and
// `backend/internal/k8s/resources/actions.go`.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/resource_actions.dart';
import 'package:kubecenter/auth/user.dart';

import '../support/mock_dio_adapter.dart';

ResponseBody _ok([Map<String, dynamic> body = const {'data': <String, dynamic>{}}]) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    200,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

Dio _dio(MockDioAdapter mock) {
  final dio = Dio(BaseOptions(baseUrl: 'http://test'));
  dio.httpClientAdapter = mock;
  return dio;
}

Map<String, dynamic> _resource({
  String name = 'app',
  bool? specSuspend,
  String? ownerKind,
  String? ownerName,
}) {
  final metadata = <String, dynamic>{
    'name': name,
    'namespace': 'default',
  };
  if (ownerKind != null) {
    metadata['ownerReferences'] = [
      {'kind': ownerKind, 'name': ownerName ?? 'parent'},
    ];
  }
  final spec = <String, dynamic>{};
  if (specSuspend != null) spec['suspend'] = specSuspend;
  return {
    'kind': 'Deployment',
    'metadata': metadata,
    'spec': spec,
  };
}

void main() {
  group('executeAction request shapes', () {
    test('scale POSTs {replicas} to /scale', () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/deployments/default/app/scale',
          (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.scale,
        kind: 'deployments',
        namespace: 'default',
        name: 'app',
        params: const {'replicas': 5},
      );
      expect(res.message, 'Scaled to 5 replicas');
      expect(mock.requests.last.data, {'replicas': 5});
    });

    test('scale rejects negative replicas before hitting network', () async {
      final mock = MockDioAdapter();
      await expectLater(
        () => executeAction(
          dio: _dio(mock),
          id: ActionId.scale,
          kind: 'deployments',
          namespace: 'default',
          name: 'app',
          params: const {'replicas': -1},
        ),
        throwsA(isA<Exception>()),
      );
      expect(mock.requests, isEmpty);
    });

    test('restart POSTs to /restart with no body', () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/deployments/default/app/restart',
          (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.restart,
        kind: 'deployments',
        namespace: 'default',
        name: 'app',
      );
      expect(res.message, 'Rolling restart initiated');
    });

    test('delete uses HTTP DELETE on the base path', () async {
      final mock = MockDioAdapter();
      mock.on('DELETE', '/api/v1/resources/pods/default/p1', (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.delete,
        kind: 'pods',
        namespace: 'default',
        name: 'p1',
      );
      expect(res.message, 'Deleted p1');
      expect(mock.requests.last.method, 'DELETE');
    });

    test('suspend POSTs {suspend: true}', () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/cronjobs/default/c1/suspend',
          (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.suspend,
        kind: 'cronjobs',
        namespace: 'default',
        name: 'c1',
        params: const {'suspend': true},
      );
      expect(res.message, 'Suspended');
      expect(mock.requests.last.data, {'suspend': true});
    });

    test('suspend with false returns "Resumed"', () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/cronjobs/default/c1/suspend',
          (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.suspend,
        kind: 'cronjobs',
        namespace: 'default',
        name: 'c1',
        params: const {'suspend': false},
      );
      expect(res.message, 'Resumed');
    });

    test('trigger reads new Job name from response.data.metadata.name',
        () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/cronjobs/default/c1/trigger', (_) {
        return _ok({
          'data': {
            'metadata': {'name': 'c1-29384'}
          }
        });
      });
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.trigger,
        kind: 'cronjobs',
        namespace: 'default',
        name: 'c1',
      );
      expect(res.message, 'Job "c1-29384" created');
      expect(res.createdName, 'c1-29384');
    });

    test('rollback POSTs {revision} to /rollback', () async {
      final mock = MockDioAdapter();
      mock.on('POST', '/api/v1/resources/deployments/default/app/rollback',
          (_) => _ok());
      final res = await executeAction(
        dio: _dio(mock),
        id: ActionId.rollback,
        kind: 'deployments',
        namespace: 'default',
        name: 'app',
        params: const {'revision': 3},
      );
      expect(res.message, 'Rolled back to revision 3');
      expect(mock.requests.last.data, {'revision': 3});
    });
  });

  group('getActionMeta', () {
    test('delete carries typeToConfirm = name + danger=true', () {
      final meta = getActionMeta(ActionId.delete, _resource(name: 'my-pod'));
      expect(meta.label, 'Delete');
      expect(meta.danger, isTrue);
      expect(meta.typeToConfirm, 'my-pod');
      expect(meta.confirmMessage, contains('my-pod'));
    });

    test('delete on managed resource mentions owner kind+name', () {
      final meta = getActionMeta(
        ActionId.delete,
        _resource(name: 'p1', ownerKind: 'ReplicaSet', ownerName: 'rs-abc'),
      );
      expect(meta.confirmMessage, contains('ReplicaSet/rs-abc'));
      expect(meta.confirmMessage, contains('recreated after deletion'));
    });

    test('suspend label flips when spec.suspend is true', () {
      final paused = getActionMeta(
        ActionId.suspend,
        _resource(specSuspend: true),
      );
      expect(paused.label, 'Resume');
      final running = getActionMeta(
        ActionId.suspend,
        _resource(specSuspend: false),
      );
      expect(running.label, 'Suspend');
    });

    test('restart uses simple confirm, not destructive', () {
      final meta = getActionMeta(ActionId.restart, _resource());
      expect(meta.confirm, 'confirm');
      expect(meta.danger, isFalse);
      expect(meta.typeToConfirm, isNull);
    });
  });

  group('getVisibleActions', () {
    final adminRbac = RBACSummary.fromJson({
      'clusterScoped': const <String, dynamic>{},
      'namespaces': {
        'default': {
          'deployments': ['*'],
          'pods': ['*'],
        },
      },
    });

    final readOnlyRbac = RBACSummary.fromJson({
      'namespaces': {
        'default': {
          'deployments': ['get', 'list', 'watch'],
        },
      },
    });

    final updateOnlyRbac = RBACSummary.fromJson({
      'namespaces': {
        'default': {
          'deployments': ['get', 'list', 'update'],
        },
      },
    });

    test('admin sees every action declared for the kind', () {
      final visible =
          getVisibleActions('deployments', 'default', adminRbac);
      expect(
        visible,
        containsAll(<ActionId>[
          ActionId.scale,
          ActionId.restart,
          ActionId.delete,
          ActionId.rollback,
        ]),
      );
    });

    test('read-only RBAC hides every write action', () {
      final visible =
          getVisibleActions('deployments', 'default', readOnlyRbac);
      expect(visible, isEmpty);
    });

    test('update-only RBAC shows scale/restart/rollback but not delete', () {
      final visible =
          getVisibleActions('deployments', 'default', updateOnlyRbac);
      expect(visible, contains(ActionId.scale));
      expect(visible, contains(ActionId.restart));
      expect(visible, contains(ActionId.rollback));
      expect(visible, isNot(contains(ActionId.delete)));
    });

    test('null RBAC returns all actions (optimistic)', () {
      final visible = getVisibleActions('pods', 'default', null);
      expect(visible, [ActionId.delete]);
    });

    test('unknown kind returns empty list', () {
      final visible =
          getVisibleActions('mysteries', 'default', adminRbac);
      expect(visible, isEmpty);
    });
  });
}
