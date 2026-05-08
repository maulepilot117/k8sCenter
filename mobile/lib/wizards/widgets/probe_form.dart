// Probe form sub-widget. Used by Deployment, DaemonSet, StatefulSet
// (and Job/CronJob via container). Mirrors the backend's `ProbeInput`
// (`backend/internal/wizard/container.go:53`) — which only supports
// `http` and `tcp` handlers (no `exec`, no startup probe). The plan's
// "HTTP / TCP / Exec" language predates the backend constraint check;
// the surface here matches the backend exactly so apply never 422s on
// a handler the form would have accepted.
//
// Two probes are surfaced: liveness and readiness. Each is optional —
// when the operator hasn't enabled either, the wrapping ProbeForm
// emits null on `onChanged` for that probe slot, which the wizard
// controller maps to "omit field" in `toPreviewBody`.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

/// Mirrors the backend's `ProbeInput` JSON. Wizards build their form
/// state on top of this record so probe fields ride identically across
/// Deployment/Job/CronJob/DaemonSet/StatefulSet.
class ProbeData {
  const ProbeData({
    this.type = 'http',
    this.path = '/healthz',
    this.port = 8080,
    this.initialDelaySeconds = 0,
    this.periodSeconds = 10,
  });

  /// "http" or "tcp" — backend rejects anything else.
  final String type;
  final String path;
  final int port;
  final int initialDelaySeconds;
  final int periodSeconds;

  ProbeData copyWith({
    String? type,
    String? path,
    int? port,
    int? initialDelaySeconds,
    int? periodSeconds,
  }) =>
      ProbeData(
        type: type ?? this.type,
        path: path ?? this.path,
        port: port ?? this.port,
        initialDelaySeconds: initialDelaySeconds ?? this.initialDelaySeconds,
        periodSeconds: periodSeconds ?? this.periodSeconds,
      );

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{
      'type': type,
      'port': port,
    };
    if (type == 'http') {
      out['path'] = path;
    }
    if (initialDelaySeconds > 0) {
      out['initialDelaySeconds'] = initialDelaySeconds;
    }
    if (periodSeconds > 0) {
      out['periodSeconds'] = periodSeconds;
    }
    return out;
  }
}

const List<String> kProbeHandlers = ['http', 'tcp'];

/// Standalone probe section — the operator chooses to enable, picks
/// handler, fills in fields. Stateless except for the input controllers
/// it manages internally.
class ProbeFormSection extends StatefulWidget {
  const ProbeFormSection({
    super.key,
    required this.label,
    required this.probe,
    required this.onChanged,
    this.fieldErrors = const <String, String>{},
    this.fieldPrefix = 'probes.liveness',
  });

  final String label;
  final ProbeData? probe;

  /// Null means "delete this probe".
  final ValueChanged<ProbeData?> onChanged;

  /// Map of `<fieldPrefix>.<key>` → message. Wizard's stepErrors slice.
  final Map<String, String> fieldErrors;

  /// Backend field path prefix used to look up errors from
  /// [fieldErrors]. e.g., `probes.liveness` for Deployment, or
  /// `container.probes.liveness` for Job/CronJob/DaemonSet/StatefulSet.
  final String fieldPrefix;

  @override
  State<ProbeFormSection> createState() => _ProbeFormSectionState();
}

class _ProbeFormSectionState extends State<ProbeFormSection> {
  late final TextEditingController _pathCtl =
      TextEditingController(text: widget.probe?.path ?? '/healthz');
  late final TextEditingController _portCtl =
      TextEditingController(text: (widget.probe?.port ?? 8080).toString());
  late final TextEditingController _initialDelayCtl = TextEditingController(
      text: (widget.probe?.initialDelaySeconds ?? 0).toString());
  late final TextEditingController _periodCtl = TextEditingController(
      text: (widget.probe?.periodSeconds ?? 10).toString());

  @override
  void didUpdateWidget(covariant ProbeFormSection oldWidget) {
    super.didUpdateWidget(oldWidget);
    final p = widget.probe;
    if (p == null) return;
    if (_pathCtl.text != p.path) _pathCtl.text = p.path;
    if (_portCtl.text != p.port.toString()) {
      _portCtl.text = p.port.toString();
    }
    if (_initialDelayCtl.text != p.initialDelaySeconds.toString()) {
      _initialDelayCtl.text = p.initialDelaySeconds.toString();
    }
    if (_periodCtl.text != p.periodSeconds.toString()) {
      _periodCtl.text = p.periodSeconds.toString();
    }
  }

  @override
  void dispose() {
    _pathCtl.dispose();
    _portCtl.dispose();
    _initialDelayCtl.dispose();
    _periodCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final probe = widget.probe;
    final enabled = probe != null;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  widget.label,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Switch(
                value: enabled,
                onChanged: (v) {
                  if (v) {
                    widget.onChanged(probe ?? const ProbeData());
                  } else {
                    widget.onChanged(null);
                  }
                },
              ),
            ],
          ),
          if (enabled) ...[
            const SizedBox(height: 8),
            DropdownButtonFormField<String>(
              initialValue: probe.type,
              items: const [
                DropdownMenuItem(value: 'http', child: Text('HTTP GET')),
                DropdownMenuItem(value: 'tcp', child: Text('TCP socket')),
              ],
              onChanged: (v) {
                if (v == null) return;
                widget.onChanged(probe.copyWith(type: v));
              },
              decoration: InputDecoration(
                labelText: 'Handler',
                isDense: true,
                border: const OutlineInputBorder(),
                errorText: widget.fieldErrors['${widget.fieldPrefix}.type'],
              ),
            ),
            const SizedBox(height: 8),
            if (probe.type == 'http')
              TextField(
                controller: _pathCtl,
                onChanged: (v) =>
                    widget.onChanged(probe.copyWith(path: v)),
                decoration: InputDecoration(
                  labelText: 'Path',
                  hintText: '/healthz',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: widget.fieldErrors['${widget.fieldPrefix}.path'],
                ),
              ),
            if (probe.type == 'http') const SizedBox(height: 8),
            TextField(
              controller: _portCtl,
              keyboardType: TextInputType.number,
              onChanged: (v) {
                final n = int.tryParse(v) ?? 0;
                widget.onChanged(probe.copyWith(port: n));
              },
              decoration: InputDecoration(
                labelText: 'Port',
                hintText: '8080',
                isDense: true,
                border: const OutlineInputBorder(),
                errorText: widget.fieldErrors['${widget.fieldPrefix}.port'],
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _initialDelayCtl,
                    keyboardType: TextInputType.number,
                    onChanged: (v) {
                      final n = int.tryParse(v) ?? 0;
                      widget.onChanged(
                          probe.copyWith(initialDelaySeconds: n));
                    },
                    decoration: const InputDecoration(
                      labelText: 'Initial delay (s)',
                      isDense: true,
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: _periodCtl,
                    keyboardType: TextInputType.number,
                    onChanged: (v) {
                      final n = int.tryParse(v) ?? 0;
                      widget.onChanged(probe.copyWith(periodSeconds: n));
                    },
                    decoration: const InputDecoration(
                      labelText: 'Period (s)',
                      isDense: true,
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}
