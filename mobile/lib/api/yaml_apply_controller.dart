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

  final int index;
  final String kind;
  final String name;
  final String? namespace;

  /// One of "created" | "configured" | "unchanged" | "failed".
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

  factory ApplyResponse.fromJson(Map<String, dynamic> json) {
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

  /// Replace the editor content. Resets any prior result/error.
  void setContent(String yaml) {
    state = state.copyWith(
      yamlContent: yaml,
      status: YamlApplyStatus.idle,
      clearResult: true,
      clearError: true,
    );
  }

  /// Hit /v1/yaml/validate. Server returns the dry-run preview (same
  /// shape as apply) so the operator can see what will change before
  /// committing.
  Future<void> validate() async {
    if (state.status == YamlApplyStatus.validating ||
        state.status == YamlApplyStatus.applying) {
      return;
    }
    state = state.copyWith(
      status: YamlApplyStatus.validating,
      clearResult: true,
      clearError: true,
    );
    try {
      final res = await _post('/api/v1/yaml/validate');
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
  Future<void> apply() async {
    if (state.status == YamlApplyStatus.validating ||
        state.status == YamlApplyStatus.applying) {
      return;
    }
    state = state.copyWith(
      status: YamlApplyStatus.applying,
      clearResult: true,
      clearError: true,
    );
    try {
      final res = await _post('/api/v1/yaml/apply');
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

  /// Reset to idle preserving the current editor content. Used by the
  /// "Done" button in the result panel and by Reload.
  void reset() {
    state = state.copyWith(
      status: YamlApplyStatus.idle,
      clearResult: true,
      clearError: true,
    );
  }

  Future<ApplyResponse> _post(String path) async {
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
      final body = res.data ?? const <String, dynamic>{};
      return ApplyResponse.fromJson(body);
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final yamlApplyControllerProvider = AutoDisposeNotifierProvider.family<
    YamlApplyController, YamlApplyState, YamlApplyKey>(YamlApplyController.new);
