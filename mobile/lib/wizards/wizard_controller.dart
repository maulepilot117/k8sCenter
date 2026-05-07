// Generic wizard controller. Per-wizard concrete subclasses extend this
// with a typed form record, override [buildInitialForm] / [toPreviewBody]
// / [errorRouter], and the rest of the state machine is shared.
//
// State transitions:
//   formEditing  ── next from non-Review step ──▶ formEditing (next step)
//   formEditing  ── next from last form step ──▶ previewing
//   previewing   ── 200 ──▶ reviewing
//   previewing   ── 422 with field errors ──▶ formEditing (rewound to
//                                             the lowest-index step that
//                                             owns any error)
//   previewing   ── other failure ──▶ failed (operator can re-tap Next)
//   reviewing    ── apply tap ──▶ applying
//   applying     ── 200 ──▶ applied
//   applying     ── failure ──▶ failed (state preserved; operator can
//                                       Back, edit, re-preview, retry)
//   any          ── back ──▶ formEditing (one step back)
//   any          ── reset ──▶ formEditing on step 0 (rare; used by the
//                             cluster-mismatch discard path)
//
// Cluster pinning: the controller captures the active cluster id at
// construction time and re-checks it at preview/apply. A mid-flow
// cluster switch surfaces a clear failure rather than firing the write
// at the wrong cluster — the same defense PR-2a / PR-2b added to
// resource_actions and yaml_apply_controller.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../cluster/cluster_provider.dart';
import 'wizard_preview_client.dart';
import 'wizard_step.dart';

enum WizardStatus {
  formEditing,
  previewing,
  reviewing,
  applying,
  applied,
  failed,
}

/// Family key — clusterId pins the controller; draftId scopes a draft
/// instance so two wizards open simultaneously (tablet master/detail
/// future case) get isolated state. Defaults to a singleton 'default'
/// draft when callers don't care.
class WizardKey {
  const WizardKey({required this.clusterId, this.draftId = 'default'});

  final String clusterId;
  final String draftId;

  @override
  bool operator ==(Object other) =>
      other is WizardKey &&
      other.clusterId == clusterId &&
      other.draftId == draftId;

  @override
  int get hashCode => Object.hash(clusterId, draftId);
}

/// Apply result envelope returned by /api/v1/yaml/apply. Slim local
/// shape — the wizard's review surface only needs the summary; full
/// per-row results render via the YamlApplyController for direct YAML
/// edits and aren't needed here.
class WizardApplyOutcome {
  const WizardApplyOutcome({
    required this.created,
    required this.configured,
    required this.unchanged,
    required this.failed,
    required this.firstResultName,
    required this.firstResultKind,
    required this.firstResultNamespace,
  });

  final int created;
  final int configured;
  final int unchanged;
  final int failed;
  final String firstResultName;
  final String firstResultKind;
  final String? firstResultNamespace;

  bool get allSucceeded => failed == 0;
}

class WizardState<TForm> {
  const WizardState({
    required this.status,
    required this.currentStep,
    required this.form,
    required this.stepErrors,
    this.previewYaml,
    this.applyOutcome,
    this.errorMessage,
  });

  final WizardStatus status;

  /// Zero-based index. Last index is the Review step.
  final int currentStep;

  final TForm form;

  /// Errors keyed by step index — each entry is a `field path → message`
  /// map the step widget consumes to render inline messages.
  final Map<int, StepFieldErrors> stepErrors;

  final String? previewYaml;
  final WizardApplyOutcome? applyOutcome;
  final String? errorMessage;

  WizardState<TForm> copyWith({
    WizardStatus? status,
    int? currentStep,
    TForm? form,
    Map<int, StepFieldErrors>? stepErrors,
    String? previewYaml,
    WizardApplyOutcome? applyOutcome,
    String? errorMessage,
    bool clearPreviewYaml = false,
    bool clearApplyOutcome = false,
    bool clearErrorMessage = false,
  }) {
    return WizardState<TForm>(
      status: status ?? this.status,
      currentStep: currentStep ?? this.currentStep,
      form: form ?? this.form,
      stepErrors: stepErrors ?? this.stepErrors,
      previewYaml:
          clearPreviewYaml ? null : (previewYaml ?? this.previewYaml),
      applyOutcome:
          clearApplyOutcome ? null : (applyOutcome ?? this.applyOutcome),
      errorMessage:
          clearErrorMessage ? null : (errorMessage ?? this.errorMessage),
    );
  }
}

/// Generic wizard controller. Each wizard subclass:
///   1. Picks its own typed `TForm` record
///   2. Overrides [buildInitialForm], [toPreviewBody], [steps],
///      [wizardType], and [errorRouter]
///   3. Optionally overrides [validateLocally] for quick required-field
///      gates that don't need a server round-trip
abstract class WizardController<TForm>
    extends AutoDisposeFamilyNotifier<WizardState<TForm>, WizardKey> {
  /// Backend wizard type — e.g., `configmap`, `secret`, `service`.
  String get wizardType;

  /// Step list rendered by the stepper. Last entry is Review.
  List<WizardStep> get steps;

  /// Build the empty/default form when the wizard opens.
  TForm buildInitialForm();

  /// Serialize the current form into the JSON body posted to
  /// /api/v1/wizards/:type/preview.
  Map<String, dynamic> toPreviewBody(TForm form);

  /// Map a server field path to the step index that owns it. Unmapped
  /// fields default to step 0 (the first form step) so the operator at
  /// least sees something rather than a silent error.
  int errorRouter(String fieldPath);

  /// Optional client-side gate — quick required-field checks that don't
  /// need the server round-trip. Returns the error map keyed by field
  /// path. Empty map means OK to advance.
  StepFieldErrors validateLocally(TForm form, int stepIndex) =>
      const <String, String>{};

  @override
  WizardState<TForm> build(WizardKey arg) {
    return WizardState<TForm>(
      status: WizardStatus.formEditing,
      currentStep: 0,
      form: buildInitialForm(),
      stepErrors: const <int, StepFieldErrors>{},
    );
  }

  /// Replace the form (e.g., after a step widget edits a field). Clears
  /// errors for the current step so corrected fields don't keep showing
  /// their old messages.
  void updateForm(TForm Function(TForm) update) {
    final next = update(state.form);
    final errors = Map<int, StepFieldErrors>.from(state.stepErrors)
      ..remove(state.currentStep);
    state = state.copyWith(
      form: next,
      stepErrors: errors,
      clearErrorMessage: true,
    );
  }

  /// Jump to an arbitrary completed step. Stepper widget calls this when
  /// the operator taps a prior step's chip. Future steps are
  /// tap-disabled at the widget level so this guard is defensive only.
  void goToStep(int step) {
    if (step < 0 || step >= steps.length) return;
    if (step > state.currentStep) return;
    state = state.copyWith(currentStep: step, clearErrorMessage: true);
  }

  /// Advance one step. From the last form step, transitions into
  /// preview. Local validation runs first and aborts the advance if it
  /// surfaces errors.
  Future<void> next() async {
    final isLastFormStep = state.currentStep == steps.length - 2;
    final localErrors = validateLocally(state.form, state.currentStep);
    if (localErrors.isNotEmpty) {
      final errors = Map<int, StepFieldErrors>.from(state.stepErrors);
      errors[state.currentStep] = localErrors;
      state = state.copyWith(stepErrors: errors);
      return;
    }
    if (isLastFormStep) {
      await _runPreview();
      return;
    }
    state = state.copyWith(
      currentStep: state.currentStep + 1,
      clearErrorMessage: true,
    );
  }

  /// Step backwards. From Review back into the last form step. Clears
  /// preview YAML so a stale preview doesn't render against an edited
  /// form.
  void back() {
    if (state.currentStep == 0) return;
    state = state.copyWith(
      currentStep: state.currentStep - 1,
      status: WizardStatus.formEditing,
      clearPreviewYaml: true,
      clearErrorMessage: true,
    );
  }

  /// Trigger the preview round-trip. Surfaced for the rare cases the
  /// operator wants to retry from the Review step (e.g., transient 5xx).
  Future<void> retryPreview() => _runPreview();

  Future<void> _runPreview() async {
    if (!_clusterStillPinned()) return;
    state = state.copyWith(
      status: WizardStatus.previewing,
      clearPreviewYaml: true,
      clearErrorMessage: true,
    );
    try {
      final client = WizardPreviewClient(ref.read(dioProvider));
      final result =
          await client.preview(wizardType, toPreviewBody(state.form));
      switch (result) {
        case PreviewYaml(:final yaml):
          state = state.copyWith(
            status: WizardStatus.reviewing,
            currentStep: steps.length - 1,
            previewYaml: yaml,
            stepErrors: const <int, StepFieldErrors>{},
          );
        case PreviewErrors(:final errors):
          _routeFieldErrors(errors);
      }
    } on ApiError catch (e) {
      state = state.copyWith(
        status: WizardStatus.failed,
        errorMessage: e.message,
      );
    } catch (_) {
      state = state.copyWith(
        status: WizardStatus.failed,
        errorMessage: 'Preview failed unexpectedly.',
      );
    }
  }

  /// Apply the previewed YAML via /api/v1/yaml/apply. On success
  /// transitions to applied + populates [WizardApplyOutcome] so the
  /// review screen can navigate to the created resource. On failure
  /// preserves preview YAML so the operator can Back, fix, retry.
  Future<void> apply() async {
    final yaml = state.previewYaml;
    if (yaml == null || yaml.isEmpty) return;
    if (state.status == WizardStatus.applying) return;
    if (!_clusterStillPinned()) return;
    state = state.copyWith(
      status: WizardStatus.applying,
      clearApplyOutcome: true,
      clearErrorMessage: true,
    );
    try {
      final outcome = await _postYamlApply(yaml);
      state = state.copyWith(
        status: WizardStatus.applied,
        applyOutcome: outcome,
      );
    } on ApiError catch (e) {
      state = state.copyWith(
        status: WizardStatus.failed,
        errorMessage: e.message,
      );
    } catch (_) {
      state = state.copyWith(
        status: WizardStatus.failed,
        errorMessage: 'Apply failed unexpectedly.',
      );
    }
  }

  /// Reset to a fresh form on step 0. Used by the cluster-mismatch
  /// discard path.
  void discardAndReset() {
    state = WizardState<TForm>(
      status: WizardStatus.formEditing,
      currentStep: 0,
      form: buildInitialForm(),
      stepErrors: const <int, StepFieldErrors>{},
    );
  }

  bool _clusterStillPinned() {
    final active = ref.read(activeClusterProvider);
    if (active == arg.clusterId) return true;
    state = state.copyWith(
      status: WizardStatus.failed,
      errorMessage:
          'Cluster changed during this wizard. Aborted to avoid mutating '
          'the wrong cluster. Discard or re-open from the new cluster.',
    );
    return false;
  }

  void _routeFieldErrors(List<WizardFieldError> errors) {
    final byStep = <int, StepFieldErrors>{};
    var lowestStep = steps.length - 2; // last form step is the fallback
    for (final err in errors) {
      final step = errorRouter(err.field);
      final clamped = step.clamp(0, steps.length - 2);
      if (clamped < lowestStep) lowestStep = clamped;
      final existing = byStep.putIfAbsent(clamped, () => <String, String>{});
      existing[err.field] = err.message;
    }
    state = state.copyWith(
      status: WizardStatus.formEditing,
      currentStep: lowestStep,
      stepErrors: byStep,
      clearPreviewYaml: true,
    );
  }

  Future<WizardApplyOutcome> _postYamlApply(String yaml) async {
    final dio = ref.read(dioProvider);
    try {
      final res = await dio.post<Map<String, dynamic>>(
        '/api/v1/yaml/apply',
        data: yaml,
        options: Options(
          contentType: 'application/yaml',
          headers: {'Accept': 'application/json'},
        ),
      );
      final outer = res.data ?? const <String, dynamic>{};
      final inner = (outer['data'] as Map<String, dynamic>?) ??
          const <String, dynamic>{};
      final summary =
          (inner['summary'] as Map<String, dynamic>?) ?? const {};
      final results = (inner['results'] as List?) ?? const <dynamic>[];
      final firstResult = results.isNotEmpty && results.first is Map
          ? Map<String, dynamic>.from(results.first as Map)
          : const <String, dynamic>{};
      return WizardApplyOutcome(
        created: (summary['created'] as num?)?.toInt() ?? 0,
        configured: (summary['configured'] as num?)?.toInt() ?? 0,
        unchanged: (summary['unchanged'] as num?)?.toInt() ?? 0,
        failed: (summary['failed'] as num?)?.toInt() ?? 0,
        firstResultName: firstResult['name'] as String? ?? '',
        firstResultKind: firstResult['kind'] as String? ?? '',
        firstResultNamespace: firstResult['namespace'] as String?,
      );
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}
