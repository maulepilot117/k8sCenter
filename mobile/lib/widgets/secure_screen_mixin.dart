// Screen-capture suppression for screens displaying revealed plaintext
// (Secret detail, future API-token reveal surfaces).
//
// Android: flips WindowManager.FLAG_SECURE on the platform window — the
// OS hides the screen contents from the recent-apps preview snapshot and
// blocks screenshots/screen recording.
//
// iOS: there is no equivalent system flag. The defense is layered.
// (1) A Flutter-rendered blur OverlayEntry is eagerly inserted at
// setSensitive(true) time and made visible by a ValueNotifier flip on
// lifecycle inactive/paused/hidden. (2) A native UIView blocker added in
// SceneDelegate.sceneWillResignActive (#302) — fires synchronously
// before iOS captures the app-switcher snapshot, so the recent-apps
// thumbnail shows the opaque blocker regardless of whether Flutter
// completes the next pipeline flush in time. The native layer is
// defense-in-depth; the Flutter overlay is the primary defense for
// debug builds and any future iOS multitasking modes that change
// snapshot timing.
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
import 'package:flutter/services.dart';
import 'package:flutter_windowmanager_plus/flutter_windowmanager_plus.dart';
import 'package:sentry_flutter/sentry_flutter.dart';

/// Channel into `mobile/ios/Runner/SceneDelegate.swift` for the native
/// iOS UIView blocker added in #302. The native side installs the
/// blocker synchronously inside `sceneWillResignActive` — the only
/// hook iOS guarantees fires before the app-switcher snapshot — which
/// closes the residual sub-frame race the eager-OverlayEntry Flutter
/// pattern from PR #300 narrowed but could not fully eliminate.
///
/// If the channel is not registered (Android, Flutter test runs, older
/// iOS builds that predate this PR) the `MissingPluginException` is
/// swallowed and the mixin degrades to Flutter-overlay-only defense.
@visibleForTesting
const MethodChannel kIOSSecureScreenChannel =
    MethodChannel('kubecenter/secure_screen');

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

  /// Test seam for the Android secure-flag platform calls. Production wires
  /// these to the real `flutter_windowmanager_plus` methods, which self-guard
  /// on `dart:io` `Platform.isAndroid` and therefore no-op (returning without
  /// touching the MethodChannel) under `flutter test` on a host OS. The
  /// mixin's own `defaultTargetPlatform` gate already scopes these calls to
  /// Android, so the plugin's redundant host guard is irrelevant in
  /// production but un-mockable in tests — hence this indirection, which lets
  /// tests observe the calls without faking `Platform.isAndroid`.
  @visibleForTesting
  static Future<bool> Function(int flags) addSecureFlags =
      FlutterWindowManagerPlus.addFlags;
  @visibleForTesting
  static Future<bool> Function(int flags) clearSecureFlags =
      FlutterWindowManagerPlus.clearFlags;

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
  /// getter reflects the painted state, not entry presence. Tests that
  /// need to assert entry-but-not-painted use
  /// `find.byKey(blurOverlayKey)` against the widget tree.
  @visibleForTesting
  bool get isBlurOverlayShown => _blurOverlay != null && _blurVisible.value;

  /// Stable key on the overlay's outermost widget so tests can locate
  /// the entry without depending on widget types that MaterialApp /
  /// Scaffold / framework internals also use.
  @visibleForTesting
  static const Key blurOverlayKey = ValueKey('SecureScreenMixin.blurOverlay');

  /// Test-only handle to the lifecycle observer so the disposed-notifier
  /// safety net can be exercised directly. The framework's normal
  /// `removeObserver` in dispose prevents `_fireLifecycle` from reaching
  /// this State after teardown — tests that want to verify post-dispose
  /// safety must capture this reference BEFORE the widget is pumped out
  /// and then invoke `didChangeAppLifecycleState` on it.
  @visibleForTesting
  WidgetsBindingObserver? get debugLifecycleObserver => _observer;

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
      // serialized future to the runtime. Errors are logged but
      // swallowed — a thrown PlatformException from `clearFlags` would
      // otherwise propagate as an unhandled async error and crash the
      // app on a screen that is already torn down.
      _inflightPlatformCall =
          (_inflightPlatformCall ?? Future.value()).then((_) async {
        try {
          await _clearFlagSecure();
        } catch (error) {
          debugPrint('SecureScreenMixin clearFlags during dispose: $error');
        }
        // Symmetric iOS native disarm — the SceneDelegate.sensitive flag
        // lives on the scene singleton and survives widget teardown. Without
        // this call, navigating away from a sensitive screen while sensitive
        // is true leaves the native blocker armed for every subsequent
        // sceneWillResignActive — every backgrounding from any screen shows
        // an opaque cover until the process is restarted. #302 P1 follow-up.
        try {
          await _notifyIOSNativeSensitive(false);
        } catch (error) {
          debugPrint('SecureScreenMixin iOS disarm during dispose: $error');
        }
      });
      unawaited(_inflightPlatformCall!);
    }
    super.dispose();
  }

  /// Toggles secure-screen behaviour. Idempotent.
  ///
  /// - `true` on Android: adds `FLAG_SECURE` to the window.
  /// - `true` on iOS: arms the eager blur overlay AND notifies the
  ///   native `SceneDelegate` via [kIOSSecureScreenChannel] so its
  ///   UIView blocker arms inside `sceneWillResignActive` before iOS
  ///   captures the app-switcher snapshot (#302).
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
    // calls don't race the platform side. The chain serializes BOTH the
    // Android FLAG_SECURE call and the iOS native blocker notification —
    // dispatching both inside the same continuation guarantees the
    // native side knows we're sensitive before the next iOS scene-resign
    // can fire (#302).
    //
    // The iOS native notification is dispatched concurrently with the
    // Android FLAG_SECURE call (not sequentially) so the iOS-side flag
    // is set as early as possible. The Android call short-circuits on
    // non-Android platforms — on an iOS build it's a synchronous no-op —
    // but firing them in parallel still cuts the time-to-native-set on
    // any platform where both channels exist. Both must complete before
    // the chain resolves so the next setSensitive call's idempotency
    // guard sees a consistent state. Race-window narrowing only; the
    // platform-channel round trip is still async so a sub-15ms reveal-
    // then-background gesture can still beat the native flag. The
    // Flutter eager-overlay is the primary defense for that window.
    final next = (_inflightPlatformCall ?? Future.value()).then((_) async {
      final iosCall = _notifyIOSNativeSensitive(sensitive);
      if (sensitive) {
        await _addFlagSecure();
      } else {
        await _clearFlagSecure();
      }
      await iosCall;
    });
    _inflightPlatformCall = next;
    try {
      await next;
    } finally {
      if (identical(_inflightPlatformCall, next)) {
        _inflightPlatformCall = null;
      }
    }
    // Read the field, not the local arg: two rapid awaited
    // setSensitive(true) → setSensitive(false) → setSensitive(true)
    // calls overlap on the platform-channel chain. The middle call's
    // post-await tail would otherwise tear down the overlay the third
    // call relies on, leaving `_sensitive=true` with no entry — a
    // subsequent lifecycle event would flip `_blurVisible` against an
    // unmounted ValueListenableBuilder, no scrim paints, and iOS
    // captures plaintext. Regresses the very race this PR closes.
    if (!_sensitive) {
      _removeBlurOverlay();
    }
  }

  bool get _isNoOpEnvironment {
    if (kIgnoreDebugForTests) return false;
    return kDebugMode;
  }

  Future<void> _addFlagSecure() async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.android) return;
    await addSecureFlags(FlutterWindowManagerPlus.FLAG_SECURE);
  }

  Future<void> _clearFlagSecure() async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.android) return;
    await clearSecureFlags(FlutterWindowManagerPlus.FLAG_SECURE);
  }

  /// Notifies the iOS [SceneDelegate] of the sensitive state via the
  /// [kIOSSecureScreenChannel] so the native UIView blocker can arm
  /// synchronously in `sceneWillResignActive` before iOS captures the
  /// app-switcher snapshot. Fails open: a `MissingPluginException`
  /// (Android build, test environment, or pre-#302 iOS binary) is
  /// swallowed because the Flutter overlay still provides primary
  /// defense. `PlatformException` is logged but not rethrown — a
  /// native-side failure must not regress the Dart-side sensitive flag.
  Future<void> _notifyIOSNativeSensitive(bool sensitive) async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.iOS) return;
    try {
      await kIOSSecureScreenChannel.invokeMethod<void>(
        'setSensitive',
        sensitive,
      );
    } on MissingPluginException {
      // Older binaries or test environments without the channel
      // registered. Degrade to Flutter-overlay-only defense.
      //
      // On a healthy post-#302 iOS binary this should never fire — if it
      // does, the native defense-in-depth layer is silently off for the
      // whole session. Surface as a Sentry breadcrumb so opted-in users
      // produce a signal we can investigate. Fire-and-forget — if Sentry
      // isn't initialized (no DSN, opted out) the SDK no-ops and the
      // try-catch absorbs anything else.
      try {
        Sentry.addBreadcrumb(
          Breadcrumb(
            level: SentryLevel.warning,
            category: 'secure-screen',
            message: 'iOS native channel missing — degraded to Flutter-overlay '
                'defense only. Expected on Android, web, and pre-#302 iOS '
                'binaries; unexpected on a healthy post-#302 iOS build.',
          ),
        );
      } catch (_) {
        // Sentry not initialized; intentionally silent.
      }
    } on PlatformException catch (error) {
      debugPrint('SecureScreenMixin iOS channel: $error');
      // Production builds strip debugPrint output. Mirror to Sentry so
      // BAD_ARG / SCENE_GONE / native-side exceptions are observable.
      try {
        Sentry.addBreadcrumb(
          Breadcrumb(
            level: SentryLevel.error,
            category: 'secure-screen',
            message:
                'iOS native channel PlatformException: code=${error.code}',
          ),
        );
      } catch (_) {
        // Sentry not initialized; intentionally silent.
      }
    }
  }

  void _onLifecycleStateChanged(AppLifecycleState state) {
    if (!_sensitive) return;
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.iOS) return;
    // Verify the overlay actually mounted before flipping visibility.
    // `_insertBlurOverlay` no-ops via the `!mounted` guard if
    // setSensitive(true) ran before the first frame (initState path) —
    // in that case _sensitive is true but _blurOverlay is null. Without
    // this check the notifier flips against an unmounted
    // ValueListenableBuilder, no scrim paints, and iOS captures
    // plaintext silently. Re-attempt the insertion as a recovery path
    // so a late-mounted screen still gets the scrim on the next pump.
    if (_blurOverlay == null) {
      _insertBlurOverlay();
      if (_blurOverlay == null) return;
    }
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
    // child swaps between an empty placeholder and the scrim. The
    // top-level ValueKey is the anchor tests use to locate this overlay
    // — `find.byType(IgnorePointer)` is ambiguous because MaterialApp
    // and framework internals inject their own.
    return Positioned.fill(
      key: blurOverlayKey,
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

