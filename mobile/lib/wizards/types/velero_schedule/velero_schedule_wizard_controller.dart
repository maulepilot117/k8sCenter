// Velero Schedule wizard controller. Mirrors
// `frontend/islands/VeleroScheduleWizard.tsx` and
// `backend/internal/wizard/velero.go:187`.
//
// Wire format:
//   {
//     name, namespace?,
//     schedule,                 // cron expression
//     paused?,
//     includedNamespaces?,
//     excludedNamespaces?,
//     storageLocation?,
//     ttl?,
//     snapshotVolumes?,
//   }
//
// The backend flattens the embedded Backup template into spec.template
// at YAML render time. Mobile reuses VeleroBackup's form fields rather
// than embedding a separate `template:` map — this keeps the form
// shape ergonomic and the backend wire shape correct.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/duration_input.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<({String label, String value})> kVeleroCronPresets = [
  (label: 'Every hour', value: '@hourly'),
  (label: 'Every day at 01:00', value: '0 1 * * *'),
  (label: 'Every Sunday at 02:00', value: '0 2 * * 0'),
  (label: 'Every 6 hours', value: '0 */6 * * *'),
];

class VeleroScheduleForm {
  const VeleroScheduleForm({
    this.name = '',
    this.namespace = 'velero',
    this.schedule = '0 1 * * *',
    this.paused = false,
    this.includedNamespaces = const <String>{},
    this.excludedNamespaces = const <String>{},
    this.storageLocation = '',
    this.ttl = '',
    this.snapshotVolumes = true,
  });

  final String name;
  final String namespace;
  final String schedule;
  final bool paused;
  final Set<String> includedNamespaces;
  final Set<String> excludedNamespaces;
  final String storageLocation;
  final String ttl;
  final bool snapshotVolumes;

  VeleroScheduleForm copyWith({
    String? name,
    String? namespace,
    String? schedule,
    bool? paused,
    Set<String>? includedNamespaces,
    Set<String>? excludedNamespaces,
    String? storageLocation,
    String? ttl,
    bool? snapshotVolumes,
  }) =>
      VeleroScheduleForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        schedule: schedule ?? this.schedule,
        paused: paused ?? this.paused,
        includedNamespaces: includedNamespaces ?? this.includedNamespaces,
        excludedNamespaces: excludedNamespaces ?? this.excludedNamespaces,
        storageLocation: storageLocation ?? this.storageLocation,
        ttl: ttl ?? this.ttl,
        snapshotVolumes: snapshotVolumes ?? this.snapshotVolumes,
      );
}

class VeleroScheduleWizardController
    extends WizardController<VeleroScheduleForm> {
  @override
  String get wizardType => 'velero-schedule';

  @override
  String get resourceListKind => 'schedules';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Schedule',
          description: 'Cron and pause toggle',
        ),
        WizardStep(
          title: 'Backup template',
          description: 'Scope, retention, snapshots',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  VeleroScheduleForm buildInitialForm() => const VeleroScheduleForm();

  @override
  Map<String, dynamic> toPreviewBody(VeleroScheduleForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'schedule': form.schedule,
    };
    if (form.paused) body['paused'] = true;
    if (form.includedNamespaces.isNotEmpty) {
      body['includedNamespaces'] = form.includedNamespaces.toList()..sort();
    }
    if (form.excludedNamespaces.isNotEmpty) {
      body['excludedNamespaces'] = form.excludedNamespaces.toList()..sort();
    }
    if (form.storageLocation.trim().isNotEmpty) {
      body['storageLocation'] = form.storageLocation.trim();
    }
    if (form.ttl.trim().isNotEmpty) {
      body['ttl'] = form.ttl.trim();
    }
    body['snapshotVolumes'] = form.snapshotVolumes;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    switch (fieldPath) {
      case 'name':
      case 'namespace':
      case 'schedule':
      case 'paused':
        return 0;
      case 'includedNamespaces':
      case 'excludedNamespaces':
      case 'storageLocation':
      case 'ttl':
      case 'snapshotVolumes':
        return 1;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(VeleroScheduleForm form, int stepIndex) {
    if (stepIndex == 0) {
      final out = <String, String>{
        ...validateNameAndNamespace(form.name, form.namespace),
      };
      if (form.schedule.trim().isEmpty) {
        out['schedule'] = 'Cron schedule is required';
      }
      return out;
    }
    if (stepIndex == 1) {
      final out = <String, String>{};
      final overlap = form.includedNamespaces
          .intersection(form.excludedNamespaces);
      if (overlap.isNotEmpty) {
        out['includedNamespaces'] =
            'Namespace cannot appear in both Included and Excluded: '
            '${overlap.join(", ")}';
      }
      final ttlErr = validateDuration(form.ttl);
      if (ttlErr != null) out['ttl'] = ttlErr;
      return out;
    }
    return const <String, String>{};
  }
}

final veleroScheduleWizardProvider = AutoDisposeNotifierProvider.family<
    VeleroScheduleWizardController,
    WizardState<VeleroScheduleForm>,
    WizardKey>(VeleroScheduleWizardController.new);
