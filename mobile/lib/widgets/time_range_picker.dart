// Time-range selector shared across every M4 observability surface
// (metrics, logs, policy compliance). Uses a SegmentedButton for
// the six preset slots; "Custom" opens the Material date-range picker
// so operators can investigate a specific incident window.
//
// `timeRangeFromPreset` is a pure top-level function so it can be
// called from provider code without a BuildContext (e.g. to seed the
// initial query key on first load).

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

/// The six time-window options surfaced in the picker.
enum TimePreset { last15m, last1h, last6h, last24h, last7d, custom }

/// Typed time window emitted by [TimeRangePicker] and accepted by
/// every chart and query provider in M4.
typedef TimeRange = ({DateTime start, DateTime end, TimePreset preset});

/// Converts a [TimePreset] to a concrete [TimeRange]. [now] defaults
/// to `DateTime.now()` and is injectable for tests.
TimeRange timeRangeFromPreset(TimePreset p, {DateTime? now}) {
  final end = now ?? DateTime.now();
  final Duration delta;
  switch (p) {
    case TimePreset.last15m:
      delta = const Duration(minutes: 15);
    case TimePreset.last1h:
      delta = const Duration(hours: 1);
    case TimePreset.last6h:
      delta = const Duration(hours: 6);
    case TimePreset.last24h:
      delta = const Duration(hours: 24);
    case TimePreset.last7d:
      delta = const Duration(days: 7);
    case TimePreset.custom:
      // Caller must supply explicit start/end for custom; fall back to 1h.
      delta = const Duration(hours: 1);
  }
  return (start: end.subtract(delta), end: end, preset: p);
}

/// Compact time-range picker rendered as a [SegmentedButton] row.
/// "Custom" opens [showDateRangePicker] and emits a [TimeRange] with
/// [TimePreset.custom] so callers can detect the operator chose an
/// explicit window rather than a rolling preset.
class TimeRangePicker extends StatefulWidget {
  const TimeRangePicker({
    required this.initial,
    required this.onChanged,
    super.key,
  });

  final TimeRange initial;
  final ValueChanged<TimeRange> onChanged;

  @override
  State<TimeRangePicker> createState() => _TimeRangePickerState();
}

class _TimeRangePickerState extends State<TimeRangePicker> {
  late TimePreset _selected;

  @override
  void initState() {
    super.initState();
    _selected = widget.initial.preset;
  }

  Future<void> _openCustomPicker() async {
    final result = await showDateRangePicker(
      context: context,
      firstDate: DateTime.now().subtract(const Duration(days: 365)),
      lastDate: DateTime.now(),
      initialDateRange: DateTimeRange(
        start: widget.initial.start,
        end: widget.initial.end,
      ),
    );
    if (result == null || !mounted) return;
    setState(() => _selected = TimePreset.custom);
    widget.onChanged((
      start: result.start,
      end: result.end,
      preset: TimePreset.custom,
    ));
  }

  void _selectPreset(TimePreset p) {
    if (p == TimePreset.custom) {
      _openCustomPicker();
      return;
    }
    setState(() => _selected = p);
    widget.onChanged(timeRangeFromPreset(p));
  }

  @override
  Widget build(BuildContext context) {
    // Read colors to style the button; actual coloring is driven by
    // Material's SegmentedButton theme which inherits ColorScheme.primary.
    Theme.of(context).extension<KubeColors>()!;

    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: SegmentedButton<TimePreset>(
        segments: const [
          ButtonSegment(value: TimePreset.last15m, label: Text('15m')),
          ButtonSegment(value: TimePreset.last1h, label: Text('1h')),
          ButtonSegment(value: TimePreset.last6h, label: Text('6h')),
          ButtonSegment(value: TimePreset.last24h, label: Text('24h')),
          ButtonSegment(value: TimePreset.last7d, label: Text('7d')),
          ButtonSegment(value: TimePreset.custom, label: Text('Custom')),
        ],
        selected: {_selected},
        onSelectionChanged: (s) => _selectPreset(s.first),
        showSelectedIcon: false,
        style: const ButtonStyle(
          visualDensity: VisualDensity.compact,
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
      ),
    );
  }
}
