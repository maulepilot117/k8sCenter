// Defensive parser tests for DashboardSummary + Utilization. Covers
// the JSON drift cases that the live backend can produce: int-vs-double,
// missing keys, present-but-null fields, and the synthetic
// "Prometheus unavailable" payload.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/dashboard/dashboard_state.dart';

void main() {
  group('DashboardSummary.fromJson', () {
    test('parses canonical envelope', () {
      final s = DashboardSummary.fromJson({
        'nodes': {'total': 5, 'ready': 4},
        'pods': {'total': 100, 'running': 95, 'pending': 3, 'failed': 2},
        'services': {'total': 30},
        'alerts': {'active': 1, 'critical': 0},
      });
      expect(s.nodes.total, 5);
      expect(s.pods.running, 95);
      expect(s.servicesTotal, 30);
      expect(s.alerts.critical, 0);
      expect(s.cpu, isNull);
      expect(s.memory, isNull);
    });

    test('coerces double counts to int (PromQL drift)', () {
      final s = DashboardSummary.fromJson({
        'nodes': {'total': 5.0, 'ready': 4.0},
        'pods': {'total': 100.0, 'running': 95.0, 'pending': 3, 'failed': 2},
        'services': {'total': 30.0},
        'alerts': {'active': 1.0, 'critical': 0.0},
      });
      expect(s.nodes.total, 5);
      expect(s.pods.running, 95);
      expect(s.servicesTotal, 30);
    });

    test('handles missing nested objects', () {
      final s = DashboardSummary.fromJson(<String, dynamic>{});
      expect(s.nodes.total, 0);
      expect(s.pods.running, 0);
      expect(s.servicesTotal, 0);
      expect(s.alerts.active, 0);
      expect(s.cpu, isNull);
      expect(s.memory, isNull);
    });

    test('handles non-numeric counts (defensive default)', () {
      final s = DashboardSummary.fromJson({
        'nodes': {'total': 'n/a', 'ready': null},
        'pods': <String, dynamic>{},
        'services': {'total': true},
        'alerts': {'active': [1, 2]},
      });
      expect(s.nodes.total, 0);
      expect(s.nodes.ready, 0);
      expect(s.servicesTotal, 0);
      expect(s.alerts.active, 0);
    });

    test('present-but-null cpu/memory parses as null', () {
      final s = DashboardSummary.fromJson({
        'nodes': <String, dynamic>{},
        'pods': <String, dynamic>{},
        'services': <String, dynamic>{},
        'alerts': <String, dynamic>{},
        'cpu': null,
        'memory': null,
      });
      expect(s.cpu, isNull);
      expect(s.memory, isNull);
    });
  });

  group('Utilization.fromJson', () {
    test('canonical Prometheus payload', () {
      final u = Utilization.fromJson({
        'percentage': 42.5,
        'used': '4.2 cores',
        'total': '10 cores',
        'requests': '6 cores',
        'limits': '12 cores',
      });
      expect(u.percentage, 42.5);
      expect(u.unavailable, isFalse);
      expect(u.hasFinitePercentage, isTrue);
    });

    test('synthetic "Prometheus unavailable" payload detected', () {
      // backend/internal/k8s/resources/dashboard.go emits this shape
      // when Prom errors but allocatable>0.
      final u = Utilization.fromJson({
        'percentage': 0,
        'used': 'N/A',
        'total': '10 cores',
        'requests': '6 cores',
        'limits': '12 cores',
      });
      expect(u.unavailable, isTrue);
    });

    test('non-finite percentage from 0/0 division is normalized', () {
      final u = Utilization.fromJson({
        'percentage': double.nan,
        'used': '0',
        'total': '0',
      });
      // _asDouble guards against non-finite — value collapses to 0.
      expect(u.percentage, 0);
      expect(u.hasFinitePercentage, isTrue);
    });

    test('zero-but-real (used != "N/A") is not unavailable', () {
      final u = Utilization.fromJson({
        'percentage': 0,
        'used': '0 cores',
        'total': '10 cores',
      });
      expect(u.unavailable, isFalse);
    });
  });
}
