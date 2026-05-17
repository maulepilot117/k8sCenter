// Shared FCM helper functions extracted from feature-specific code.
//
// [requestFcmPermission] is the canonical one-shot permission request used
// by the onboarding tour (NotificationsCard). It is separate from
// [FcmRegistration.ensureRegistered], which runs post-auth and registers
// the device token with the backend. Both call the same Firebase singleton,
// so the platform-level permission dialog is shown at most once per install.
//
// Any failure (missing google-services.json / GoogleService-Info.plist,
// web or desktop build, sandboxed CI) is swallowed and logged — the caller
// always advances regardless of the user's permission choice.

import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart' show debugPrint, kIsWeb;

/// Surfaces the OS push-permission prompt by initialising Firebase (if not
/// already done) and calling [FirebaseMessaging.requestPermission].
///
/// Silently swallows all errors so the onboarding tour can always advance
/// regardless of FCM availability. Use [FcmRegistration.ensureRegistered]
/// for the full post-auth registration flow that also sends the token to
/// the backend.
Future<void> requestFcmPermission() async {
  if (kIsWeb) return;
  try {
    if (!Platform.isIOS && !Platform.isAndroid) return;
  } catch (_) {
    return;
  }
  try {
    await Firebase.initializeApp();
    await FirebaseMessaging.instance.requestPermission(
      alert: true,
      badge: true,
      sound: true,
    );
  } catch (e) {
    debugPrint('[onboarding] notifications permission request skipped: $e');
  }
}
