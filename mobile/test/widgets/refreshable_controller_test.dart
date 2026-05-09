// Tests for the RefreshableController mixin's race-protection
// state — captureDispatchId / isFresh / bumpDispatch / cancelInflight
// / pinnedMismatchMessage. These are the bits that don't require a
// real Riverpod Ref. End-to-end cluster-pin behavior is validated by
// the per-domain controller tests in PR-4b onward, where a real
// ProviderContainer + Notifier composition is available.

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

    test('cancelInflight cancels the current token and rotates a new one',
        () {
      final h = _Harness(pinnedClusterId: 'a', activeClusterId: 'a');
      final tokenA = h.currentCancelToken;
      expect(tokenA.isCancelled, isFalse);
      h.cancelInflight();
      expect(tokenA.isCancelled, isTrue);
      final tokenB = h.currentCancelToken;
      expect(tokenB, isNot(same(tokenA)));
      expect(tokenB.isCancelled, isFalse);
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
  });
}
