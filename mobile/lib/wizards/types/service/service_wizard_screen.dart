// Service wizard screen. Configure step composes name/ns/type
// dropdowns, the KeyValueTable for selector + labels, and a small
// ports table that adds rows on the fly. Same review-step shape as
// ConfigMap and Secret.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'service_wizard_controller.dart';

class ServiceWizardScreen extends ConsumerStatefulWidget {
  const ServiceWizardScreen({super.key});

  @override
  ConsumerState<ServiceWizardScreen> createState() =>
      _ServiceWizardScreenState();
}

class _ServiceWizardScreenState
    extends ConsumerState<ServiceWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<ServiceForm>(
      wizardType: 'service',
      title: 'New Service',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: serviceWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<ServiceForm>(
              wizardKey: _wizardKey,
              controllerProvider: serviceWizardProvider,
            ),
      ],
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(serviceWizardProvider(wizardKey));
    final controller = ref.read(serviceWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'my-service',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        DropdownButtonFormField<String>(
          initialValue: state.form.type,
          decoration: InputDecoration(
            labelText: 'Type',
            border: const OutlineInputBorder(),
            errorText: stepErrors['type'],
          ),
          items: [
            for (final t in kServiceTypes)
              DropdownMenuItem(value: t, child: Text(t)),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(type: v));
          },
        ),
        const SizedBox(height: 24),
        _SectionHeader('Selector', subtitle: 'Match pods by label'),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.selector,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(selector: pairs)),
          keyLabel: 'Label key',
          valueLabel: 'Label value',
          errorMessage: stepErrors['selector'],
        ),
        const SizedBox(height: 24),
        _SectionHeader(
          'Labels',
          subtitle: 'Optional metadata labels on the Service',
        ),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.labels,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(labels: pairs)),
          keyLabel: 'Label key',
          valueLabel: 'Label value',
        ),
        const SizedBox(height: 24),
        _SectionHeader(
          'Ports',
          subtitle: 'At least one port mapping is required',
        ),
        const SizedBox(height: 8),
        _PortsEditor(
          ports: state.form.ports,
          onChanged: (ports) =>
              controller.updateForm((f) => f.copyWith(ports: ports)),
          stepErrors: stepErrors,
        ),
        if (stepErrors['ports'] != null) ...[
          const SizedBox(height: 8),
          Text(
            stepErrors['ports']!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title, {this.subtitle});
  final String title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        if (subtitle != null)
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Text(
              subtitle!,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
      ],
    );
  }
}

/// Map a display-row index to the index the backend reports errors
/// against for `ports[N]` paths, accounting for `portsAsJson()`
/// stripping empty rows. Returns null when [displayIndex] is out of
/// range or the row at [displayIndex] is itself empty.
int? _portsServerIndexFor(List<ServicePort> rows, int displayIndex) {
  if (displayIndex < 0 || displayIndex >= rows.length) return null;
  if (rows[displayIndex].isEmpty) return null;
  var serverIndex = 0;
  for (var i = 0; i < displayIndex; i++) {
    if (!rows[i].isEmpty) serverIndex++;
  }
  return serverIndex;
}

/// Mini repeating-row editor for ServicePort entries. Trailing empty
/// row is auto-rendered so operators don't need an explicit "Add"
/// button.
class _PortsEditor extends StatelessWidget {
  const _PortsEditor({
    required this.ports,
    required this.onChanged,
    required this.stepErrors,
  });

  final List<ServicePort> ports;
  final ValueChanged<List<ServicePort>> onChanged;
  final Map<String, String> stepErrors;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final display = _displayPorts(ports);

    return Column(
      children: [
        for (var i = 0; i < display.length; i++)
          Builder(
            builder: (context) {
              // portsAsJson() strips empty rows before send, so
              // server-reported errors are indexed against the stripped
              // list. Map display-row index → server index so the error
              // lands on the row the operator actually filled.
              final serverIndex = _portsServerIndexFor(display, i);
              return Padding(
                padding: EdgeInsets.only(
                    bottom: i == display.length - 1 ? 0 : 8),
                child: _PortRow(
                  port: display[i],
                  showRemove:
                      !(i == display.length - 1 && display[i].isEmpty),
                  portError: serverIndex == null
                      ? null
                      : stepErrors['ports[$serverIndex].port'],
                  targetPortError: serverIndex == null
                      ? null
                      : stepErrors['ports[$serverIndex].targetPort'],
                  nameError: serverIndex == null
                      ? null
                      : stepErrors['ports[$serverIndex].name'],
                  onChanged: (next) => _emit(_replace(display, i, next)),
                  onRemove: () => _emit(_removeAt(display, i)),
                  colors: colors,
                ),
              );
            },
          ),
      ],
    );
  }

  List<ServicePort> _displayPorts(List<ServicePort> ports) {
    if (ports.isEmpty || !ports.last.isEmpty) {
      return [...ports, const ServicePort()];
    }
    return ports;
  }

  List<ServicePort> _replace(List<ServicePort> rows, int i, ServicePort v) {
    final out = [...rows];
    out[i] = v;
    return out;
  }

  List<ServicePort> _removeAt(List<ServicePort> rows, int i) {
    final out = [...rows]..removeAt(i);
    return out;
  }

  void _emit(List<ServicePort> rows) {
    final cleaned = [...rows];
    while (cleaned.isNotEmpty && cleaned.last.isEmpty) {
      cleaned.removeLast();
    }
    onChanged(cleaned);
  }
}

class _PortRow extends StatefulWidget {
  const _PortRow({
    required this.port,
    required this.showRemove,
    required this.portError,
    required this.targetPortError,
    required this.nameError,
    required this.onChanged,
    required this.onRemove,
    required this.colors,
  });

  final ServicePort port;
  final bool showRemove;
  final String? portError;
  final String? targetPortError;
  final String? nameError;
  final ValueChanged<ServicePort> onChanged;
  final VoidCallback onRemove;
  final KubeColors colors;

  @override
  State<_PortRow> createState() => _PortRowState();
}

class _PortRowState extends State<_PortRow> {
  late final TextEditingController _name =
      TextEditingController(text: widget.port.name);
  late final TextEditingController _port = TextEditingController(
      text: widget.port.port == 0 ? '' : '${widget.port.port}');
  late final TextEditingController _target = TextEditingController(
      text: widget.port.targetPort == 0 ? '' : '${widget.port.targetPort}');

  @override
  void didUpdateWidget(covariant _PortRow old) {
    super.didUpdateWidget(old);
    final p = widget.port;
    if (_name.text != p.name) _name.text = p.name;
    final portText = p.port == 0 ? '' : '${p.port}';
    if (_port.text != portText) _port.text = portText;
    final targetText = p.targetPort == 0 ? '' : '${p.targetPort}';
    if (_target.text != targetText) _target.text = targetText;
  }

  @override
  void dispose() {
    _name.dispose();
    _port.dispose();
    _target.dispose();
    super.dispose();
  }

  void _emit(ServicePort next) => widget.onChanged(next);

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              flex: 4,
              child: TextField(
                controller: _name,
                onChanged: (v) => _emit(widget.port.copyWith(name: v)),
                decoration: InputDecoration(
                  labelText: 'Name (optional)',
                  hintText: 'http',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: widget.nameError,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              flex: 3,
              child: TextField(
                controller: _port,
                keyboardType: TextInputType.number,
                inputFormatters: [
                  FilteringTextInputFormatter.digitsOnly,
                ],
                onChanged: (v) => _emit(widget.port.copyWith(
                  port: int.tryParse(v) ?? 0,
                )),
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
              child: TextField(
                controller: _target,
                keyboardType: TextInputType.number,
                inputFormatters: [
                  FilteringTextInputFormatter.digitsOnly,
                ],
                onChanged: (v) => _emit(widget.port.copyWith(
                  targetPort: int.tryParse(v) ?? 0,
                )),
                decoration: InputDecoration(
                  labelText: 'Target',
                  hintText: '8080',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: widget.targetPortError,
                ),
              ),
            ),
            const SizedBox(width: 4),
            DropdownButton<String>(
              value: widget.port.protocol,
              underline: const SizedBox.shrink(),
              items: [
                for (final p in kServiceProtocols)
                  DropdownMenuItem(value: p, child: Text(p)),
              ],
              onChanged: (v) {
                if (v == null) return;
                _emit(widget.port.copyWith(protocol: v));
              },
            ),
            IconButton(
              tooltip: 'Remove port',
              visualDensity: VisualDensity.compact,
              onPressed: widget.showRemove ? widget.onRemove : null,
              icon: Icon(
                Icons.close,
                color: widget.colors.textMuted,
                size: 18,
              ),
            ),
          ],
        ),
      ],
    );
  }
}

