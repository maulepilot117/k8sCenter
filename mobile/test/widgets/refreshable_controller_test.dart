// Tests for the RefreshableController mixin's race-protection
// state — captureDispatchId / isFresh / bumpDispatch / supersede
// / pinnedMismatchMessage / isCancelException — plus an end-to-end
// dispose-path test backed by a real ProviderContainer.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/widgets/refreshable_controller.dart';

class _Harness with RefreshableController {
  _Harness({required this.pinnedClusterId, required this.activeClusterId});

  @override
  final String pinnedClusterId;

  String activeClusterId;

  @override
  String currentActiveClusterId(Ref ref) => activeClusterId;
}

void main() {
  group('RefreshableController', () {
    test('captureDispatchId returns increasing ids; isFresh tracks them',
        () {
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      final id1 = h.captureDispatchId();
      final id2 = h.captureDispatchId();
      expect(id1 < id2, isTrue);
      expect(h.isFresh(id1), isFalse);
      expect(h.isFresh(id2), isTrue);
    });

    test('bumpDispatch invalidates a previously captured id', () {
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      final captured = h.captureDispatchId();
      expect(h.isFresh(captured), isTrue);
      h.bumpDispatch();
      expect(h.isFresh(captured), isFalse);
    });

    test('supersede cancels the current token and rotates a new one',
        () {
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      final tokenA = h.currentCancelToken;
      expect(tokenA.isCancelled, isFalse);
      h.supersede();
      expect(tokenA.isCancelled, isTrue);
      final tokenB = h.currentCancelToken;
      expect(tokenB, isNot(same(tokenA)));
      expect(tokenB.isCancelled, isFalse);
    });

    test('supersede bumps dispatch id so late results are dropped', () {
      // A response that lands after the socket-buffer race is captured
      // before supersede fires. After the cancel, isFresh on the
      // captured id must return false even though the controller is
      // not disposed.
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      final captured = h.captureDispatchId();
      expect(h.isFresh(captured), isTrue);
      h.supersede();
      expect(h.isFresh(captured), isFalse);
    });

    test('clusterStillPinned: true when active matches pinned, false otherwise',
        () {
      final h = _Harness(pinnedClusterId: 'cluster-a', activeClusterId: 'cluster-a');
      final container = ProviderContainer();
      addTearDown(container.dispose);
      // Use a no-op provider to materialize a Ref the harness can read
      // through. The harness ignores the Ref and reads activeClusterId
      // directly, so any Ref is fine.
      final probe = Provider<int>((ref) {
        expect(h.clusterStillPinned(ref), isTrue);
        h.activeClusterId = 'cluster-b';
        expect(h.clusterStillPinned(ref), isFalse);
        return 0;
      });
      container.read(probe);
    });

    test('initRefreshable is idempotent — second call is a no-op', () {
      // After the first init, _refreshableInitialized flips true and a
      // dispose hook is registered. A second call must not register a
      // second hook (which would double-cancel the CancelToken and
      // double-set _disposed). We can't directly count onDispose
      // registrations, so we verify the public surface stays consistent
      // and the call does not throw.
      final provider = AutoDisposeNotifierProvider<_TinyNotifier, int>(
        _TinyNotifier.new,
      );
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(provider.notifier);
      expect(notifier.isInitialized, isTrue);
      // Re-invoking initRefreshable inside the same notifier instance
      // must be safe.
      expect(() => notifier.callInitAgain(), returnsNormally);
      expect(notifier.isInitialized, isTrue);
      expect(notifier.isDisposed, isFalse);
    });

    test('pinnedMismatchMessage differs between pre/post-emission phases',
        () {
      final h = _Harness(pinnedClusterId: 'cluster-a', activeClusterId: 'cluster-b');
      final pre = h.pinnedMismatchMessage(PinPhase.preEmission);
      final post = h.pinnedMismatchMessage(PinPhase.postEmission);
      expect(pre, contains('Aborted'));
      expect(post, contains('was loaded from the'));
      expect(post, contains('cluster-a'));
      expect(pre, isNot(equals(post)));
    });

    test('isDisposed reads false until dispose hook fires (state default)',
        () {
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      expect(h.isDisposed, isFalse);
    });

    test('isCancelException true only for DioException(cancel)', () {
      final cancel = DioException(
        requestOptions: RequestOptions(path: '/'),
        type: DioExceptionType.cancel,
      );
      final connTimeout = DioException(
        requestOptions: RequestOptions(path: '/'),
        type: DioExceptionType.connectionTimeout,
      );
      expect(RefreshableController.isCancelException(cancel), isTrue);
      expect(RefreshableController.isCancelException(connTimeout), isFalse);
      expect(RefreshableController.isCancelException(StateError('x')), isFalse);
    });
  });

  group('RefreshableController dispose path (ProviderContainer-backed)', () {
    test('isDisposed flips true after the provider is invalidated; '
        'CancelToken cancels in flight', () {
      final provider = AutoDisposeNotifierProvider<_TinyNotifier, int>(
        _TinyNotifier.new,
      );
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(provider.notifier);
      expect(notifier.isInitialized, isTrue);
      expect(notifier.isDisposed, isFalse);
      // Acquire the token before disposal so we can verify cancellation.
      final token = notifier.currentCancelToken;
      expect(token.isCancelled, isFalse);

      // Disposing the container fires the autoDispose lifecycle which
      // runs every `ref.onDispose` callback registered in
      // `initRefreshable`.
      container.dispose();
      expect(notifier.isDisposed, isTrue);
      expect(token.isCancelled, isTrue);
    });
  });
}

class _TinyNotifier extends AutoDisposeNotifier<int> with RefreshableController {
  @override
  String get pinnedClusterId => 'pinned';

  @override
  String currentActiveClusterId(Ref ref) => 'pinned';

  @override
  int build() {
    initRefreshable(ref);
    return 0;
  }

  /// Test hook: re-invoke initRefreshable on the same instance to
  /// exercise the idempotent guard.
  void callInitAgain() => initRefreshable(ref);
}
