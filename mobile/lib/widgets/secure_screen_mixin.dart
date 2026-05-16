// Screen-capture suppression for screens displaying revealed plaintext
// (Secret detail, future API-token reveal surfaces).
//
// Android: flips WindowManager.FLAG_SECURE on the platform window — the
// OS hides the screen contents from the recent-apps preview snapshot and
// blocks screenshots/screen recording.
//
// iOS: there is no equivalent system flag. The pattern is to push a blur
// overlay when the app lifecycle enters inactive/paused (which fires
// BEFORE iOS captures the app-switcher snapshot) and remove it on resume.
//
// Debug-build override: in `kDebugMode`, both paths are no-ops so QA can
// screen-record bug reproductions without rebuilding. Tests bypass this
// via [SecureScreenMixin.kIgnoreDebugForTests].

import 'dart:ui';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_windowmanager/flutter_windowmanager.dart';

/// Mix into a `State<...>` subclass to opt into screen-capture
/// suppression. Call [setSensitive] when secure content is revealed
/// or concealed.
///
/// Idempotent: calling `setSensitive(true)` twice in a row is a no-op
/// on the second call.
///
/// Compatibility note: the constraint `on State<T>` accepts both
/// `State<MyWidget>` and `ConsumerState<MyWidget>` (the Riverpod variant
/// extends `State`). Mix into the State subclass directly:
///
/// ```dart
/// class _SecretDetailState extends ConsumerState<SecretDetailScreen>
///     with SecureScreenMixin<SecretDetailScreen> { ... }
/// ```
mixin SecureScreenMixin<T extends StatefulWidget> on State<T> {
  /// Visible-for-test override that disables the [kDebugMode] no-op.
  /// Set to `true` in widget tests that need to assert on FLAG_SECURE
  /// or blur-overlay behaviour.
  @visibleForTesting
  static bool kIgnoreDebugForTests = false;

  bool _sensitive = false;
  OverlayEntry? _blurOverlay;
  _LifecycleObserver? _observer;

  /// Current sensitive state. Visible for tests.
  @visibleForTesting
  bool get isSensitive => _sensitive;

  /// Whether a blur overlay is currently inserted. Visible for tests.
  @visibleForTesting
  bool get isBlurOverlayShown => _blurOverlay != null;

  @override
  void initState() {
    super.initState();
    _observer = _LifecycleObserver(this);
    WidgetsBinding.instance.addObserver(_observer!);
  }

  @override
  void dispose() {
    if (_observer != null) {
      WidgetsBinding.instance.removeObserver(_observer!);
      _observer = null;
    }
    _removeBlurOverlay();
    if (_sensitive) {
      // Fire-and-forget — dispose cannot await.
      unawaited(_clearFlagSecure());
    }
    super.dispose();
  }

  /// Toggles secure-screen behaviour. Idempotent.
  ///
  /// - `true` on Android: adds `FLAG_SECURE` to the window.
  /// - `true` on iOS: arms the blur overlay so the next inactive/paused
  ///   lifecycle event covers the screen.
  /// - `false`: clears both and removes any active overlay.
  Future<void> setSensitive(bool sensitive) async {
    if (_isNoOpEnvironment) return;
    if (_sensitive == sensitive) return;
    _sensitive = sensitive;
    if (sensitive) {
      await _addFlagSecure();
    } else {
      await _clearFlagSecure();
      _removeBlurOverlay();
    }
  }

  bool get _isNoOpEnvironment {
    if (kIgnoreDebugForTests) return false;
    return kDebugMode;
  }

  Future<void> _addFlagSecure() async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.android) return;
    await FlutterWindowManager.addFlags(FlutterWindowManager.FLAG_SECURE);
  }

  Future<void> _clearFlagSecure() async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.android) return;
    await FlutterWindowManager.clearFlags(FlutterWindowManager.FLAG_SECURE);
  }

  void _onLifecycleStateChanged(AppLifecycleState state) {
    if (!_sensitive) return;
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.iOS) return;
    if (state == AppLifecycleState.inactive ||
        state == AppLifecycleState.paused) {
      _insertBlurOverlay();
    } else if (state == AppLifecycleState.resumed) {
      _removeBlurOverlay();
    }
  }

  void _insertBlurOverlay() {
    if (_blurOverlay != null) return;
    if (!mounted) return;
    _blurOverlay = OverlayEntry(builder: _blurOverlayBuilder);
    Overlay.of(context, rootOverlay: true).insert(_blurOverlay!);
  }

  Widget _blurOverlayBuilder(BuildContext ctx) {
    // 30px sigma chosen empirically — 24px lets character outlines bleed
    // through on small-font Secret values in iOS Simulator captures.
    // The translucent surface-colored scrim renders correctly across all
    // 7 themes; no per-theme branching needed.
    final scrimColor =
        Theme.of(ctx).colorScheme.surface.withValues(alpha: 0.7);
    return Positioned.fill(
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: 30, sigmaY: 30),
        child: ColoredBox(color: scrimColor),
      ),
    );
  }

  void _removeBlurOverlay() {
    final overlay = _blurOverlay;
    if (overlay == null) return;
    _blurOverlay = null;
    overlay.remove();
  }
}

/// Inner `WidgetsBindingObserver` that delegates to the mixin. Lets the
/// mixin stay narrow (`on State<T>` only) without forcing consumers to
/// also `with WidgetsBindingObserver` separately.
class _LifecycleObserver with WidgetsBindingObserver {
  _LifecycleObserver(this._owner);
  final SecureScreenMixin _owner;

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _owner._onLifecycleStateChanged(state);
  }
}

/// Local fire-and-forget helper; mirrors the same shape used elsewhere
/// in `mobile/lib/main.dart` so we don't pull in `package:async`.
void unawaited(Future<void> future) {}
