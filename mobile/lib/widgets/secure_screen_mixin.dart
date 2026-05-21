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
// Eager-overlay race mitigation (#271): the OverlayEntry is inserted at
// `setSensitive(true)` time, not on the lifecycle event. While the screen
// is sensitive the entry sits in the Overlay tree but renders an empty
// `SizedBox.expand()` (passing pointer events through via `IgnorePointer`
// and excluded from the semantics tree). On `inactive`/`paused`/`hidden`
// a `ValueNotifier<bool>` flips and the `ValueListenableBuilder` swaps in
// the `BackdropFilter` + scrim. Closes the window in which iOS could
// snapshot the app-switcher frame before Flutter materialized a brand-new
// `OverlayEntry` on the lifecycle event — only a single rebuild remains
// between lifecycle delivery and first paint.
//
// Debug-build override: in `kDebugMode`, both paths are no-ops so QA can
// screen-record bug reproductions without rebuilding. Tests bypass this
// via [SecureScreenMixin.kIgnoreDebugForTests].

import 'dart:async';
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
  /// Defaults to `kDebugMode` so widget tests get the production code
  /// path automatically — tests that explicitly want to verify the
  /// debug-mode gate set this back to `false` for that one case. (The
  /// default-`false`-and-flip-in-setUp pattern used to work but was
  /// load-bearing for every secure-screen test, which made it easy to
  /// forget when adding new ones.)
  @visibleForTesting
  static bool kIgnoreDebugForTests = kDebugMode;

  bool _sensitive = false;
  OverlayEntry? _blurOverlay;
  // Drives the inner `ValueListenableBuilder` that swaps the overlay
  // child between an empty placeholder and the `BackdropFilter`. Toggled
  // by the lifecycle observer; never read before the overlay is inserted.
  final ValueNotifier<bool> _blurVisible = ValueNotifier<bool>(false);
  _LifecycleObserver? _observer;

  /// Serializes platform-channel calls so dispose can chain a clear
  /// onto any in-flight add (or vice versa). Without this, dispose's
  /// fire-and-forget clearFlags could race a still-running addFlags
  /// and leave the FLAG_SECURE set on the window after the screen is
  /// gone, blocking screenshots app-wide until the next add/clear.
  Future<void>? _inflightPlatformCall;

  /// Current sensitive state. Visible for tests.
  @visibleForTesting
  bool get isSensitive => _sensitive;

  /// Whether the blur scrim is currently painted (visible to the user).
  /// After the eager-overlay mitigation, the [OverlayEntry] may be
  /// inserted while the screen is sensitive but the scrim hidden — this
  /// getter reflects the painted state, not entry presence.
  @visibleForTesting
  bool get isBlurOverlayShown => _blurOverlay != null && _blurVisible.value;

  /// Whether the blur [OverlayEntry] is currently inserted in the Overlay
  /// tree, independent of whether the scrim is painting. Always tracks
  /// `_sensitive` on iOS; always `false` on Android (which uses
  /// `FLAG_SECURE` instead) and other platforms. Visible for tests.
  @visibleForTesting
  bool get isBlurOverlayInserted => _blurOverlay != null;

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
    // Dispose AFTER `_removeBlurOverlay` so the overlay child's last
    // rebuild reads a still-live notifier. Any post-dispose `setSensitive`
    // tail (already-resolved await chain) short-circuits in
    // `_removeBlurOverlay` via the `_blurOverlay == null` check before it
    // can touch the notifier.
    _blurVisible.dispose();
    if (_sensitive) {
      // Fire-and-forget but chained onto any in-flight platform call so
      // a clearFlags() can't overlap an addFlags() that's still in
      // transit. dispose cannot await its own work, so we hand the
      // serialized future to the runtime.
      _inflightPlatformCall =
          (_inflightPlatformCall ?? Future.value()).then((_) {
        return _clearFlagSecure();
      });
      unawaited(_inflightPlatformCall!);
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
    // Eagerly insert the iOS blur overlay BEFORE awaiting the platform
    // channel so the `OverlayEntry` is in the widget tree well before any
    // lifecycle event can fire. When iOS hands control to the snapshotter
    // on `applicationWillResignActive` the BackdropFilter element is
    // already mounted; a `ValueNotifier` flip is the only remaining work
    // between lifecycle delivery and first paint of the scrim. #271.
    if (sensitive) {
      _insertBlurOverlay();
    }
    // Chain onto any in-flight platform call so two rapid setSensitive
    // calls don't race the platform side.
    final next = (_inflightPlatformCall ?? Future.value()).then((_) {
      if (sensitive) {
        return _addFlagSecure();
      } else {
        return _clearFlagSecure();
      }
    });
    _inflightPlatformCall = next;
    try {
      await next;
    } finally {
      if (identical(_inflightPlatformCall, next)) {
        _inflightPlatformCall = null;
      }
    }
    if (!sensitive) {
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
    // `hidden` was added in Flutter 3.13 as the state delivered when the
    // app is moved to the background on iOS via a system-driven path
    // (e.g., Stage Manager) without first transitioning through
    // `inactive`. We treat it the same as inactive/paused so the blur
    // cover lands BEFORE the OS captures the app-switcher snapshot.
    if (state == AppLifecycleState.inactive ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden) {
      _blurVisible.value = true;
    } else if (state == AppLifecycleState.resumed) {
      _blurVisible.value = false;
    }
  }

  void _insertBlurOverlay() {
    if (_blurOverlay != null) return;
    if (!mounted) return;
    // Only iOS uses the Flutter overlay path. Android relies on
    // `FLAG_SECURE` and has no overlay; skipping the insertion here
    // avoids a no-op widget in the Android Overlay tree.
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.iOS) return;
    _blurOverlay = OverlayEntry(builder: _blurOverlayBuilder);
    Overlay.of(context, rootOverlay: true).insert(_blurOverlay!);
  }

  Widget _blurOverlayBuilder(BuildContext ctx) {
    // Outer wrappers are constant: the overlay always fills the screen,
    // never receives pointer events, and never contributes to the
    // semantics tree of the sensitive screen under it. Only the inner
    // child swaps between an empty placeholder and the scrim.
    return Positioned.fill(
      child: IgnorePointer(
        ignoring: true,
        child: ExcludeSemantics(
          child: ValueListenableBuilder<bool>(
            valueListenable: _blurVisible,
            builder: (innerCtx, visible, _) {
              if (!visible) {
                // Inert placeholder. `SizedBox.expand()` matches the
                // outer `Positioned.fill` constraints so the layout
                // doesn't re-measure on visibility flip.
                return const SizedBox.expand();
              }
              // 30px sigma chosen empirically — 24px lets character
              // outlines bleed through on small-font Secret values in
              // iOS Simulator captures. The translucent surface-colored
              // scrim renders correctly across all themes; no per-theme
              // branching needed.
              final scrimColor =
                  Theme.of(innerCtx).colorScheme.surface.withValues(alpha: 0.7);
              return BackdropFilter(
                filter: ImageFilter.blur(sigmaX: 30, sigmaY: 30),
                child: ColoredBox(color: scrimColor),
              );
            },
          ),
        ),
      ),
    );
  }

  void _removeBlurOverlay() {
    final overlay = _blurOverlay;
    if (overlay == null) return;
    _blurOverlay = null;
    // Reset visibility to the hidden default so the next sensitive
    // session starts clean. Safe before `_blurVisible.dispose()` because
    // dispose-order in `dispose()` puts overlay removal first.
    _blurVisible.value = false;
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

