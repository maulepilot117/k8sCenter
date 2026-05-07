// Wizard preview HTTP client. Wraps POST /api/v1/wizards/:type/preview
// and parses the two response shapes the backend can return:
//
//   200 { "data": { "yaml": "<multi-line YAML>" } }
//   422 { "error": {
//           "code": 422,
//           "message": "validation failed",
//           "detail": "[{\"field\":\"...\",\"message\":\"...\"}, ...]"
//         } }
//
// Other status codes propagate as ApiError via the regular dio
// interceptor stack so widgets handle them through the same error
// surface they use for everything else.
//
// The 422 detail is JSON-encoded inside a string field — a quirk of how
// the backend currently serializes field errors. We decode it here so
// callers always receive a typed `List<WizardFieldError>`.

import 'dart:convert';

import 'package:dio/dio.dart';

import '../api/api_error.dart';
import 'wizard_step.dart';

/// Result of a preview call. Either a YAML string (validation passed,
/// preview rendered) or a list of field-level errors (validation failed,
/// each error pointing to a specific form field).
sealed class PreviewResult {
  const PreviewResult();
}

class PreviewYaml extends PreviewResult {
  const PreviewYaml(this.yaml);
  final String yaml;
}

class PreviewErrors extends PreviewResult {
  const PreviewErrors(this.errors);
  final List<WizardFieldError> errors;
}

class WizardPreviewClient {
  WizardPreviewClient(this._dio);

  final Dio _dio;

  /// POST /api/v1/wizards/:type/preview with the given form body.
  ///
  /// Returns [PreviewYaml] on 200, [PreviewErrors] on 422. Anything else
  /// throws [ApiError] (network failure, 5xx, auth issues — same surface
  /// every other write hits in this app).
  Future<PreviewResult> preview(
    String type,
    Map<String, dynamic> body,
  ) async {
    try {
      final res = await _dio.post<Map<String, dynamic>>(
        '/api/v1/wizards/$type/preview',
        data: body,
        options: Options(
          contentType: 'application/json',
          headers: {'Accept': 'application/json'},
        ),
      );
      final outer = res.data ?? const <String, dynamic>{};
      final inner = (outer['data'] as Map<String, dynamic>?) ??
          const <String, dynamic>{};
      final yaml = inner['yaml'] as String? ?? '';
      return PreviewYaml(yaml);
    } on DioException catch (e) {
      // 422 carries field errors as JSON-encoded `detail` — extract them
      // and return a typed PreviewErrors instead of throwing. Anything
      // else flows through the canonical ApiError pipeline.
      final res = e.response;
      if (res?.statusCode == 422) {
        final fieldErrors = _parseFieldErrors(res?.data);
        if (fieldErrors.isNotEmpty) {
          return PreviewErrors(fieldErrors);
        }
      }
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Parse the JSON-encoded `error.detail` array. Returns an empty list
  /// if the payload doesn't carry the expected shape so callers fall
  /// through to a generic ApiError surface.
  static List<WizardFieldError> _parseFieldErrors(dynamic body) {
    if (body is! Map) return const [];
    final error = body['error'];
    if (error is! Map) return const [];
    final detail = error['detail'];
    if (detail is! String || detail.isEmpty) return const [];
    try {
      final decoded = json.decode(detail);
      if (decoded is! List) return const [];
      return decoded
          .whereType<Map<dynamic, dynamic>>()
          .map((m) =>
              WizardFieldError.fromJson(Map<String, dynamic>.from(m)))
          .where((e) => e.field.isNotEmpty || e.message.isNotEmpty)
          .toList();
    } on FormatException {
      return const [];
    }
  }
}
