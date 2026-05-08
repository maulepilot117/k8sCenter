// ScheduledSnapshot wizard controller. Mirrors
// `frontend/islands/ScheduledSnapshotWizard.tsx` and
// `backend/internal/wizard/scheduled_snapshot.go:13`.
//
// Wire format:
//   { name, namespace, sourcePVC, volumeSnapshotClassName,
//     schedule, retentionCount }
//
// Three-step layout:
//   0 — Source & Schedule (name + ns + PVC + cron)
//   1 — Class & Retention (snapshot class + retention)
//   2 — Review

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

// ScheduledSnapshot's backend validates schedules with a strict 5-field
// regex (cronRegex in backend/internal/wizard/container.go), which
// rejects @-shorthand. Keep all presets as 5-field expressions so the
// chip the operator clicks always survives backend validation.
// VeleroSchedule's backend uses cron.ParseStandard and accepts
// @-shorthand — its preset list lives separately for that reason.
const List<({String label, String value})> kCronPresets = [
  (label: 'Every hour', value: '0 * * * *'),
  (label: 'Every day at 02:00', value: '0 2 * * *'),
  (label: 'Every Sunday at 03:00', value: '0 3 * * 0'),
  (label: 'Every 6 hours', value: '0 */6 * * *'),
];

class ScheduledSnapshotForm {
  const ScheduledSnapshotForm({
    this.name = '',
    this.namespace = '',
    this.sourcePVC = '',
    this.volumeSnapshotClassName = '',
    this.schedule = '0 2 * * *',
    this.retentionCount = 7,
  });

  final String name;
  final String namespace;
  final String sourcePVC;
  final String volumeSnapshotClassName;
  final String schedule;
  final int retentionCount;

  ScheduledSnapshotForm copyWith({
    String? name,
    String? namespace,
    String? sourcePVC,
    String? volumeSnapshotClassName,
    String? schedule,
    int? retentionCount,
  }) =>
      ScheduledSnapshotForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        sourcePVC: sourcePVC ?? this.sourcePVC,
        volumeSnapshotClassName:
            volumeSnapshotClassName ?? this.volumeSnapshotClassName,
        schedule: schedule ?? this.schedule,
        retentionCount: retentionCount ?? this.retentionCount,
      );
}

class ScheduledSnapshotWizardController
    extends WizardController<ScheduledSnapshotForm> {
  @override
  String get wizardType => 'scheduled-snapshot';

  /// CronJob is what the multi-doc YAML lands. Invalidate the
  /// cronjobs list cache on success so the operator sees the new
  /// entry immediately. The companion ServiceAccount/Role/RoleBinding
  /// don't have user-facing list screens worth eager-refreshing.
  @override
  String get resourceListKind => 'cronjobs';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Source & Schedule',
          description: 'PVC and cron schedule',
        ),
        WizardStep(
          title: 'Class & Retention',
          description: 'Snapshot class and retention count',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  ScheduledSnapshotForm buildInitialForm() => const ScheduledSnapshotForm();

  @override
  Map<String, dynamic> toPreviewBody(ScheduledSnapshotForm form) {
    return {
      'name': form.name,
      'namespace': form.namespace,
      'sourcePVC': form.sourcePVC,
      'volumeSnapshotClassName': form.volumeSnapshotClassName,
      'schedule': form.schedule,
      'retentionCount': form.retentionCount,
    };
  }

  @override
  int? errorRouter(String fieldPath) {
    switch (fieldPath) {
      case 'name':
      case 'namespace':
      case 'sourcePVC':
      case 'schedule':
        return 0;
      case 'volumeSnapshotClassName':
      case 'retentionCount':
        return 1;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(
      ScheduledSnapshotForm form, int stepIndex) {
    if (stepIndex == 0) {
      final out = <String, String>{
        ...validateNameAndNamespace(form.name, form.namespace),
      };
      if (form.sourcePVC.trim().isEmpty) {
        out['sourcePVC'] = 'Source PVC is required';
      }
      if (form.schedule.trim().isEmpty) {
        out['schedule'] = 'Cron schedule is required';
      }
      return out;
    }
    if (stepIndex == 1) {
      final out = <String, String>{};
      if (form.volumeSnapshotClassName.trim().isEmpty) {
        out['volumeSnapshotClassName'] = 'Snapshot class is required';
      }
      if (form.retentionCount < 1 || form.retentionCount > 100) {
        out['retentionCount'] = 'Retention must be between 1 and 100';
      }
      return out;
    }
    return const <String, String>{};
  }
}

final scheduledSnapshotWizardProvider = AutoDisposeNotifierProvider.family<
    ScheduledSnapshotWizardController,
    WizardState<ScheduledSnapshotForm>,
    WizardKey>(ScheduledSnapshotWizardController.new);
