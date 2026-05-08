// Ingress wizard screen. Single Configure step composes name +
// namespace + ingressClassName, rules (RepeatingRowGroup of host +
// nested RepeatingRowGroup of paths), and an optional TLS section.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'ingress_wizard_controller.dart';

class IngressWizardScreen extends ConsumerStatefulWidget {
  const IngressWizardScreen({super.key});

  @override
  ConsumerState<IngressWizardScreen> createState() =>
      _IngressWizardScreenState();
}

class _IngressWizardScreenState extends ConsumerState<IngressWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<IngressForm>(
      wizardType: 'ingress',
      title: 'New Ingress',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: ingressWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<IngressForm>(
              wizardKey: _wizardKey,
              controllerProvider: ingressWizardProvider,
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
    final state = ref.watch(ingressWizardProvider(wizardKey));
    final controller = ref.read(ingressWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'web',
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
        TextFormField(
          initialValue: state.form.ingressClassName,
          decoration: InputDecoration(
            labelText: 'Ingress class (optional)',
            hintText: 'nginx',
            border: const OutlineInputBorder(),
            errorText: stepErrors['ingressClassName'],
          ),
          onChanged: (v) => controller
              .updateForm((f) => f.copyWith(ingressClassName: v.trim())),
        ),
        const SizedBox(height: 24),
        WizardSectionHeader(
          'Rules',
          subtitle: 'Each rule maps a host (or any host) to one or more paths',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<IngressRule>(
          items: state.form.rules,
          addLabel: 'Add rule',
          onAdd: () => controller.updateForm(
              (f) => f.copyWith(rules: [...f.rules, const IngressRule()])),
          onRemove: (i) {
            final next = [...state.form.rules]..removeAt(i);
            controller.updateForm((f) => f.copyWith(rules: next));
          },
          itemBuilder: (ctx, i, rule) {
            return Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                border: Border.all(color: colors.borderSubtle),
                borderRadius: BorderRadius.circular(6),
              ),
              child: _RuleEditor(
                index: i,
                rule: rule,
                stepErrors: stepErrors,
                onChanged: (next) {
                  final list = [...state.form.rules];
                  list[i] = next;
                  controller.updateForm((f) => f.copyWith(rules: list));
                },
              ),
            );
          },
          errorMessage: stepErrors['rules'],
        ),
        const SizedBox(height: 24),
        WizardSectionHeader(
          'TLS',
          subtitle: 'Optional — bind hosts to TLS secrets',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<IngressTls>(
          items: state.form.tls,
          addLabel: 'Add TLS entry',
          emptyMessage: 'No TLS configured',
          onAdd: () => controller.updateForm(
              (f) => f.copyWith(tls: [...f.tls, const IngressTls()])),
          onRemove: (i) {
            final next = [...state.form.tls]..removeAt(i);
            controller.updateForm((f) => f.copyWith(tls: next));
          },
          itemBuilder: (ctx, i, t) {
            return _TlsEditor(
              index: i,
              tls: t,
              stepErrors: stepErrors,
              onChanged: (next) {
                final list = [...state.form.tls];
                list[i] = next;
                controller.updateForm((f) => f.copyWith(tls: list));
              },
            );
          },
        ),
      ],
    );
  }
}

class _RuleEditor extends StatelessWidget {
  const _RuleEditor({
    required this.index,
    required this.rule,
    required this.stepErrors,
    required this.onChanged,
  });

  final int index;
  final IngressRule rule;
  final Map<String, String> stepErrors;
  final ValueChanged<IngressRule> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextFormField(
          initialValue: rule.host,
          decoration: InputDecoration(
            labelText: 'Host (optional — leave blank to match all)',
            hintText: 'app.example.com',
            border: const OutlineInputBorder(),
            isDense: true,
            errorText: stepErrors['rules[$index].host'],
          ),
          onChanged: (v) => onChanged(rule.copyWith(host: v.trim())),
        ),
        const SizedBox(height: 8),
        Text(
          'Paths',
          style: TextStyle(
            color: colors.textMuted,
            fontSize: 12,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        RepeatingRowGroup<IngressPath>(
          items: rule.paths,
          addLabel: 'Add path',
          onAdd: () =>
              onChanged(rule.copyWith(paths: [...rule.paths, const IngressPath()])),
          onRemove: (i) {
            final next = [...rule.paths]..removeAt(i);
            onChanged(rule.copyWith(paths: next));
          },
          itemBuilder: (ctx, i, p) => _PathRow(
            ruleIndex: index,
            pathIndex: i,
            path: p,
            stepErrors: stepErrors,
            onChanged: (next) {
              final list = [...rule.paths];
              list[i] = next;
              onChanged(rule.copyWith(paths: list));
            },
          ),
          errorMessage: stepErrors['rules[$index].paths'],
        ),
      ],
    );
  }
}

class _PathRow extends StatefulWidget {
  const _PathRow({
    required this.ruleIndex,
    required this.pathIndex,
    required this.path,
    required this.stepErrors,
    required this.onChanged,
  });

  final int ruleIndex;
  final int pathIndex;
  final IngressPath path;
  final Map<String, String> stepErrors;
  final ValueChanged<IngressPath> onChanged;

  @override
  State<_PathRow> createState() => _PathRowState();
}

class _PathRowState extends State<_PathRow> {
  late final TextEditingController _path =
      TextEditingController(text: widget.path.path);
  late final TextEditingController _service =
      TextEditingController(text: widget.path.serviceName);
  late final TextEditingController _port = TextEditingController(
      text: widget.path.servicePort == 0 ? '' : '${widget.path.servicePort}');

  @override
  void didUpdateWidget(covariant _PathRow old) {
    super.didUpdateWidget(old);
    if (_path.text != widget.path.path) _path.text = widget.path.path;
    if (_service.text != widget.path.serviceName) {
      _service.text = widget.path.serviceName;
    }
    final next = widget.path.servicePort == 0
        ? ''
        : '${widget.path.servicePort}';
    if (_port.text != next) _port.text = next;
  }

  @override
  void dispose() {
    _path.dispose();
    _service.dispose();
    _port.dispose();
    super.dispose();
  }

  String? _err(String suffix) =>
      widget.stepErrors['rules[${widget.ruleIndex}].paths[${widget.pathIndex}]$suffix'];

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              flex: 5,
              child: TextField(
                controller: _path,
                onChanged: (v) =>
                    widget.onChanged(widget.path.copyWith(path: v)),
                decoration: InputDecoration(
                  labelText: 'Path',
                  hintText: '/',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.path'),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              flex: 4,
              child: DropdownButtonFormField<String>(
                initialValue: widget.path.pathType,
                isExpanded: true,
                decoration: InputDecoration(
                  labelText: 'Type',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.pathType'),
                ),
                items: [
                  for (final t in kIngressPathTypes)
                    DropdownMenuItem(value: t, child: Text(t)),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  widget.onChanged(widget.path.copyWith(pathType: v));
                },
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        Row(
          children: [
            Expanded(
              flex: 5,
              child: TextField(
                controller: _service,
                onChanged: (v) => widget.onChanged(
                    widget.path.copyWith(serviceName: v.trim())),
                decoration: InputDecoration(
                  labelText: 'Service name',
                  hintText: 'web',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.serviceName'),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              flex: 3,
              child: TextField(
                controller: _port,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                onChanged: (v) => widget.onChanged(widget.path
                    .copyWith(servicePort: int.tryParse(v) ?? 0)),
                decoration: InputDecoration(
                  labelText: 'Port',
                  hintText: '80',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.servicePort'),
                ),
              ),
            ),
          ],
        ),
      ],
    );
  }
}

class _TlsEditor extends StatefulWidget {
  const _TlsEditor({
    required this.index,
    required this.tls,
    required this.stepErrors,
    required this.onChanged,
  });

  final int index;
  final IngressTls tls;
  final Map<String, String> stepErrors;
  final ValueChanged<IngressTls> onChanged;

  @override
  State<_TlsEditor> createState() => _TlsEditorState();
}

class _TlsEditorState extends State<_TlsEditor> {
  late final TextEditingController _hosts =
      TextEditingController(text: widget.tls.hosts.join(', '));
  late final TextEditingController _secret =
      TextEditingController(text: widget.tls.secretName);

  @override
  void didUpdateWidget(covariant _TlsEditor old) {
    super.didUpdateWidget(old);
    final hostsText = widget.tls.hosts.join(', ');
    if (_hosts.text != hostsText) _hosts.text = hostsText;
    if (_secret.text != widget.tls.secretName) {
      _secret.text = widget.tls.secretName;
    }
  }

  @override
  void dispose() {
    _hosts.dispose();
    _secret.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        TextField(
          controller: _hosts,
          onChanged: (v) {
            final parsed = v
                .split(',')
                .map((s) => s.trim())
                .where((s) => s.isNotEmpty)
                .toList();
            widget.onChanged(widget.tls.copyWith(hosts: parsed));
          },
          decoration: InputDecoration(
            labelText: 'Hosts (comma-separated)',
            hintText: 'app.example.com, www.example.com',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText: widget.stepErrors['tls[${widget.index}].hosts'],
          ),
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _secret,
          onChanged: (v) =>
              widget.onChanged(widget.tls.copyWith(secretName: v.trim())),
          decoration: InputDecoration(
            labelText: 'TLS secret name',
            hintText: 'web-tls',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText:
                widget.stepErrors['tls[${widget.index}].secretName'],
          ),
        ),
      ],
    );
  }
}
