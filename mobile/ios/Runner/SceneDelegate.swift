import Flutter
import UIKit

/// Scene delegate hosting the secure-screen blocker for #302.
///
/// Closes the residual iOS app-switcher snapshot race left after PR #300
/// (issue #271 / Mitigation 1). When a sensitive Dart screen is mounted,
/// `SecureScreenMixin.setSensitive(true)` sends a `setSensitive: true`
/// call over the `kubecenter/secure_screen` `FlutterMethodChannel`,
/// flipping `sensitive` on this delegate. iOS calls
/// `sceneWillResignActive` synchronously before snapshotting the
/// app-switcher thumbnail — the blocker is added as a top-level subview
/// of the scene's window inside that callback. UIKit lays out and
/// renders the blocker before iOS captures the snapshot, so the recent-
/// apps thumbnail shows the opaque cover, not the revealed Secret.
///
/// The Flutter-side blur overlay (eager-OverlayEntry pattern from
/// PR #300) is still the primary defense. This native layer is
/// defense-in-depth — it catches the sub-frame race that pure Flutter
/// rendering cannot guarantee to close before snapshot. Both layers
/// arm together via `SecureScreenMixin.setSensitive`.
///
/// Scene-based vs application-based lifecycle: this app declares
/// `UIApplicationSceneManifest` in `Info.plist` with
/// `UIApplicationSupportsMultipleScenes = false`, so the legacy
/// `AppDelegate.applicationWillResignActive` is NOT called by iOS.
/// `sceneWillResignActive` on the active scene delegate is the only
/// pre-snapshot hook for scene apps; implementing this on AppDelegate
/// would not fire on iOS 13+.
class SceneDelegate: FlutterSceneDelegate {

  /// Channel name shared with `mobile/lib/widgets/secure_screen_mixin.dart`.
  /// If you rename here, rename there too (and update the channel-call
  /// test in `mobile/test/widgets/secure_screen_mixin_test.dart`).
  private static let channelName = "kubecenter/secure_screen"

  /// Dart-controlled flag. `true` means a sensitive screen is currently
  /// mounted and the blocker must arm on the next scene-resign-active.
  /// Mirrors `_sensitive` on the Dart `SecureScreenMixin`.
  private var sensitive = false

  /// Single instance of the opaque blocker view. Lazy-initialized on
  /// first arm so we don't pay the allocation cost during cold start.
  private lazy var secureBlocker: UIView = makeSecureBlocker()

  // MARK: - Method Channel

  override func scene(
    _ scene: UIScene,
    willConnectTo session: UISceneSession,
    options connectionOptions: UIScene.ConnectionOptions
  ) {
    super.scene(scene, willConnectTo: session, options: connectionOptions)
    guard
      let controller = self.window?.rootViewController as? FlutterViewController
    else {
      // Flutter engine attach failed; non-fatal — the Dart side's
      // Flutter overlay still defends the screen, just without the
      // native belt-and-suspenders layer.
      return
    }
    let channel = FlutterMethodChannel(
      name: SceneDelegate.channelName,
      binaryMessenger: controller.binaryMessenger
    )
    channel.setMethodCallHandler { [weak self] call, result in
      guard let self = self else {
        result(FlutterError(
          code: "SCENE_GONE",
          message: "Scene torn down before method call",
          details: nil
        ))
        return
      }
      switch call.method {
      case "setSensitive":
        if let value = call.arguments as? Bool {
          self.sensitive = value
          // If the user revealed a Secret WHILE the app is currently
          // resigning active (rare but reachable on iPad Stage Manager
          // focus loss interleaved with reveal), arm the blocker
          // immediately so the next snapshot sees it.
          if value, let window = self.window, scene.activationState != .foregroundActive {
            self.armBlocker(in: window)
          } else if !value {
            self.disarmBlocker()
          }
          result(nil)
        } else {
          result(FlutterError(
            code: "BAD_ARG",
            message: "setSensitive requires a Bool argument",
            details: String(describing: call.arguments)
          ))
        }
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  // MARK: - Snapshot defense

  override func sceneWillResignActive(_ scene: UIScene) {
    super.sceneWillResignActive(scene)
    guard sensitive, let window = self.window else { return }
    armBlocker(in: window)
  }

  override func sceneDidBecomeActive(_ scene: UIScene) {
    super.sceneDidBecomeActive(scene)
    disarmBlocker()
  }

  // MARK: - Blocker management

  private func armBlocker(in window: UIWindow) {
    secureBlocker.frame = window.bounds
    window.addSubview(secureBlocker)
    window.bringSubviewToFront(secureBlocker)
  }

  private func disarmBlocker() {
    secureBlocker.removeFromSuperview()
  }

  private func makeSecureBlocker() -> UIView {
    // Opaque solid view — chosen over `UIVisualEffectView` because the
    // blur effect's render path can defer to the next frame, which is
    // the exact race we're trying to close. A solid `backgroundColor`
    // is applied synchronously during the next layout pass triggered
    // by `addSubview`, which iOS completes before snapshotting.
    //
    // Uses `systemBackground` so the cover adapts to dark/light mode and
    // visually matches the theme without per-theme branching.
    let view = UIView(frame: .zero)
    view.backgroundColor = .systemBackground
    view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
    view.isUserInteractionEnabled = false
    view.accessibilityElementsHidden = true
    view.isAccessibilityElement = false
    return view
  }
}
