// YAML validate/apply state machine. Riverpod port of
// `frontend/lib/yaml-apply.ts:useYamlApply`.
//
// State transitions:
//   idle ──validate──▶ validating ──ok──▶ validated  (showing dry-run results)
//                                  └err──▶ failed
//   idle ──apply───▶ applying    ──ok──▶ applied    (showing real results)
//                                  └err──▶ failed
//   any  ──reset───▶ idle
//
// Family key: (clusterId, kind, namespace, name) so two simultaneous
// edits on different resources don't share state. Apply success
// invalidates the resource's GET so Overview/YAML refetch.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';
import 'resource_repository.dart';

/// Per-row entry in the apply response.
class ApplyResult {
  const ApplyResult({
    required this.index,
    required this.kind,
    required this.name,
    this.namespace,
    required this.action,
    this.error,
  });

  factory ApplyResult.fromJson(Map<String, dynamic> json) {
    return ApplyResult(
      index: (json['index'] as num?)?.toInt() ?? 0,
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String?,
      action: json['action'] as String? ?? 'unknown',
      error: json['error'] as String?,
    );
  }

  /// Adapts `/yaml/validate`'s `docResult` shape into the same model the
  /// apply path uses, so the editor's result panel can render both.
  /// Validate's `errors: [{field, message}]` collapse into a single
  /// joined `error` string for display.
  factory ApplyResult.fromValidateDoc(Map<String, dynamic> json) {
    final errors = (json['errors'] as List?) ?? const [];
    final messages = errors
        .whereType<Map<dynamic, dynamic>>()
        .map((e) => e['message'] as String? ?? '')
        .where((m) => m.isNotEmpty)
        .toList();
    final valid = json['valid'] as bool? ?? false;
    return ApplyResult(
      index: (json['index'] as num?)?.toInt() ?? 0,
      kind: json['kind'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String?,
      action: valid ? 'valid' : 'invalid',
      error: messages.isEmpty ? null : messages.join('; '),
    );
  }

  final int index;
  final String kind;
  final String name;
  final String? namespace;

  /// One of "created" | "configured" | "unchanged" | "failed" (apply) or
  /// "valid" | "invalid" (validate dry-run, adapted).
  final String action;
  final String? error;
}

class ApplySummary {
  const ApplySummary({
    required this.total,
    required this.created,
    required this.configured,
    required this.unchanged,
    required this.failed,
  });

  factory ApplySummary.fromJson(Map<String, dynamic> json) {
    return ApplySummary(
      total: (json['total'] as num?)?.toInt() ?? 0,
      created: (json['created'] as num?)?.toInt() ?? 0,
      configured: (json['configured'] as num?)?.toInt() ?? 0,
      unchanged: (json['unchanged'] as num?)?.toInt() ?? 0,
      failed: (json['failed'] as num?)?.toInt() ?? 0,
    );
  }

  final int total;
  final int created;
  final int configured;
  final int unchanged;
  final int failed;
}

class ApplyResponse {
  const ApplyResponse({required this.results, required this.summary});

  /// Parses an `/api/v1/yaml/apply` response. Backend wraps every
  /// success body in `{"data": {...}}` via `httputil.WriteData`; this
  /// factory expects the *unwrapped* inner map.
  factory ApplyResponse.fromApply(Map<String, dynamic> json) {
    final rawResults = (json['results'] as List?) ?? const <dynamic>[];
    final results = rawResults
        .whereType<Map<dynamic, dynamic>>()
        .map<ApplyResult>(
            (m) => ApplyResult.fromJson(Map<String, dynamic>.from(m)))
        .toList();
    final rawSummary =
        (json['summary'] as Map<String, dynamic>?) ?? const {};
    return ApplyResponse(
      results: results,
      summary: ApplySummary.fromJson(rawSummary),
    );
  }

  /// Parses an `/api/v1/yaml/validate` response — `{documents: [...],
  /// valid: bool}`. We adapt each doc into an [ApplyResult] (action is
  /// "valid"/"invalid" with errors joined) and synthesize a summary so
  /// the result panel can render both responses uniformly.
  factory ApplyResponse.fromValidate(Map<String, dynamic> json) {
    final rawDocs = (json['documents'] as List?) ?? const <dynamic>[];
    final results = rawDocs
        .whereType<Map<dynamic, dynamic>>()
        .map<ApplyResult>((m) =>
            ApplyResult.fromValidateDoc(Map<String, dynamic>.from(m)))
        .toList();
    var failed = 0;
    var unchanged = 0;
    for (final r in results) {
      if (r.action == 'invalid') {
        failed++;
      } else {
        unchanged++;
      }
    }
    return ApplyResponse(
      results: results,
      summary: ApplySummary(
        total: results.length,
        created: 0,
        configured: 0,
        unchanged: unchanged,
        failed: failed,
      ),
    );
  }

  final List<ApplyResult> results;
  final ApplySummary summary;
}

/// State machine status. Distinguishes "validate succeeded" (validated)
/// from "apply succeeded" (applied) so the UI can render a dry-run
/// preview separately from the live-apply result panel.
enum YamlApplyStatus { idle, validating, validated, applying, applied, failed }

class YamlApplyState {
  const YamlApplyState({
    required this.status,
    required this.yamlContent,
    this.result,
    this.error,
  });

  const YamlApplyState.idle(String yaml)
      : this(status: YamlApplyStatus.idle, yamlContent: yaml);

  final YamlApplyStatus status;
  final String yamlContent;
  final ApplyResponse? result;
  final String? error;

  YamlApplyState copyWith({
    YamlApplyStatus? status,
    String? yamlContent,
    ApplyResponse? result,
    String? error,
    bool clearResult = false,
    bool clearError = false,
  }) {
    return YamlApplyState(
      status: status ?? this.status,
      yamlContent: yamlContent ?? this.yamlContent,
      result: clearResult ? null : (result ?? this.result),
      error: clearError ? null : (error ?? this.error),
    );
  }
}

class YamlApplyKey {
  const YamlApplyKey({
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String kind;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is YamlApplyKey &&
      other.clusterId == clusterId &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, kind, namespace, name);
}

class YamlApplyController
    extends AutoDisposeFamilyNotifier<YamlApplyState, YamlApplyKey> {
  @override
  YamlApplyState build(YamlApplyKey arg) {
    return const YamlApplyState.idle('');
  }

  /// Replace the editor content. Resets any prior result/error so the
  /// dry-run preview from a previous Validate doesn't stay visible
  /// against now-stale content (the operator otherwise believes they
  /// applied what they validated).
  void setContent(String yaml) {
    state = state.copyWith(
      yamlContent: yaml,
      status: YamlApplyStatus.idle,
      clearResult: true,
      clearError: true,
    );
  }

  /// Hit /v1/yaml/validate. Server returns a dry-run preview shaped as
  /// `{documents, valid}` (different from apply's `{results, summary}`);
  /// [_post] adapts it into [ApplyResponse] so the result panel renders
  /// both responses uniformly.
  Future<void> validate() async {
    if (state.status == YamlApplyStatus.validating ||
        state.status == YamlApplyStatus.applying) {
      return;
    }
    if (!_clusterStillPinned()) return;
    state = state.copyWith(
      status: YamlApplyStatus.validating,
      clearResult: true,
      clearError: true,
    );
    try {
      final res = await _post('/api/v1/yaml/validate', isApply: false);
      state = state.copyWith(
        status: YamlApplyStatus.validated,
        result: res,
      );
    } on ApiError catch (e) {
      state = state.copyWith(
        status: YamlApplyStatus.failed,
        error: e.message,
      );
    } catch (e) {
      state = state.copyWith(
        status: YamlApplyStatus.failed,
        error: 'Validate failed unexpectedly.',
      );
    }
  }

  /// Hit /v1/yaml/apply. On success, invalidates the resource's GET so
  /// the Overview/YAML tab refetches with the new state.
  ///
  /// **Cluster pinning:** the controller's family key carries the
  /// cluster id captured at parent-build time. If the operator switched
  /// clusters between editor open and Apply, [_clusterStillPinned]
  /// returns false and we abort with a clear error rather than firing
  /// the write at the wrong cluster (the same defense PR-2a added to
  /// `ResourceActionsButton`).
  Future<void> apply() async {
    if (state.status == YamlApplyStatus.validating ||
        state.status == YamlApplyStatus.applying) {
      return;
    }
    if (!_clusterStillPinned()) return;
    state = state.copyWith(
      status: YamlApplyStatus.applying,
      clearResult: true,
      clearError: true,
    );
    try {
      final res = await _post('/api/v1/yaml/apply', isApply: true);
      state = state.copyWith(
        status: YamlApplyStatus.applied,
        result: res,
      );
      // Refetch the resource so Overview reflects the new state.
      ref.invalidate(resourceGetProvider(ResourceGetKey(
        clusterId: arg.clusterId,
        kind: arg.kind,
        namespace: arg.namespace,
        name: arg.name,
      )));
    } on ApiError catch (e) {
      state = state.copyWith(
        status: YamlApplyStatus.failed,
        error: e.message,
      );
    } catch (e) {
      state = state.copyWith(
        status: YamlApplyStatus.failed,
        error: 'Apply failed unexpectedly.',
      );
    }
  }

  /// Verify the active cluster is still the one this controller was
  /// keyed against. Sets state to failed with an explanatory message if
  /// it isn't.
  bool _clusterStillPinned() {
    final active = ref.read(activeClusterProvider);
    if (active == arg.clusterId) return true;
    state = state.copyWith(
      status: YamlApplyStatus.failed,
      error:
          'Cluster changed during this edit. Aborted to avoid mutating '
          'the wrong cluster. Re-open the editor.',
    );
    return false;
  }

  /// Reset to idle preserving the current editor content. Used by the
  /// "Done" button in the result panel and by Reload.
  void reset() {
    state = state.copyWith(
      status: YamlApplyStatus.idle,
      clearResult: true,
      clearError: true,
    );
  }

  /// POSTs YAML content and unwraps the canonical `{data: ...}` envelope
  /// before deserializing. [isApply] picks the response shape: apply
  /// returns `{results, summary}`, validate returns `{documents, valid}`.
  Future<ApplyResponse> _post(String path, {required bool isApply}) async {
    final dio = ref.read(dioProvider);
    try {
      final res = await dio.post<Map<String, dynamic>>(
        path,
        data: state.yamlContent,
        options: Options(
          contentType: 'application/yaml',
          headers: {'Accept': 'application/json'},
        ),
      );
      final outer = res.data ?? const <String, dynamic>{};
      final inner =
          (outer['data'] as Map<String, dynamic>?) ?? const <String, dynamic>{};
      return isApply
          ? ApplyResponse.fromApply(inner)
          : ApplyResponse.fromValidate(inner);
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final yamlApplyControllerProvider = AutoDisposeNotifierProvider.family<
    YamlApplyController, YamlApplyState, YamlApplyKey>(YamlApplyController.new);
