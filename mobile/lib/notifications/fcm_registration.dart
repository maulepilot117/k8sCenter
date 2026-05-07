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
import 'deep_link_handler.dart';

/// Holds a parsed deep-link URI captured from a notification tap that
/// arrived before the router was ready (cold-start tap on a terminated
/// app, or background-resume before bootstrap completes). The router's
/// post-auth consumer drains this once `AuthAuthenticated` lands.
final pendingDeepLinkProvider = StateProvider<Uri?>((ref) => null);

class FcmRegistration {
  FcmRegistration(this._dio, this._ref);

  final Dio _dio;
  final Ref _ref;
  StreamSubscription<String>? _tokenRefreshSub;
  StreamSubscription<RemoteMessage>? _openedAppSub;
  bool _initialized = false;
  static const _handler = DeepLinkHandler();

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

    // Cold-start: app launched by tapping a notification while
    // terminated. Capture the link target into pendingDeepLinkProvider;
    // the router's post-auth consumer drains it.
    final initialMessage = await messaging.getInitialMessage();
    if (initialMessage != null) {
      _capturePendingLink(initialMessage);
    }

    // Background-resume: app already running in background, tapped.
    _openedAppSub?.cancel();
    _openedAppSub =
        FirebaseMessaging.onMessageOpenedApp.listen(_capturePendingLink);

    _initialized = true;
  }

  /// Extract a deep link from a push message and stash it in the
  /// pending-link state. FCM payload contract:
  ///   data: {
  ///     "deeplink": "k8scenter://cluster/local/Pod/default/web-7d4f"
  ///     // OR resourceKind/resourceNamespace/resourceName triple
  ///   }
  /// Both shapes are supported so the backend dispatcher can pick
  /// whichever matches the channel template.
  void _capturePendingLink(RemoteMessage msg) {
    final data = msg.data;
    final raw = data['deeplink'] as String?;
    Uri? candidate;
    if (raw != null && raw.isNotEmpty) {
      candidate = Uri.tryParse(raw);
    } else {
      final kind = data['resourceKind'] as String?;
      final ns = data['resourceNamespace'] as String? ?? '';
      final name = data['resourceName'] as String?;
      final cluster = data['clusterId'] as String? ?? 'local';
      if (kind != null && name != null && kind.isNotEmpty && name.isNotEmpty) {
        final segments = ns.isEmpty
            ? '$cluster/$kind/$name'
            : '$cluster/$kind/$ns/$name';
        candidate = Uri.parse('k8scenter://cluster/$segments');
      }
    }
    if (candidate == null) return;
    final parsed = _handler.parse(candidate);
    if (!parsed.isValid) {
      debugPrint('[fcm] pending link rejected: $candidate');
      return;
    }
    _ref.read(pendingDeepLinkProvider.notifier).state = candidate;
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
    await _openedAppSub?.cancel();
    _tokenRefreshSub = null;
    _openedAppSub = null;
    _initialized = false;
  }
}

final fcmRegistrationProvider = Provider<FcmRegistration>((ref) {
  final fcm = FcmRegistration(ref.watch(dioProvider), ref);
  ref.onDispose(fcm.dispose);
  return fcm;
});
