// Shared container-shape data records and row widgets used by every
// workload wizard (Deployment, Job, CronJob, DaemonSet, StatefulSet).
// Mirrors the backend's `ContainerInput` (`backend/internal/wizard/
// container.go:69`).
//
// Each record carries the minimal fields the form surfaces; serializes
// to the JSON shape the backend expects via `toJson` / collection
// helpers.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';
import 'probe_form.dart';
import 'repeating_row_group.dart';

/// Sentinel for `copyWith` parameters on form records that have
/// nullable fields (e.g. Deployment.liveness, Job.parallelism). Lets
/// callers distinguish "no change" (param omitted) from "set to null"
/// (param passed as null). Single canonical instance shared across
/// every wizard's controller.
const Object kFormFieldUnset = Object();

/// Mirrors backend `EnvVarInput`. We surface only the literal-value
/// case in M3 PR-3b — configMapRef/secretRef refs are deferrable and
/// add a separate sub-form (operator picks the source ConfigMap/Secret
/// from a list). Plan keeps that for a follow-up.
class EnvVarData {
  const EnvVarData({this.name = '', this.value = ''});

  final String name;
  final String value;

  EnvVarData copyWith({String? name, String? value}) =>
      EnvVarData(name: name ?? this.name, value: value ?? this.value);

  Map<String, dynamic> toJson() => {'name': name, 'value': value};

  bool get isEmpty => name.isEmpty && value.isEmpty;
}

/// Strip empty rows + emit JSON list. Used by all five workload
/// wizards to serialize their env var section identically.
List<Map<String, dynamic>> envVarsAsJson(List<EnvVarData> envVars) {
  return [
    for (final e in envVars)
      if (!e.isEmpty) e.toJson(),
  ];
}

/// Build the `container` JSON sub-object shared by Job, CronJob,
/// DaemonSet, and StatefulSet. Optional [liveness]/[readiness] fold
/// the probe block in when present so DaemonSet's controller doesn't
/// need to hand-roll the same shape inline.
Map<String, dynamic> buildContainerJson({
  required String image,
  required List<EnvVarData> envVars,
  ProbeData? liveness,
  ProbeData? readiness,
}) {
  final out = <String, dynamic>{'image': image};
  final ev = envVarsAsJson(envVars);
  if (ev.isNotEmpty) out['envVars'] = ev;
  if (liveness != null || readiness != null) {
    final probes = <String, dynamic>{};
    if (liveness != null) probes['liveness'] = liveness.toJson();
    if (readiness != null) probes['readiness'] = readiness.toJson();
    out['probes'] = probes;
  }
  return out;
}

/// Mirrors backend `PortInput` for a container (NOT Service port).
class ContainerPortData {
  const ContainerPortData({
    this.name = '',
    this.containerPort = 0,
    this.protocol = 'TCP',
  });

  final String name;
  final int containerPort;
  final String protocol;

  ContainerPortData copyWith({
    String? name,
    int? containerPort,
    String? protocol,
  }) =>
      ContainerPortData(
        name: name ?? this.name,
        containerPort: containerPort ?? this.containerPort,
        protocol: protocol ?? this.protocol,
      );

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{'containerPort': containerPort};
    if (name.isNotEmpty) out['name'] = name;
    if (protocol.isNotEmpty) out['protocol'] = protocol;
    return out;
  }

  bool get isEmpty => containerPort == 0 && name.isEmpty;
}

List<Map<String, dynamic>> containerPortsAsJson(List<ContainerPortData> ports) {
  return [
    for (final p in ports)
      if (!p.isEmpty) p.toJson(),
  ];
}

const List<String> kContainerPortProtocols = ['TCP', 'UDP'];

/// Compact two-field env var row. Designed to live inside
/// `RepeatingRowGroup<EnvVarData>`.
class EnvVarRow extends StatefulWidget {
  const EnvVarRow({
    super.key,
    required this.value,
    required this.onChanged,
  });

  final EnvVarData value;
  final ValueChanged<EnvVarData> onChanged;

  @override
  State<EnvVarRow> createState() => _EnvVarRowState();
}

class _EnvVarRowState extends State<EnvVarRow> {
  late final TextEditingController _name =
      TextEditingController(text: widget.value.name);
  late final TextEditingController _value =
      TextEditingController(text: widget.value.value);

  @override
  void didUpdateWidget(covariant EnvVarRow oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_name.text != widget.value.name) _name.text = widget.value.name;
    if (_value.text != widget.value.value) _value.text = widget.value.value;
  }

  @override
  void dispose() {
    _name.dispose();
    _value.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          flex: 4,
          child: TextField(
            controller: _name,
            onChanged: (v) =>
                widget.onChanged(widget.value.copyWith(name: v)),
            decoration: const InputDecoration(
              labelText: 'Name',
              hintText: 'KEY',
              isDense: true,
              border: OutlineInputBorder(),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          flex: 6,
          child: TextField(
            controller: _value,
            onChanged: (v) =>
                widget.onChanged(widget.value.copyWith(value: v)),
            decoration: const InputDecoration(
              labelText: 'Value',
              isDense: true,
              border: OutlineInputBorder(),
            ),
          ),
        ),
      ],
    );
  }
}

/// Container port row — three fields (name, port, protocol).
class ContainerPortRow extends StatefulWidget {
  const ContainerPortRow({
    super.key,
    required this.value,
    required this.onChanged,
    this.portError,
  });

  final ContainerPortData value;
  final ValueChanged<ContainerPortData> onChanged;
  final String? portError;

  @override
  State<ContainerPortRow> createState() => _ContainerPortRowState();
}

class _ContainerPortRowState extends State<ContainerPortRow> {
  late final TextEditingController _name =
      TextEditingController(text: widget.value.name);
  late final TextEditingController _port = TextEditingController(
      text: widget.value.containerPort > 0
          ? widget.value.containerPort.toString()
          : '');

  @override
  void didUpdateWidget(covariant ContainerPortRow oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_name.text != widget.value.name) _name.text = widget.value.name;
    final next = widget.value.containerPort > 0
        ? widget.value.containerPort.toString()
        : '';
    if (_port.text != next) _port.text = next;
  }

  @override
  void dispose() {
    _name.dispose();
    _port.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          flex: 4,
          child: TextField(
            controller: _name,
            onChanged: (v) =>
                widget.onChanged(widget.value.copyWith(name: v)),
            decoration: const InputDecoration(
              labelText: 'Name (optional)',
              hintText: 'http',
              isDense: true,
              border: OutlineInputBorder(),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          flex: 3,
          child: TextField(
            controller: _port,
            keyboardType: TextInputType.number,
            onChanged: (v) {
              // Silent no-op on unparseable input — keeps the last
              // good port in form state until the operator corrects.
              final n = int.tryParse(v);
              if (n == null) return;
              widget.onChanged(widget.value.copyWith(containerPort: n));
            },
            decoration: InputDecoration(
              labelText: 'Port',
              hintText: '80',
              isDense: true,
              border: const OutlineInputBorder(),
              errorText: widget.portError,
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          flex: 3,
          child: DropdownButtonFormField<String>(
            initialValue: widget.value.protocol,
            items: const [
              DropdownMenuItem(value: 'TCP', child: Text('TCP')),
              DropdownMenuItem(value: 'UDP', child: Text('UDP')),
            ],
            onChanged: (v) {
              if (v == null) return;
              widget.onChanged(widget.value.copyWith(protocol: v));
            },
            decoration: const InputDecoration(
              labelText: 'Proto',
              isDense: true,
              border: OutlineInputBorder(),
            ),
          ),
        ),
      ],
    );
  }
}

/// Reusable env-var section: heading + `RepeatingRowGroup<EnvVarData>`.
/// Replaces the ~14-line copy that previously lived in every workload
/// wizard screen.
class EnvVarSection extends StatelessWidget {
  const EnvVarSection({
    super.key,
    required this.items,
    required this.onChanged,
    this.errorMessage,
  });

  final List<EnvVarData> items;
  final ValueChanged<List<EnvVarData>> onChanged;
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Environment variables',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 12),
        RepeatingRowGroup<EnvVarData>(
          items: items,
          itemBuilder: (ctx, i, item) => EnvVarRow(
            value: item,
            onChanged: (next) {
              final list = [...items];
              list[i] = next;
              onChanged(list);
            },
          ),
          onAdd: () => onChanged([...items, const EnvVarData()]),
          onRemove: (i) {
            final list = [...items]..removeAt(i);
            onChanged(list);
          },
          addLabel: 'Add env var',
          emptyMessage: 'No env vars defined.',
          errorMessage: errorMessage,
        ),
      ],
    );
  }
}
