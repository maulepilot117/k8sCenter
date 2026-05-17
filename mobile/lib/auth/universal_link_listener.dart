// Universal Link / App Link receiver for the OIDC callback path.
//
// flutter_custom_tabs launches the IdP authorization URL in a Custom-Tab
// / SFSafariViewController; the IdP redirects via Universal Link to
// `https://<host>/m/auth/callback?code=...&state=...`; the OS routes
// the URL to the app via app_links; this listener filters for the
// callback path and dispatches to OIDCController.completeFlow.
//
// Non-OIDC Universal Links (notification deep-links) flow through the
// FCM listener stack in lib/notifications/. This listener is OIDC-only
// to keep the auth seam decoupled from notification routing.

import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../notifications/deep_link_handler.dart' show kUniversalLinkHost;
import 'oidc_controller.dart';

/// Wires app_links to OIDCController.completeFlow. Holds the
/// subscription so callers can dispose it on logout / test teardown.
class UniversalLinkListener {
  UniversalLinkListener(
    this._ref, {
    AppLinks? appLinks,
    String? universalLinkHost,
  })  : _appLinks = appLinks ?? AppLinks(),
        _universalLinkHostOverride = universalLinkHost;

  final Ref _ref;
  final AppLinks _appLinks;
  // Override seam for tests — the const `kUniversalLinkHost` is empty in
  // the test binary (no --dart-define), so tests need a way to supply a
  // host without rebuilding the whole notification module. Production
  // wiring goes through the Provider below and passes null, which keeps
  // the dart-define source of truth.
  final String? _universalLinkHostOverride;

  String get _host => _universalLinkHostOverride ?? kUniversalLinkHost;

  StreamSubscription<Uri>? _sub;
  bool _initialDrained = false;

  /// Starts listening. Idempotent. On call:
  ///   1. Subscribe to the post-init link stream so future redirects
  ///      route into [OIDCController.completeFlow].
  ///   2. Drain the initial-link (cold-start case: the IdP redirect
  ///      arrived while the app was terminated) — same dispatch path.
  Future<void> start() async {
    if (_sub != null) return;
    if (_host.isEmpty) {
      // No universal link host configured — OIDC mobile flow is
      // disabled for this build. No-op to avoid platform-channel
      // exceptions on a build that has no link mapping.
      return;
    }

    _sub = _appLinks.uriLinkStream.listen(
      _maybeDispatch,
      onError: (Object e, StackTrace st) {
        debugPrint('UniversalLinkListener stream error: $e');
      },
    );

    if (!_initialDrained) {
      _initialDrained = true;
      try {
        // Bound the platform-channel call — a hanging getInitialLink
        // would block the post-bootstrap chain in main.dart from ever
        // completing. 5s is generous; in practice this is a millisecond
        // call against the platform side.
        final initial = await _appLinks
            .getInitialLink()
            .timeout(const Duration(seconds: 5), onTimeout: () => null);
        if (initial != null) _maybeDispatch(initial);
      } catch (e) {
        debugPrint('UniversalLinkListener initial-link drain failed: $e');
      }
    }
  }

  Future<void> stop() async {
    await _sub?.cancel();
    _sub = null;
  }

  /// Filters the URI for the OIDC callback path and routes it. Visible
  /// for tests so we don't have to mock AppLinks.uriLinkStream end-to-end.
  @visibleForTesting
  Future<void> dispatch(Uri uri) => _maybeDispatch(uri);

  Future<void> _maybeDispatch(Uri uri) async {
    // Only consume the OIDC callback path. Notification deep-links and
    // any other /m/* paths flow through their own handlers.
    if (uri.scheme != 'https' && uri.scheme != 'http') return;
    if (uri.host != _host) return;
    if (uri.path != oidcCallbackPath) return;

    await _ref.read(oidcControllerProvider.notifier).completeFlow(uri);
  }
}

final universalLinkListenerProvider = Provider<UniversalLinkListener>((ref) {
  final listener = UniversalLinkListener(ref);
  ref.onDispose(() async => listener.stop());
  return listener;
});
