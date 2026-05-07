// Notification feed parsing coverage. Verifies NotificationItem.fromJson
// extracts the fields the feed UI relies on (severity tinting,
// hasResourceTarget gate for tap-to-deep-link), and tolerates the
// optional resource fields being absent for cluster-scoped events.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/notifications_center/feed_repository.dart';

void main() {
  test('parses full notification with resource target', () {
    final n = NotificationItem.fromJson({
      'id': 'n-1',
      'source': 'alertmanager',
      'severity': 'critical',
      'title': 'High pod restart rate',
      'message': 'web-7d4f-abc has restarted 5 times',
      'createdAt': '2026-05-07T12:00:00Z',
      'read': false,
      'resourceKind': 'Pod',
      'resourceNamespace': 'default',
      'resourceName': 'web-7d4f-abc',
      'clusterId': 'local',
    });
    expect(n.id, 'n-1');
    expect(n.source, 'alertmanager');
    expect(n.severity, 'critical');
    expect(n.read, isFalse);
    expect(n.hasResourceTarget, isTrue);
    expect(n.resourceKind, 'Pod');
    expect(n.clusterId, 'local');
  });

  test('cluster-scoped notification has empty namespace + still targets',
      () {
    final n = NotificationItem.fromJson({
      'id': 'n-2',
      'source': 'controller',
      'severity': 'warning',
      'title': 'Node not ready',
      'message': '',
      'createdAt': '2026-05-07T12:00:00Z',
      'resourceKind': 'Node',
      'resourceNamespace': '',
      'resourceName': 'worker-01',
    });
    expect(n.hasResourceTarget, isTrue);
    expect(n.resourceNamespace, isEmpty);
    expect(n.resourceName, 'worker-01');
  });

  test('notification without resource fields is non-targetable', () {
    final n = NotificationItem.fromJson({
      'id': 'n-3',
      'source': 'system',
      'severity': 'info',
      'title': 'Backup complete',
      'message': '',
      'createdAt': '2026-05-07T12:00:00Z',
    });
    expect(n.hasResourceTarget, isFalse);
    expect(n.resourceKind, isNull);
  });

  test('missing createdAt falls through to epoch (does not crash)', () {
    final n = NotificationItem.fromJson({
      'id': 'n-4',
      'source': 'x',
      'severity': 'info',
      'title': 't',
      'message': 'm',
    });
    // Sentinel epoch — feed UI shows it as very old, not a render crash.
    expect(n.createdAt.millisecondsSinceEpoch, 0);
  });
}
