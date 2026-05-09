// Tests for the TimeRangePicker preset → range conversion plus the
// pure helper used by callers that want presets without a widget.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/widgets/time_range_picker.dart';

void main() {
  group('timeRangeFromPreset', () {
    final fixedNow = DateTime(2026, 5, 9, 12, 0, 0);

    test('last15m yields a 15-minute window ending at now', () {
      final r = timeRangeFromPreset(TimePreset.last15m, now: fixedNow);
      expect(r.end, fixedNow);
      expect(r.start, fixedNow.subtract(const Duration(minutes: 15)));
      expect(r.preset, TimePreset.last15m);
    });

    test('last1h yields a 1-hour window', () {
      final r = timeRangeFromPreset(TimePreset.last1h, now: fixedNow);
      expect(r.end.difference(r.start), const Duration(hours: 1));
      expect(r.preset, TimePreset.last1h);
    });

    test('last6h yields a 6-hour window', () {
      final r = timeRangeFromPreset(TimePreset.last6h, now: fixedNow);
      expect(r.end.difference(r.start), const Duration(hours: 6));
    });

    test('last24h yields a 24-hour window', () {
      final r = timeRangeFromPreset(TimePreset.last24h, now: fixedNow);
      expect(r.end.difference(r.start), const Duration(hours: 24));
    });

    test('last7d yields a 7-day window', () {
      final r = timeRangeFromPreset(TimePreset.last7d, now: fixedNow);
      expect(r.end.difference(r.start), const Duration(days: 7));
    });

    test('all preset windows end at the supplied now', () {
      for (final p in [
        TimePreset.last15m,
        TimePreset.last1h,
        TimePreset.last6h,
        TimePreset.last24h,
        TimePreset.last7d,
      ]) {
        final r = timeRangeFromPreset(p, now: fixedNow);
        expect(r.end, fixedNow,
            reason: 'preset $p should end at now (no future-date drift)');
      }
    });
  });
}
