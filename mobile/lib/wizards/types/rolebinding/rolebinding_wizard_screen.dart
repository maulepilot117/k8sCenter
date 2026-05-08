// RoleBinding wizard screen. Configure step composes name + namespace
// + roleRef (KindPicker → NamedResourcePicker scoped to active
// namespace for Role / cluster-wide for ClusterRole) + subjects
// (RepeatingRowGroup of {kind, name, namespace?}).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/kind_picker.dart';
import '../../widgets/named_resource_picker.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'rolebinding_wizard_controller.dart';

class RoleBindingWizardScreen extends ConsumerStatefulWidget {
  const RoleBindingWizardScreen({super.key});

  @override
  ConsumerState<RoleBindingWizardScreen> createState() =>
      _RoleBindingWizardScreenState();
}

class _RoleBindingWizardScreenState
    extends ConsumerState<RoleBindingWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<RoleBindingForm>(
      wizardType: 'rolebinding',
      title: 'New RoleBinding',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: roleBindingWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<RoleBindingForm>(
              wizardKey: _wizardKey,
              controllerProvider: roleBindingWizardProvider,
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
    final state = ref.watch(roleBindingWizardProvider(wizardKey));
    final controller =
        ref.read(roleBindingWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    final isClusterRole = state.form.roleKind == 'ClusterRole';
    final roleListKind = isClusterRole ? 'clusterroles' : 'roles';
    final roleListNamespace = isClusterRole
        ? null
        : (state.form.namespace.trim().isEmpty
            ? null
            : state.form.namespace.trim());

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'view-binding',
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
        const WizardSectionHeader('Role reference'),
        const SizedBox(height: 8),
        KindPicker(
          options: const [
            KindPickerOption(value: 'Role', label: 'Role'),
            KindPickerOption(value: 'ClusterRole', label: 'ClusterRole'),
          ],
          selected: state.form.roleKind,
          onChanged: (v) => controller.updateForm(
              (f) => f.copyWith(roleKind: v, roleName: '')),
          errorMessage: stepErrors['roleRef.kind'],
        ),
        const SizedBox(height: 12),
        if (!isClusterRole && state.form.namespace.trim().isEmpty)
          Text(
            'Pick a namespace to load roles.',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          )
        else
          NamedResourcePicker(
            clusterId: wizardKey.clusterId,
            kind: roleListKind,
            namespace: roleListNamespace,
            selected: state.form.roleName,
            onChanged: (v) =>
                controller.updateForm((f) => f.copyWith(roleName: v)),
            label: isClusterRole ? 'ClusterRole' : 'Role',
            hint: 'Pick a $roleListKind.${state.form.roleKind} entry',
            errorMessage: stepErrors['roleRef.name'],
          ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Subjects',
          subtitle: 'Users, groups, or service accounts to bind',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<RoleBindingSubject>(
          items: state.form.subjects,
          addLabel: 'Add subject',
          onAdd: () => controller.updateForm((f) => f.copyWith(
              subjects: [...f.subjects, const RoleBindingSubject()])),
          onRemove: (i) {
            final next = [...state.form.subjects]..removeAt(i);
            controller.updateForm((f) => f.copyWith(subjects: next));
          },
          itemBuilder: (ctx, i, s) => _SubjectRow(
            index: i,
            subject: s,
            stepErrors: stepErrors,
            onChanged: (next) {
              final list = [...state.form.subjects];
              list[i] = next;
              controller.updateForm((f) => f.copyWith(subjects: list));
            },
          ),
          errorMessage: stepErrors['subjects'],
        ),
      ],
    );
  }
}

class _SubjectRow extends StatefulWidget {
  const _SubjectRow({
    required this.index,
    required this.subject,
    required this.stepErrors,
    required this.onChanged,
  });

  final int index;
  final RoleBindingSubject subject;
  final Map<String, String> stepErrors;
  final ValueChanged<RoleBindingSubject> onChanged;

  @override
  State<_SubjectRow> createState() => _SubjectRowState();
}

class _SubjectRowState extends State<_SubjectRow> {
  late final TextEditingController _name =
      TextEditingController(text: widget.subject.name);
  late final TextEditingController _ns =
      TextEditingController(text: widget.subject.namespace);

  @override
  void didUpdateWidget(covariant _SubjectRow old) {
    super.didUpdateWidget(old);
    if (_name.text != widget.subject.name) _name.text = widget.subject.name;
    if (_ns.text != widget.subject.namespace) {
      _ns.text = widget.subject.namespace;
    }
  }

  @override
  void dispose() {
    _name.dispose();
    _ns.dispose();
    super.dispose();
  }

  String? _err(String suffix) =>
      widget.stepErrors['subjects[${widget.index}]$suffix'];

  @override
  Widget build(BuildContext context) {
    final isSa = widget.subject.kind == 'ServiceAccount';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        DropdownButtonFormField<String>(
          initialValue: widget.subject.kind,
          isExpanded: true,
          decoration: InputDecoration(
            labelText: 'Kind',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText: _err('.kind'),
          ),
          items: [
            for (final k in kRoleBindingSubjectKinds)
              DropdownMenuItem(value: k, child: Text(k)),
          ],
          onChanged: (v) {
            if (v == null) return;
            widget.onChanged(widget.subject.copyWith(kind: v));
          },
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _name,
          onChanged: (v) =>
              widget.onChanged(widget.subject.copyWith(name: v.trim())),
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: isSa ? 'test-sa' : 'alice@example.com',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText: _err('.name'),
          ),
        ),
        if (isSa) ...[
          const SizedBox(height: 8),
          TextField(
            controller: _ns,
            onChanged: (v) => widget
                .onChanged(widget.subject.copyWith(namespace: v.trim())),
            decoration: InputDecoration(
              labelText: 'ServiceAccount namespace',
              hintText: 'default',
              isDense: true,
              border: const OutlineInputBorder(),
              errorText: _err('.namespace'),
            ),
          ),
        ],
      ],
    );
  }
}
