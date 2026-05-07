// FCM device registration. Runs once per authenticated launch and
// re-runs on token rotation. Conditional Firebase init: when the
// platform config (google-services.json / GoogleService-Info.plist) is
// missing, Firebase.initializeApp() throws, we log a one-line warning,
// and skip FCM entirely. The rest of the app keeps working — push is
// optional, the in-app notification feed is the primary channel.
//
// Backend contract (per PR-0):
//   POST /api/v1/notifications/devices
//     body: {"deviceToken": "<fcm token>", "platform": "ios"|"android"}
//   The handler upserts on (user_id, device_token); rotation produces a
//   new row, an old-token sweep is a deferred follow-up.

import 'dart:async';
import 'dart:io' show Platform;

import 'package:dio/dio.dart';
import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';

class FcmRegistration {
  FcmRegistration(this._dio);

  final Dio _dio;
  StreamSubscription<String>? _tokenRefreshSub;
  bool _initialized = false;

  /// Called once after auth completes. Returns silently when Firebase
  /// init fails (missing platform config, sandboxed CI run, etc.).
  Future<void> ensureRegistered() async {
    if (_initialized) return;
    if (!_supportedPlatform) return;

    try {
      await Firebase.initializeApp();
    } catch (e) {
      // Most common cause: google-services.json / GoogleService-Info.plist
      // not provided by the operator yet (PR-1g handoff). Don't crash
      // the app — push is optional.
      debugPrint('[fcm] Firebase init skipped: $e');
      return;
    }

    final messaging = FirebaseMessaging.instance;
    final settings = await messaging.requestPermission(
      alert: true,
      badge: true,
      sound: true,
    );
    if (settings.authorizationStatus == AuthorizationStatus.denied) {
      debugPrint('[fcm] notification permission denied — skipping');
      return;
    }

    final token = await messaging.getToken();
    if (token != null && token.isNotEmpty) {
      await _registerWithBackend(token);
    }

    _tokenRefreshSub?.cancel();
    _tokenRefreshSub = messaging.onTokenRefresh.listen((newToken) async {
      await _registerWithBackend(newToken);
    });

    _initialized = true;
  }

  Future<void> _registerWithBackend(String deviceToken) async {
    if (!_supportedPlatform) return;
    final platform = Platform.isIOS ? 'ios' : 'android';
    try {
      await _dio.post<dynamic>(
        '/api/v1/notifications/devices',
        data: {'deviceToken': deviceToken, 'platform': platform},
      );
    } on DioException catch (e) {
      final err = e.error;
      // 4xx surface as ApiError but we don't want to crash the auth
      // bootstrap on push registration failure — log and move on.
      debugPrint(
        '[fcm] register failed: ${err is ApiError ? err.message : e.message}',
      );
    }
  }

  bool get _supportedPlatform {
    // Web/desktop builds skip FCM entirely. M1 ships iOS + Android only.
    if (kIsWeb) return false;
    return Platform.isIOS || Platform.isAndroid;
  }

  Future<void> dispose() async {
    await _tokenRefreshSub?.cancel();
    _tokenRefreshSub = null;
    _initialized = false;
  }
}

final fcmRegistrationProvider = Provider<FcmRegistration>((ref) {
  final fcm = FcmRegistration(ref.watch(dioProvider));
  ref.onDispose(fcm.dispose);
  return fcm;
});
