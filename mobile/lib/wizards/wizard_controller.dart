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
//   applying     ── 200 + summary.failed == 0 ──▶ applied
//   applying     ── 200 + summary.failed > 0 ──▶ failed (preview YAML
//                                             preserved so operator can
//                                             Back, edit, re-apply)
//   applying     ── failure ──▶ failed (state preserved; operator can
//                                       Back, edit, re-preview, retry)
//   any          ── back ──▶ formEditing (one step back)
//   any          ── reset ──▶ formEditing on step 0 (rare; used by the
//                             cluster-mismatch discard path)
//
// Race / lifecycle hardening (post-PR-3a review):
//   * `_dispatchId` is bumped by every action that should invalidate an
//     in-flight preview (back, updateForm, discardAndReset). Each
//     `_runPreview` and `apply` invocation captures the id at start and
//     drops the result on mismatch. Prevents the late-200 from a back-
//     out preview re-routing the operator forward.
//   * `_disposed` flag set by `ref.onDispose`. Every post-await state
//     setter checks it. Prevents StateError when the autoDispose family
//     entry is torn down while a Future is still in flight (e.g.,
//     operator pops the wizard mid-apply). The HTTP write itself
//     completes server-side; k8s SSA is idempotent so retry doesn't
//     double-create.
//   * Cluster pinning is re-checked at *result arrival* of `_runPreview`
//     and `apply`, not just at request initiation. Catches the case
//     where the operator switches clusters between request issue and
//     HTTP completion (the request was sent with the new cluster's
//     X-Cluster-ID, but the wizard is pinned to the original).
//   * `_clusterMismatch` separates this failure from generic apply
//     failures so the footer can offer "Discard & restart" instead of
//     a Retry that immediately re-fails.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../api/resource_repository.dart';
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
    this.clusterMismatch = false,
    this.unrouted = const <String, String>{},
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

  /// True when the failure was specifically a cluster-pin mismatch.
  /// Drives the failed-state footer to offer "Discard & restart"
  /// instead of a Retry that would immediately re-fail.
  final bool clusterMismatch;

  /// Errors whose `field` path didn't match any per-wizard
  /// `errorRouter` mapping. Surfaced as a top-of-step banner alongside
  /// the routed inline errors so the operator at least sees the raw
  /// message instead of a silent step-0 merge.
  final StepFieldErrors unrouted;

  WizardState<TForm> copyWith({
    WizardStatus? status,
    int? currentStep,
    TForm? form,
    Map<int, StepFieldErrors>? stepErrors,
    String? previewYaml,
    WizardApplyOutcome? applyOutcome,
    String? errorMessage,
    bool? clusterMismatch,
    StepFieldErrors? unrouted,
    bool clearPreviewYaml = false,
    bool clearApplyOutcome = false,
    bool clearErrorMessage = false,
    bool clearUnrouted = false,
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
      clusterMismatch: clusterMismatch ?? this.clusterMismatch,
      unrouted:
          clearUnrouted ? const <String, String>{} : (unrouted ?? this.unrouted),
    );
  }
}

/// Generic wizard controller. Each wizard subclass:
///   1. Picks its own typed `TForm` record
///   2. Overrides [buildInitialForm], [toPreviewBody], [steps],
///      [wizardType], [resourceListKind], and [errorRouter]
///   3. Optionally overrides [validateLocally] for quick required-field
///      gates that don't need a server round-trip
abstract class WizardController<TForm>
    extends AutoDisposeFamilyNotifier<WizardState<TForm>, WizardKey> {
  /// Dispatch id bumped by every action that should invalidate an
  /// in-flight preview/apply (back, updateForm, discardAndReset). Each
  /// async path captures the id at start and drops the result if it
  /// has moved.
  int _dispatchId = 0;

  /// Set by [ref.onDispose] when the autoDispose family entry is torn
  /// down. Every post-await state setter checks this so writing to a
  /// disposed notifier never throws StateError.
  bool _disposed = false;

  /// Backend wizard type — e.g., `configmap`, `secret`, `service`.
  String get wizardType;

  /// Step list rendered by the stepper. Last entry is Review.
  List<WizardStep> get steps;

  /// Lowercase plural kind used to invalidate the resource list
  /// provider after a successful apply (e.g., `configmaps`, `secrets`,
  /// `services`). Mirrors the backend's `kind` URL param shape.
  String get resourceListKind;

  /// Additional kinds to invalidate on a successful apply, beyond
  /// [resourceListKind]. Multi-resource wizards (NamespaceLimits emits
  /// a ResourceQuota *and* a LimitRange) override this so both list
  /// caches refresh — otherwise the operator sees a stale
  /// counterpart-list immediately after creation. Default empty.
  List<String> get extraResourceListKinds => const <String>[];

  /// Build the empty/default form when the wizard opens.
  TForm buildInitialForm();

  /// Serialize the current form into the JSON body posted to
  /// /api/v1/wizards/:type/preview.
  Map<String, dynamic> toPreviewBody(TForm form);

  /// Map a server field path to the step index that owns it. Return
  /// `null` for unmapped paths — the controller then surfaces them via
  /// [WizardState.unrouted] instead of silently merging into step 0.
  int? errorRouter(String fieldPath);

  /// Optional client-side gate — quick required-field checks that don't
  /// need the server round-trip. Returns the error map keyed by field
  /// path. Empty map means OK to advance.
  StepFieldErrors validateLocally(TForm form, int stepIndex) =>
      const <String, String>{};

  /// Helper for the universal "Name + Namespace required" pair every
  /// namespaced wizard surfaces in its Configure step. Subclasses call
  /// it from their own [validateLocally] and merge the result into
  /// their step-0 error map. Pass [requireNamespace] = false for
  /// cluster-scoped wizards so only the name is checked. Centralizing
  /// the literal strings here keeps the operator-facing copy aligned
  /// across all 14+ namespaced wizards.
  StepFieldErrors validateNameAndNamespace(
    String name,
    String namespace, {
    bool requireNamespace = true,
  }) {
    final out = <String, String>{};
    if (name.trim().isEmpty) out['name'] = 'Name is required';
    if (requireNamespace && namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    return out;
  }

  @override
  WizardState<TForm> build(WizardKey arg) {
    ref.onDispose(() {
      _disposed = true;
    });
    return WizardState<TForm>(
      status: WizardStatus.formEditing,
      currentStep: 0,
      form: buildInitialForm(),
      stepErrors: const <int, StepFieldErrors>{},
    );
  }

  /// Replace the form (e.g., after a step widget edits a field). Clears
  /// errors for the current step so corrected fields don't keep showing
  /// their old messages. Bumps [_dispatchId] so an in-flight preview's
  /// late 200 doesn't re-route the operator forward against the
  /// pre-edit form. If the wizard was in [WizardStatus.previewing] or
  /// [WizardStatus.reviewing] when the edit landed, reset to
  /// [WizardStatus.formEditing] — the operator clearly isn't reviewing
  /// anymore, and the previously-previewed YAML is now stale.
  void updateForm(TForm Function(TForm) update) {
    final next = update(state.form);
    final errors = Map<int, StepFieldErrors>.from(state.stepErrors)
      ..remove(state.currentStep);
    _dispatchId++;
    final resetStatus = (state.status == WizardStatus.previewing ||
            state.status == WizardStatus.reviewing)
        ? WizardStatus.formEditing
        : state.status;
    _safeSet(state.copyWith(
      status: resetStatus,
      form: next,
      stepErrors: errors,
      clearPreviewYaml: true,
      clearApplyOutcome: true,
      clearErrorMessage: true,
      clearUnrouted: true,
    ));
  }

  /// Jump to an arbitrary completed step. Stepper widget calls this when
  /// the operator taps a prior step's chip. Future steps are
  /// tap-disabled at the widget level so this guard is defensive only.
  ///
  /// When the jump originates from a non-form-editing status (e.g. the
  /// operator taps a completed-step chip while at Review, status
  /// `reviewing`/`previewing`/`failed`), this mirrors [back]'s full
  /// reset: it bumps [_dispatchId] and clears the preview YAML, apply
  /// outcome, and cluster-mismatch flag. Without this, jumping out of
  /// Review left `status=reviewing` and a populated `previewYaml`, so the
  /// footer kept offering Apply and [apply] would commit the stale,
  /// un-previewed YAML against an edited form.
  void goToStep(int step) {
    if (step < 0 || step >= steps.length) return;
    if (step > state.currentStep) return;
    if (state.status != WizardStatus.formEditing) {
      _dispatchId++;
      _safeSet(state.copyWith(
        currentStep: step,
        status: WizardStatus.formEditing,
        clearPreviewYaml: true,
        clearApplyOutcome: true,
        clearErrorMessage: true,
        clearUnrouted: true,
        clusterMismatch: false,
      ));
      return;
    }
    _safeSet(state.copyWith(currentStep: step, clearErrorMessage: true));
  }

  /// Advance one step. From the last form step, transitions into
  /// preview. Local validation runs first and aborts the advance if it
  /// surfaces errors. Guarded against rapid double-tap during preview.
  Future<void> next() async {
    if (state.status == WizardStatus.previewing ||
        state.status == WizardStatus.applying) {
      return;
    }
    final isLastFormStep = state.currentStep == steps.length - 2;
    final localErrors = validateLocally(state.form, state.currentStep);
    if (localErrors.isNotEmpty) {
      final errors = Map<int, StepFieldErrors>.from(state.stepErrors);
      errors[state.currentStep] = localErrors;
      _safeSet(state.copyWith(stepErrors: errors));
      return;
    }
    if (isLastFormStep) {
      await _runPreview();
      return;
    }
    _safeSet(state.copyWith(
      currentStep: state.currentStep + 1,
      clearErrorMessage: true,
    ));
  }

  /// Step backwards. From Review back into the last form step. Clears
  /// preview YAML so a stale preview doesn't render against an edited
  /// form. Bumps [_dispatchId] so an in-flight preview's late 200
  /// doesn't re-route the operator forward.
  void back() {
    if (state.currentStep == 0) return;
    _dispatchId++;
    _safeSet(state.copyWith(
      currentStep: state.currentStep - 1,
      status: WizardStatus.formEditing,
      clearPreviewYaml: true,
      clearApplyOutcome: true,
      clearErrorMessage: true,
      clearUnrouted: true,
      clusterMismatch: false,
    ));
  }

  /// Trigger the preview round-trip. Surfaced for the rare cases the
  /// operator wants to retry from the Review step (e.g., transient 5xx).
  Future<void> retryPreview() => _runPreview();

  Future<void> _runPreview() async {
    if (!_clusterStillPinned()) return;
    final dispatchId = ++_dispatchId;
    _safeSet(state.copyWith(
      status: WizardStatus.previewing,
      clearPreviewYaml: true,
      clearErrorMessage: true,
      clearUnrouted: true,
    ));
    try {
      final client = WizardPreviewClient(ref.read(dioProvider));
      final result = await client.preview(
        wizardType,
        toPreviewBody(state.form),
        clusterId: arg.clusterId,
      );
      // Late-arrival guards: drop if disposed, dispatch superseded, or
      // cluster changed between request issue and HTTP completion. The
      // request itself was pinned via X-Cluster-ID header to the
      // wizard's cluster; the post-emission pin check exists to keep
      // wizard state aligned with the operator's current cluster
      // context, not to defend against wrong-cluster mutation.
      if (_disposed || dispatchId != _dispatchId) return;
      if (!_clusterStillPinned(phase: _PinPhase.postEmission)) return;
      switch (result) {
        case PreviewYaml(:final yaml):
          _safeSet(state.copyWith(
            status: WizardStatus.reviewing,
            currentStep: steps.length - 1,
            previewYaml: yaml,
            stepErrors: const <int, StepFieldErrors>{},
            clearUnrouted: true,
          ));
        case PreviewErrors(:final errors):
          _routeFieldErrors(errors);
      }
    } on ApiError catch (e) {
      if (_disposed || dispatchId != _dispatchId) return;
      _safeSet(state.copyWith(
        status: WizardStatus.failed,
        errorMessage: e.message,
      ));
    } catch (e, st) {
      // Bind exception type+stack so future Crashlytics integration has
      // signal. For now we surface the type so an operator filing a bug
      // can describe what went wrong instead of "unexpected".
      if (_disposed || dispatchId != _dispatchId) return;
      _safeSet(state.copyWith(
        status: WizardStatus.failed,
        errorMessage:
            'Preview failed unexpectedly (${e.runtimeType}). Please retry. '
            'If this persists, check the backend logs.',
      ));
      assert(() {
        // ignore: avoid_print
        print('WizardController._runPreview unhandled error: $e\n$st');
        return true;
      }());
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
    final dispatchId = ++_dispatchId;
    _safeSet(state.copyWith(
      status: WizardStatus.applying,
      clearApplyOutcome: true,
      clearErrorMessage: true,
    ));
    try {
      final outcome = await _postYamlApply(yaml);
      if (_disposed || dispatchId != _dispatchId) return;
      // Post-emission: the apply request was pinned via X-Cluster-ID
      // header, so any cluster-side mutation already landed on the
      // pinned cluster. The mismatch message reflects that.
      if (!_clusterStillPinned(phase: _PinPhase.postEmission)) return;
      if (!outcome.allSucceeded) {
        // Multi-resource wizards (PR-3b+ NamespaceLimits, etc.) can
        // produce a 200 with `summary.failed > 0`. Treat as failed so
        // the operator doesn't see "Created!" when one of two docs
        // failed. Preview YAML is preserved.
        _safeSet(state.copyWith(
          status: WizardStatus.failed,
          applyOutcome: outcome,
          errorMessage:
              'Apply partially failed: ${outcome.failed} of '
              '${outcome.created + outcome.configured + outcome.unchanged + outcome.failed} '
              'document(s) did not apply. Review the YAML and retry.',
        ));
        return;
      }
      _safeSet(state.copyWith(
        status: WizardStatus.applied,
        applyOutcome: outcome,
      ));
      // Refetch the resource list so the next list-screen visit shows
      // the new entry. ResourceListKey's namespace is nullable; pass
      // null when the apply outcome had no namespace (cluster-scoped
      // resource) so we hit the correct cache slot.
      ref.invalidate(resourceListProvider(ResourceListKey(
        clusterId: arg.clusterId,
        kind: resourceListKind,
        namespace: outcome.firstResultNamespace,
      )));
      // Multi-resource wizards (e.g. NamespaceLimits = ResourceQuota +
      // LimitRange) need their counterpart kinds refreshed too.
      for (final extra in extraResourceListKinds) {
        ref.invalidate(resourceListProvider(ResourceListKey(
          clusterId: arg.clusterId,
          kind: extra,
          namespace: outcome.firstResultNamespace,
        )));
      }
    } on ApiError catch (e) {
      if (_disposed || dispatchId != _dispatchId) return;
      _safeSet(state.copyWith(
        status: WizardStatus.failed,
        errorMessage: e.message,
      ));
    } catch (e, st) {
      if (_disposed || dispatchId != _dispatchId) return;
      _safeSet(state.copyWith(
        status: WizardStatus.failed,
        errorMessage:
            'Apply failed unexpectedly (${e.runtimeType}). The resource '
            'may or may not exist on the cluster — check the resource '
            'list to confirm before retrying.',
      ));
      assert(() {
        // ignore: avoid_print
        print('WizardController.apply unhandled error: $e\n$st');
        return true;
      }());
    }
  }

  /// Reset to a fresh form on step 0. Used by the cluster-mismatch
  /// discard path. Re-pins implicitly via the family key (the parent
  /// screen captures the cluster on first build; if the operator
  /// reopens the wizard from a different cluster, a new
  /// FamilyNotifier instance materializes with the new key).
  void discardAndReset() {
    _dispatchId++;
    _safeSet(WizardState<TForm>(
      status: WizardStatus.formEditing,
      currentStep: 0,
      form: buildInitialForm(),
      stepErrors: const <int, StepFieldErrors>{},
    ));
  }

  /// Phase used to tailor the cluster-mismatch message. Pre-emission
  /// mismatches abort cleanly; post-emission mismatches happen *after*
  /// a request has already gone out with the pinned cluster's
  /// `X-Cluster-ID` header — the resource on the pinned cluster may
  /// already exist, so the copy must not say "aborted to avoid
  /// mutating the wrong cluster" (which is false at that point).
  bool _clusterStillPinned({_PinPhase phase = _PinPhase.preEmission}) {
    final active = ref.read(activeClusterProvider);
    if (active == arg.clusterId) return true;
    final message = switch (phase) {
      _PinPhase.preEmission =>
        'Cluster changed during this wizard. Aborted to avoid mutating '
            'the wrong cluster. Discard and re-open from the new cluster.',
      _PinPhase.postEmission =>
        'Cluster changed mid-request. The resource WAS applied to the '
            'pinned cluster (${arg.clusterId}). Switch back to that '
            'cluster to view it; do NOT re-create it on the new cluster '
            'or you will end up with duplicates.',
    };
    _safeSet(state.copyWith(
      status: WizardStatus.failed,
      clusterMismatch: true,
      clearPreviewYaml: true,
      errorMessage: message,
    ));
    return false;
  }

  void _routeFieldErrors(List<WizardFieldError> errors) {
    final byStep = <int, StepFieldErrors>{};
    final unrouted = <String, String>{};
    var lowestStep = (steps.length - 2).clamp(0, steps.length - 1);
    for (final err in errors) {
      final routed = errorRouter(err.field);
      if (routed == null) {
        // Unknown path — surface as a top-of-step banner instead of
        // silently merging into step 0. Operator at least sees the raw
        // message and can correlate to the form field by field name.
        unrouted[err.field] = err.message;
        continue;
      }
      final clamped = routed.clamp(0, (steps.length - 2).clamp(0, steps.length - 1));
      if (clamped < lowestStep) lowestStep = clamped;
      final existing =
          byStep.putIfAbsent(clamped, () => <String, String>{});
      existing[err.field] = err.message;
    }
    _safeSet(state.copyWith(
      status: WizardStatus.formEditing,
      currentStep: lowestStep,
      stepErrors: byStep,
      unrouted: unrouted,
      clearPreviewYaml: true,
    ));
  }

  /// Guarded state setter — no-op when the autoDispose family entry has
  /// been torn down. Without this, a Future resolving on a disposed
  /// notifier throws StateError, surfacing as an unhandled async
  /// exception.
  void _safeSet(WizardState<TForm> next) {
    if (_disposed) return;
    state = next;
  }

  Future<WizardApplyOutcome> _postYamlApply(String yaml) async {
    final dio = ref.read(dioProvider);
    try {
      final res = await dio.post<Map<String, dynamic>>(
        '/api/v1/yaml/apply',
        data: yaml,
        options: Options(
          contentType: 'application/yaml',
          headers: {
            'Accept': 'application/json',
            // Pin X-Cluster-ID to the wizard's locked cluster so a
            // mid-flight switch of activeClusterProvider cannot let
            // ClusterInterceptor rewrite the header. The interceptor
            // only injects when the header is absent. PR-3c applied
            // this defense to the picker path; the wizard
            // preview/apply path was missed at the time.
            'X-Cluster-ID': arg.clusterId,
          },
          // Wizards may produce multi-MB YAML (large ConfigMaps in
          // PR-3b+ etc.); the dioProvider's default 30s sendTimeout
          // can be tight on slow mobile networks. Mirrors M2's YAML
          // editor's choice.
          sendTimeout: const Duration(seconds: 60),
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

enum _PinPhase { preEmission, postEmission }
