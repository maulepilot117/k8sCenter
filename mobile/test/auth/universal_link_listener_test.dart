// Tests for [UniversalLinkListener]. Covers the dispatch filter (only
// the configured https host + the OIDC callback path route through to
// the controller) and the empty-universal-link-host short-circuit.
//
// The platform-channel surface (uriLinkStream, getInitialLink) is
// avoided by exercising the @visibleForTesting `dispatch` seam
// directly — same code path the live listener funnels everything
// through. Idempotency of [start] is verified at the "second call is a
// no-op" level via the empty-host branch, since exercising the actual
// stream subscription requires a fake platform plugin that exceeds the
// value of a unit test here.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/oidc_controller.dart';
import 'package:kubecenter/auth/pending_oidc_store.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/auth/universal_link_listener.dart';

/// Captures completeFlow invocations so the dispatch filter can be
/// asserted without running the full OIDC exchange.
class _RecordingOIDCController extends OIDCController {
  _RecordingOIDCController({String host = 'k8scenter.test'})
      : super(universalLinkHost: host);

  final List<Uri> completedFlows = [];

  @override
  Future<void> completeFlow(Uri callback) async {
    completedFlows.add(callback);
  }
}

void main() {
  group('UniversalLinkListener.dispatch (filter)', () {
    late _RecordingOIDCController recorder;
    late ProviderContainer container;
    late UniversalLinkListener listener;

    setUp(() {
      recorder = _RecordingOIDCController();
      container = ProviderContainer(overrides: [
        pendingOidcStoreProvider.overrideWithValue(InMemoryPendingOidcStore()),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        oidcControllerProvider.overrideWith(() => recorder),
        // Override the listener provider to supply a fixed host —
        // kUniversalLinkHost is empty in the test binary.
        universalLinkListenerProvider.overrideWith((ref) {
          return UniversalLinkListener(
            ref,
            universalLinkHost: 'k8scenter.test',
          );
        }),
      ]);
      // Construct the listener directly with the live AppLinks singleton
      // — start() is never called in these tests so no platform-channel
      // calls fire. dispatch() runs entirely in-process.
      listener = container.read(universalLinkListenerProvider);
    });

    tearDown(() => container.dispose());

    test('OIDC callback URL routes to completeFlow', () async {
      await listener.dispatch(
          Uri.parse('https://k8scenter.test/m/auth/callback?code=X&state=Y'));
      expect(recorder.completedFlows, hasLength(1));
      expect(recorder.completedFlows.single.path, '/m/auth/callback');
    });

    test('custom-scheme link ignored (only https/http handled here)',
        () async {
      await listener
          .dispatch(Uri.parse('k8scenter://cluster/local/Pod/ns/foo'));
      expect(recorder.completedFlows, isEmpty);
    });

    test('different host ignored', () async {
      await listener.dispatch(
          Uri.parse('https://other.example.com/m/auth/callback?code=X'));
      expect(recorder.completedFlows, isEmpty);
    });

    test('different path on same host ignored', () async {
      await listener.dispatch(
          Uri.parse('https://k8scenter.test/m/notifications'));
      expect(recorder.completedFlows, isEmpty);
    });

    test('callback query string preserved across dispatch', () async {
      await listener.dispatch(Uri.parse(
          'https://k8scenter.test/m/auth/callback?code=AUTH&state=ST&error_description=foo'));
      final received = recorder.completedFlows.single;
      expect(received.queryParameters['code'], 'AUTH');
      expect(received.queryParameters['state'], 'ST');
      expect(received.queryParameters['error_description'], 'foo');
    });
  });

  group('UniversalLinkListener.start (host empty)', () {
    test(
        'empty kUniversalLinkHost: start() returns without subscribing',
        () async {
      // The provider reads kUniversalLinkHost from the
      // notifications/deep_link_handler module. In the test binary that
      // const evaluates to empty (no --dart-define), so start() should
      // short-circuit and never touch the platform channel.
      final container = ProviderContainer(overrides: [
        pendingOidcStoreProvider.overrideWithValue(InMemoryPendingOidcStore()),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      ]);
      addTearDown(container.dispose);
      final listener = container.read(universalLinkListenerProvider);

      // Should complete without throwing. If start() incorrectly hit
      // AppLinks().uriLinkStream, the default method-channel platform
      // would throw a MissingPluginException in unit-test mode.
      await listener.start();
      // Second call is a no-op (idempotent).
      await listener.start();
    });
  });
}
