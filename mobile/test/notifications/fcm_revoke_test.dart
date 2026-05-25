// Tests for FcmRegistration.revokeDeviceByToken — the inner network unit
// of the audit-finding-P2-9 logout-revoke flow.
//
// We exercise revokeDeviceByToken directly (bypassing FirebaseMessaging
// platform channels) so the test suite stays green on the headless Dart
// runner where Firebase is never initialised. The outer revokeCurrentDevice
// wraps this same call after pulling the token from
// FirebaseMessaging.instance.getToken().

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/notifications/fcm_registration.dart';

import '../support/mock_dio_adapter.dart';

({FcmRegistration fcm, MockDioAdapter mock, ProviderContainer container})
    _makeFcm() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
    ],
  );
  // Attach the adapter BEFORE pulling fcmRegistrationProvider so the
  // FcmRegistration instance the provider builds uses the mock-backed Dio.
  container.read(dioProvider).httpClientAdapter = mock;
  final fcm = container.read(fcmRegistrationProvider);
  return (fcm: fcm, mock: mock, container: container);
}

void main() {
  test(
      'revokeDeviceByToken: lists devices, deletes the one whose deviceToken '
      'matches, and returns true', () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/notifications/devices',
      body: {
        'data': [
          {
            'id': 'dev-other',
            'userId': 'u1',
            'deviceToken': 'other-token',
            'platform': 'ios',
          },
          {
            'id': 'dev-mine',
            'userId': 'u1',
            'deviceToken': 'my-token',
            'platform': 'android',
          },
        ],
      },
    );
    mock.onJson(
      'DELETE',
      '/api/v1/notifications/devices/dev-mine',
      body: <String, dynamic>{},
      status: 204,
    );

    final ok = await fcm.revokeDeviceByToken('my-token');

    expect(ok, isTrue);
    expect(
      mock.requests.map((r) => '${r.method} ${r.path}').toList(),
      [
        'GET /api/v1/notifications/devices',
        'DELETE /api/v1/notifications/devices/dev-mine',
      ],
    );
  });

  test(
      'revokeDeviceByToken: when no device matches the token, skips DELETE '
      'and returns false', () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/notifications/devices',
      body: {
        'data': [
          {
            'id': 'dev-other',
            'userId': 'u1',
            'deviceToken': 'other-token',
            'platform': 'ios',
          },
        ],
      },
    );

    final ok = await fcm.revokeDeviceByToken('missing-token');

    expect(ok, isFalse);
    expect(
      mock.requests.map((r) => r.method),
      ['GET'],
      reason: 'no DELETE should be issued when token has no matching device',
    );
  });

  test(
      'revokeDeviceByToken: empty devices list returns false without DELETE',
      () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/notifications/devices',
      body: {'data': <Map<String, dynamic>>[]},
    );

    final ok = await fcm.revokeDeviceByToken('any');

    expect(ok, isFalse);
    expect(
      mock.requests.where((r) => r.method == 'DELETE'),
      isEmpty,
    );
  });

  test(
      'revokeDeviceByToken: GET error swallowed (returns false) so logout '
      'can still proceed', () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);
    // No handler registered → MockDioAdapter returns 404, which Dio
    // surfaces as a DioException.

    final ok = await fcm.revokeDeviceByToken('my-token');

    expect(ok, isFalse);
  });

  test(
      'revokeDeviceByToken: DELETE error swallowed (returns false) when '
      'list succeeds but server rejects the delete', () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/notifications/devices',
      body: {
        'data': [
          {
            'id': 'dev-mine',
            'userId': 'u1',
            'deviceToken': 'my-token',
            'platform': 'android',
          },
        ],
      },
    );
    mock.onJson(
      'DELETE',
      '/api/v1/notifications/devices/dev-mine',
      status: 500,
      body: {
        'error': {'code': 500, 'message': 'kaboom'},
      },
    );

    final ok = await fcm.revokeDeviceByToken('my-token');

    expect(ok, isFalse);
  });

  test(
      'revokeDeviceByToken: malformed list response (data not a list) returns '
      'false without DELETE', () async {
    final (:fcm, :mock, :container) = _makeFcm();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/notifications/devices',
      body: {'data': 'not-a-list'},
    );

    final ok = await fcm.revokeDeviceByToken('my-token');

    expect(ok, isFalse);
    expect(mock.requests.where((r) => r.method == 'DELETE'), isEmpty);
  });
}
