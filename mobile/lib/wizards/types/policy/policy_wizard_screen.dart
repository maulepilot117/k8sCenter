// Policy wizard screen. Three steps:
//   0. Template — picker grouped by category (8 templates).
//   1. Configure — engine + name + action + targetKinds + scope
//      (excludedNamespaces) + per-template params (rendered generically
//      from `paramFields`).
//   2. Review — shared WizardReviewBody.
//
// Engine auto-detect runs once at wizard open via
// `policyEngineStatusProvider`. When neither Kyverno nor Gatekeeper
// is detected, the Template step renders an EmptyState in place of
// the picker — apply path is unreachable in that state because
// validateLocally on step 0 will gate "Pick a template".

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'policy_engine_status.dart';
import 'policy_templates.dart';
import 'policy_wizard_controller.dart';

const List<String> _kKyvernoActions = ['Audit', 'Enforce'];
const List<String> _kGatekeeperActions = ['deny', 'dryrun', 'warn'];
const List<String> _kAllowedTargetKinds = [
  'Pod',
  'Deployment',
  'StatefulSet',
  'DaemonSet',
  'ReplicaSet',
  'Job',
  'CronJob',
];

class PolicyWizardScreen extends ConsumerStatefulWidget {
  const PolicyWizardScreen({super.key});

  @override
  ConsumerState<PolicyWizardScreen> createState() => _PolicyWizardScreenState();
}

class _PolicyWizardScreenState extends ConsumerState<PolicyWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<PolicyForm>(
      wizardType: 'policy',
      title: 'New Policy',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: policyWizardProvider,
      stepBuilders: [
        (ctx) => _TemplateStep(wizardKey: _wizardKey),
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<PolicyForm>(
              wizardKey: _wizardKey,
              controllerProvider: policyWizardProvider,
            ),
      ],
    );
  }
}

class _TemplateStep extends ConsumerWidget {
  const _TemplateStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(policyWizardProvider(wizardKey));
    final controller = ref.read(policyWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    final statusAsync = ref.watch(policyEngineStatusProvider(
      PolicyEngineStatusKey(clusterId: wizardKey.clusterId),
    ));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        statusAsync.when(
          loading: () => const Padding(
            padding: EdgeInsets.symmetric(vertical: 24),
            child: Center(child: CircularProgressIndicator()),
          ),
          error: (e, _) => _StatusError(
            message: '$e',
            onRetry: () => ref.invalidate(policyEngineStatusProvider(
              PolicyEngineStatusKey(clusterId: wizardKey.clusterId),
            )),
          ),
          data: (status) {
            final engines = status.availableEngines;
            if (engines.isEmpty) {
              return _NoEngineEmpty(colors: colors);
            }
            // The controller's pickTemplate sets engine from the
            // template's first supported engine. If the cluster has
            // both engines, the operator can switch on the Configure
            // step. We surface the discovery result here as context.
            return Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _DiscoveredEnginesBanner(
                  engines: engines,
                  colors: colors,
                ),
                const SizedBox(height: 16),
                ..._buildTemplateGroups(
                  selectedId: state.form.templateId,
                  engines: engines,
                  // Thread the cluster-installed engine list into
                  // pickTemplate so the engine auto-default intersects
                  // template-supported engines with what's actually
                  // installed. Without this, every template picks
                  // `kyverno` (the first engine in template defs) and
                  // the wizard pins to Kyverno on a Gatekeeper-only
                  // cluster, with no UI to override (engine ChoiceChip
                  // is hidden when pickableEngines.length == 1).
                  onPick: (id) =>
                      controller.pickTemplate(id, availableEngines: engines),
                  colors: colors,
                ),
                if (stepErrors['templateId'] != null) ...[
                  const SizedBox(height: 12),
                  Text(
                    stepErrors['templateId']!,
                    style: TextStyle(color: colors.error),
                  ),
                ],
              ],
            );
          },
        ),
      ],
    );
  }

  List<Widget> _buildTemplateGroups({
    required String selectedId,
    required List<String> engines,
    required ValueChanged<String> onPick,
    required KubeColors colors,
  }) {
    final out = <Widget>[];
    for (final cat in kPolicyCategories) {
      final templates = kPolicyTemplates
          .where((t) =>
              t.category == cat &&
              t.engines.any((e) => engines.contains(e)))
          .toList();
      if (templates.isEmpty) continue;
      out.add(WizardSectionHeader(cat));
      out.add(const SizedBox(height: 8));
      for (final t in templates) {
        out.add(_TemplateTile(
          template: t,
          selected: t.id == selectedId,
          onTap: () => onPick(t.id),
          colors: colors,
        ));
        out.add(const SizedBox(height: 6));
      }
      out.add(const SizedBox(height: 12));
    }
    return out;
  }
}

class _DiscoveredEnginesBanner extends StatelessWidget {
  const _DiscoveredEnginesBanner({
    required this.engines,
    required this.colors,
  });
  final List<String> engines;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final label = engines.length == 1
        ? 'Detected engine: ${engines.first}'
        : 'Detected engines: ${engines.join(", ")}';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: colors.accent.withValues(alpha: 0.08),
        border: Border.all(color: colors.accent),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(children: [
        Icon(Icons.check_circle_outline, color: colors.accent, size: 16),
        const SizedBox(width: 8),
        Expanded(
          child: Text(
            label,
            style: TextStyle(color: colors.textPrimary, fontSize: 12),
          ),
        ),
      ]),
    );
  }
}

class _NoEngineEmpty extends StatelessWidget {
  const _NoEngineEmpty({required this.colors});
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.policy_outlined, color: colors.textMuted, size: 40),
          const SizedBox(height: 8),
          Text(
            'No policy engine installed',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'Install Kyverno or Gatekeeper on this cluster before using the '
            'Policy wizard. Open k8sCenter on a desktop for installation '
            'guidance.',
            style: TextStyle(color: colors.textSecondary),
          ),
        ],
      ),
    );
  }
}

class _StatusError extends StatelessWidget {
  const _StatusError({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(color: colors.error),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Failed to detect policy engine: $message',
            style: TextStyle(color: colors.error),
          ),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerRight,
            child: ElevatedButton.icon(
              onPressed: onRetry,
              icon: const Icon(Icons.refresh, size: 16),
              label: const Text('Retry'),
            ),
          ),
        ],
      ),
    );
  }
}

class _NoPickableEngineBanner extends StatelessWidget {
  const _NoPickableEngineBanner({
    required this.templateName,
    required this.onGoBack,
  });
  final String templateName;
  final VoidCallback onGoBack;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(color: colors.warning),
        borderRadius: BorderRadius.circular(6),
        color: colors.warning.withValues(alpha: 0.08),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            Icon(Icons.warning_amber_outlined,
                color: colors.warning, size: 18),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                'Template no longer supported on this cluster',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ]),
          const SizedBox(height: 6),
          Text(
            '"$templateName" requires an engine that is not currently '
            'installed on this cluster. Pick a different template.',
            style: TextStyle(color: colors.textSecondary),
          ),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerRight,
            child: ElevatedButton.icon(
              onPressed: onGoBack,
              icon: const Icon(Icons.arrow_back, size: 16),
              label: const Text('Pick a different template'),
            ),
          ),
        ],
      ),
    );
  }
}

class _TemplateTile extends StatelessWidget {
  const _TemplateTile({
    required this.template,
    required this.selected,
    required this.onTap,
    required this.colors,
  });

  final PolicyTemplateInfo template;
  final bool selected;
  final VoidCallback onTap;
  final KubeColors colors;

  Color _severityColor() {
    switch (template.severity) {
      case 'high':
        return colors.error;
      case 'medium':
        return colors.warning;
      default:
        return colors.textMuted;
    }
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border.all(
            color: selected ? colors.accent : colors.borderSubtle,
            width: selected ? 2 : 1,
          ),
          borderRadius: BorderRadius.circular(8),
          color: selected
              ? colors.accent.withValues(alpha: 0.08)
              : Colors.transparent,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(children: [
              Expanded(
                child: Text(
                  template.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: _severityColor().withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  template.severity.toUpperCase(),
                  style: TextStyle(
                    color: _severityColor(),
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ]),
            const SizedBox(height: 4),
            Text(
              template.description,
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(policyWizardProvider(wizardKey));
    final controller = ref.read(policyWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[1] ?? const <String, String>{};
    final form = state.form;
    final template = findPolicyTemplate(form.templateId);

    final statusAsync = ref.watch(policyEngineStatusProvider(
      PolicyEngineStatusKey(clusterId: wizardKey.clusterId),
    ));
    final availableEngines = statusAsync.maybeWhen(
      data: (s) => s.availableEngines,
      orElse: () => const <String>[],
    );

    if (template == null) {
      return Padding(
        padding: const EdgeInsets.all(12),
        child: Text(
          'Pick a template on the previous step before configuring.',
          style: TextStyle(
            color: Theme.of(context).extension<KubeColors>()!.textMuted,
          ),
        ),
      );
    }

    // Engines the operator can pick = intersection of cluster-installed
    // engines and template-supported engines.
    final pickableEngines = template.engines
        .where((e) => availableEngines.contains(e))
        .toList();
    final actions =
        form.engine == 'gatekeeper' ? _kGatekeeperActions : _kKyvernoActions;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        // Engine availability sanity check. If the cluster's engine
        // list changed out-of-band (e.g. Kyverno uninstalled mid-wizard
        // via GitOps reconcile), the picked template may no longer
        // be supported by any installed engine. The engine ChoiceChip
        // is hidden when pickableEngines.length <= 1, so without this
        // banner the wizard becomes a dead-end where the operator
        // cannot change engine and Apply is destined to 422. Surface
        // the situation explicitly with an action to go back.
        if (pickableEngines.isEmpty) ...[
          _NoPickableEngineBanner(
            templateName: template.name,
            onGoBack: () => controller.goToStep(0),
          ),
          const SizedBox(height: 16),
        ],
        TextFormField(
          initialValue: form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: template.id,
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        if (pickableEngines.length > 1) ...[
          const WizardSectionHeader('Engine'),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            children: [
              for (final e in pickableEngines)
                ChoiceChip(
                  label: Text(e),
                  selected: form.engine == e,
                  onSelected: (_) => controller.setEngine(e),
                ),
            ],
          ),
          if (stepErrors['engine'] != null) ...[
            const SizedBox(height: 4),
            Text(stepErrors['engine']!,
                style: TextStyle(
                  color: Theme.of(context).extension<KubeColors>()!.error,
                )),
          ],
          const SizedBox(height: 16),
        ],
        DropdownButtonFormField<String>(
          initialValue: actions.contains(form.action) ? form.action : null,
          decoration: InputDecoration(
            labelText: 'Action',
            helperText: form.engine == 'gatekeeper'
                ? 'deny blocks, dryrun audits, warn audits with a notice'
                : 'Audit logs violations; Enforce blocks them',
            border: const OutlineInputBorder(),
            errorText: stepErrors['action'],
          ),
          items: [
            for (final a in actions)
              DropdownMenuItem(value: a, child: Text(a)),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(action: v));
          },
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader(
          'Target kinds',
          subtitle: 'Resource kinds this policy applies to',
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          children: [
            for (final k in _kAllowedTargetKinds)
              FilterChip(
                label: Text(k),
                selected: form.targetKinds.contains(k),
                onSelected: (sel) {
                  final next = [...form.targetKinds];
                  if (sel) {
                    if (!next.contains(k)) next.add(k);
                  } else {
                    next.remove(k);
                  }
                  controller.updateForm((f) => f.copyWith(targetKinds: next));
                },
              ),
          ],
        ),
        if (stepErrors['targetKinds'] != null) ...[
          const SizedBox(height: 4),
          Text(stepErrors['targetKinds']!,
              style: TextStyle(
                color: Theme.of(context).extension<KubeColors>()!.error,
              )),
        ],
        const SizedBox(height: 16),
        TextFormField(
          initialValue: form.description,
          decoration: InputDecoration(
            labelText: 'Description (optional)',
            border: const OutlineInputBorder(),
            errorText: stepErrors['description'],
          ),
          maxLines: 2,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(description: v)),
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader(
          'Excluded namespaces',
          subtitle: 'System namespaces excluded by default; edit as needed',
        ),
        const SizedBox(height: 8),
        _StringListEditor(
          values: form.excludedNamespaces,
          hintText: 'kube-system',
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(excludedNamespaces: next)),
        ),
        if (template.paramFields.isNotEmpty) ...[
          const SizedBox(height: 24),
          const WizardSectionHeader(
            'Parameters',
            subtitle: 'Template-specific configuration',
          ),
          const SizedBox(height: 8),
          for (final p in template.paramFields)
            _ParamFieldWidget(
              spec: p,
              value: form.params[p.key],
              error: stepErrors['params.${p.key}'],
              onChanged: (v) => controller.setParam(p.key, v),
            ),
        ],
      ],
    );
  }
}

/// Renders one of: boolean (Switch), string (TextField), stringList
/// (RepeatingRowGroup of strings).
class _ParamFieldWidget extends StatelessWidget {
  const _ParamFieldWidget({
    required this.spec,
    required this.value,
    required this.error,
    required this.onChanged,
  });

  final PolicyParamField spec;
  final Object? value;
  final String? error;
  final ValueChanged<Object> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    Widget child;
    switch (spec.type) {
      case 'boolean':
        final b = value is bool ? value as bool : false;
        child = SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: Text(spec.label),
          subtitle: Text(
            spec.description,
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
          value: b,
          onChanged: (v) => onChanged(v),
        );
        break;
      case 'string':
        final s = value is String ? value as String : '';
        child = _ParamStringField(
          initialValue: s,
          label: spec.label + (spec.required ? ' *' : ''),
          description: spec.description,
          error: error,
          onChanged: (v) => onChanged(v),
        );
        break;
      case 'stringList':
      default:
        final list =
            value is List ? List<String>.from(value as List) : <String>[];
        child = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              spec.label + (spec.required ? ' *' : ''),
              style: TextStyle(
                color: colors.textPrimary,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              spec.description,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
            const SizedBox(height: 8),
            _StringListEditor(
              values: list,
              hintText: spec.label.toLowerCase(),
              onChanged: (next) => onChanged(next),
            ),
            if (error != null) ...[
              const SizedBox(height: 4),
              Text(error!, style: TextStyle(color: colors.error, fontSize: 12)),
            ],
          ],
        );
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: child,
    );
  }
}

class _ParamStringField extends StatefulWidget {
  const _ParamStringField({
    required this.initialValue,
    required this.label,
    required this.description,
    required this.error,
    required this.onChanged,
  });

  final String initialValue;
  final String label;
  final String description;
  final String? error;
  final ValueChanged<String> onChanged;

  @override
  State<_ParamStringField> createState() => _ParamStringFieldState();
}

class _ParamStringFieldState extends State<_ParamStringField> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.initialValue);

  @override
  void didUpdateWidget(covariant _ParamStringField old) {
    super.didUpdateWidget(old);
    if (widget.initialValue != _ctl.text &&
        widget.initialValue != old.initialValue) {
      _ctl.text = widget.initialValue;
    }
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: _ctl,
      decoration: InputDecoration(
        labelText: widget.label,
        helperText: widget.description,
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
      onChanged: widget.onChanged,
    );
  }
}

/// Repeating list of single-string rows (used for excludedNamespaces
/// and any `stringList` param). Sentinel-empty rows aren't dropped at
/// emit time so the operator's draft survives — empty rows filter out
/// at preview-body composition time.
class _StringListEditor extends StatelessWidget {
  const _StringListEditor({
    required this.values,
    required this.hintText,
    required this.onChanged,
  });

  final List<String> values;
  final String hintText;
  final ValueChanged<List<String>> onChanged;

  @override
  Widget build(BuildContext context) {
    return RepeatingRowGroup<String>(
      items: values.isEmpty ? const [''] : values,
      addLabel: 'Add',
      onAdd: () => onChanged([...values, '']),
      onRemove: (i) {
        final next = [...values]..removeAt(i);
        onChanged(next);
      },
      itemBuilder: (ctx, i, v) => _StringRow(
        initialValue: v,
        hintText: hintText,
        onChanged: (next) {
          final list = [...values];
          while (list.length <= i) {
            list.add('');
          }
          list[i] = next;
          // Drop trailing empties so the controller's persistent state
          // doesn't accumulate sentinel rows on every keystroke.
          while (list.isNotEmpty && list.last.trim().isEmpty) {
            list.removeLast();
          }
          onChanged(list);
        },
      ),
    );
  }
}

class _StringRow extends StatefulWidget {
  const _StringRow({
    required this.initialValue,
    required this.hintText,
    required this.onChanged,
  });

  final String initialValue;
  final String hintText;
  final ValueChanged<String> onChanged;

  @override
  State<_StringRow> createState() => _StringRowState();
}

class _StringRowState extends State<_StringRow> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.initialValue);

  @override
  void didUpdateWidget(covariant _StringRow old) {
    super.didUpdateWidget(old);
    if (widget.initialValue != _ctl.text &&
        widget.initialValue != old.initialValue) {
      _ctl.text = widget.initialValue;
    }
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: _ctl,
      decoration: InputDecoration(
        hintText: widget.hintText,
        isDense: true,
        border: const OutlineInputBorder(),
      ),
      onChanged: widget.onChanged,
    );
  }
}
