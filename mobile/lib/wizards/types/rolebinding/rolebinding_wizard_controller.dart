// RoleBinding wizard controller. Mirrors
// `frontend/islands/RoleBindingWizard.tsx` and ports the wire contract
// from `backend/internal/wizard/rolebinding.go:12`.
//
// Wire format (`RoleBindingInput`):
//   {
//     name, namespace?,
//     clusterScope: bool,
//     roleRef:  {kind: "Role"|"ClusterRole", name},
//     subjects: [{kind: "User"|"Group"|"ServiceAccount", name, namespace?}],
//   }
//
// One Configure step + Review. Cluster-scoped variant is reachable as
// a separate registry entry (not in PR-3c — namespaced only). Backend
// rejects ClusterRoleBinding referencing a Role.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kRoleBindingRoleKinds = ['Role', 'ClusterRole'];
const List<String> kRoleBindingSubjectKinds = [
  'User',
  'Group',
  'ServiceAccount',
];

class RoleBindingSubject {
  const RoleBindingSubject({
    this.kind = 'User',
    this.name = '',
    this.namespace = '',
  });

  final String kind;
  final String name;
  final String namespace;

  RoleBindingSubject copyWith({
    String? kind,
    String? name,
    String? namespace,
  }) =>
      RoleBindingSubject(
        kind: kind ?? this.kind,
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
      );

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{
      'kind': kind,
      'name': name,
    };
    // Backend ignores namespace for User/Group; emit only for SA so
    // the YAML preview matches what the cluster will actually store.
    if (kind == 'ServiceAccount') {
      out['namespace'] = namespace;
    } else {
      out['namespace'] = '';
    }
    return out;
  }
}

class RoleBindingForm {
  const RoleBindingForm({
    this.name = '',
    this.namespace = '',
    this.roleKind = 'Role',
    this.roleName = '',
    this.subjects = const <RoleBindingSubject>[RoleBindingSubject()],
  });

  final String name;
  final String namespace;
  final String roleKind;
  final String roleName;
  final List<RoleBindingSubject> subjects;

  RoleBindingForm copyWith({
    String? name,
    String? namespace,
    String? roleKind,
    String? roleName,
    List<RoleBindingSubject>? subjects,
  }) =>
      RoleBindingForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        roleKind: roleKind ?? this.roleKind,
        roleName: roleName ?? this.roleName,
        subjects: subjects ?? this.subjects,
      );
}

class RoleBindingWizardController
    extends WizardController<RoleBindingForm> {
  @override
  String get wizardType => 'rolebinding';

  @override
  String get resourceListKind => 'rolebindings';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Role reference and subjects',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  RoleBindingForm buildInitialForm() => const RoleBindingForm();

  @override
  Map<String, dynamic> toPreviewBody(RoleBindingForm form) {
    return <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'clusterScope': false,
      'roleRef': {
        'kind': form.roleKind,
        'name': form.roleName,
      },
      'subjects': [for (final s in form.subjects) s.toJson()],
    };
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'roleRef.kind' ||
        fieldPath == 'roleRef.name' ||
        fieldPath == 'subjects' ||
        fieldPath.startsWith('subjects[')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(RoleBindingForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.roleName.trim().isEmpty) {
      out['roleRef.name'] = 'Role name is required';
    }
    if (form.subjects.isEmpty) {
      out['subjects'] = 'Add at least one subject';
    } else {
      for (var i = 0; i < form.subjects.length; i++) {
        final s = form.subjects[i];
        if (s.name.trim().isEmpty) {
          out['subjects[$i].name'] = 'Name is required';
        }
        if (s.kind == 'ServiceAccount' && s.namespace.trim().isEmpty) {
          out['subjects[$i].namespace'] =
              'Namespace is required for ServiceAccount subjects';
        }
      }
    }
    return out;
  }
}

final roleBindingWizardProvider = AutoDisposeNotifierProvider.family<
    RoleBindingWizardController,
    WizardState<RoleBindingForm>,
    WizardKey>(RoleBindingWizardController.new);
