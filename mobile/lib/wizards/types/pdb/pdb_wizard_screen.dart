// PDB wizard screen. Configure step composes name + namespace +
// selector (KeyValueTable) + a SegmentedButton picker for policy +
// a single value field (string-shaped, accepts "2" or "50%").

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'pdb_wizard_controller.dart';

class PdbWizardScreen extends ConsumerStatefulWidget {
  const PdbWizardScreen({super.key});

  @override
  ConsumerState<PdbWizardScreen> createState() => _PdbWizardScreenState();
}

class _PdbWizardScreenState extends ConsumerState<PdbWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<PdbForm>(
      wizardType: 'pdb',
      title: 'New PodDisruptionBudget',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: pdbWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<PdbForm>(
              wizardKey: _wizardKey,
              controllerProvider: pdbWizardProvider,
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
    final state = ref.watch(pdbWizardProvider(wizardKey));
    final controller = ref.read(pdbWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'web-pdb',
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
        const SizedBox(height: 24),
        const WizardSectionHeader('Selector', subtitle: 'Pods this budget protects'),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.selector,
          onChanged: (kv) =>
              controller.updateForm((f) => f.copyWith(selector: kv)),
          keyLabel: 'Label key',
          valueLabel: 'Label value',
          errorMessage: stepErrors['selector'],
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Policy',
          subtitle: 'Pick exactly one. Values can be integers ("2") or percentages ("50%").',
        ),
        const SizedBox(height: 8),
        SegmentedButton<PdbPolicy>(
          segments: const [
            ButtonSegment(
                value: PdbPolicy.minAvailable, label: Text('minAvailable')),
            ButtonSegment(
                value: PdbPolicy.maxUnavailable,
                label: Text('maxUnavailable')),
          ],
          selected: {state.form.policy},
          showSelectedIcon: false,
          onSelectionChanged: (s) =>
              controller.updateForm((f) => f.copyWith(policy: s.first)),
        ),
        const SizedBox(height: 12),
        TextFormField(
          initialValue: state.form.value,
          decoration: InputDecoration(
            labelText: state.form.policy == PdbPolicy.minAvailable
                ? 'minAvailable'
                : 'maxUnavailable',
            hintText: '2 or 50%',
            border: const OutlineInputBorder(),
            errorText: stepErrors[state.form.policy == PdbPolicy.minAvailable
                ? 'minAvailable'
                : 'maxUnavailable'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(value: v.trim())),
        ),
      ],
    );
  }
}
