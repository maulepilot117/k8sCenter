// Typed parse of the backend's error envelope: `{error:{code, message, detail,
// reason}}`. Mirrors `frontend/lib/api.ts` ApiError so widget error states
// render identical wording on both surfaces.
//
// Robustness contract: `ApiError.fromDio` never lets a non-String value leak
// into `message`, `detail`, or `reason`. Backends returning unexpected body
// shapes (top-level fields, plain string, HTML, Map-with-no-recognized-key)
// fall back to a friendly status-derived message rather than rendering raw
// `Map.toString()` braces-and-commas to operators.

import 'package:dio/dio.dart';

class ApiError implements Exception {
  ApiError({
    required this.statusCode,
    required this.code,
    required this.message,
    this.detail,
    this.reason,
    this.extra,
  });

  final int statusCode;
  final int code;
  final String message;
  final String? detail;

  /// Endpoint-specific reason code (e.g. "active_job_exists",
  /// "scope_changed"). Mirrors the frontend's `ApiError.reason`.
  final String? reason;

  /// Endpoint-specific structured detail (e.g. `{jobId: "..."}` on
  /// `active_job_exists`, `{added: [...], removed: [...]}` on
  /// `scope_changed`). Mirrors the frontend's `ApiError.body.error.extra`.
  /// Prefer the [extraString] helper over reading the map directly.
  final Map<String, dynamic>? extra;

  /// Returns the `extra[key]` value as a String, or null when the key is
  /// missing or non-string. Mirrors `errorExtra(err, key)` in the web
  /// `api.ts`. Use for 409 `active_job_exists.jobId` and similar.
  String? extraString(String key) {
    final v = extra?[key];
    return v is String ? v : null;
  }

  /// Builds an ApiError from a DioException by inspecting the response body.
  /// Tolerates three body shapes; everything else falls back to a friendly
  /// status-derived message:
  ///   1. Canonical envelope: `{error: {code, message, detail, reason, extra}}`
  ///   2. Top-level fields: `{message, code, detail, reason, extra}`
  ///   3. Plain string body — used as-is when short and not HTML
  factory ApiError.fromDio(DioException e) {
    final res = e.response;
    final status = res?.statusCode ?? 0;
    final body = res?.data;

    if (body is Map) {
      final nested = body['error'];
      if (nested is Map) {
        final msg = _asString(nested['message']);
        if (msg != null) {
          return ApiError(
            statusCode: status,
            code: _asInt(nested['code']) ?? status,
            message: msg,
            detail: _asString(nested['detail']),
            reason: _asString(nested['reason']),
            extra: _asExtra(nested['extra']),
          );
        }
      }

      final topMessage = _asString(body['message']);
      if (topMessage != null) {
        return ApiError(
          statusCode: status,
          code: _asInt(body['code']) ?? status,
          message: topMessage,
          detail: _asString(body['detail']),
          reason: _asString(body['reason']),
          extra: _asExtra(body['extra']),
        );
      }
    }

    if (body is String) {
      final trimmed = body.trim();
      if (trimmed.isNotEmpty &&
          trimmed.length <= 200 &&
          !trimmed.startsWith('<')) {
        return ApiError(statusCode: status, code: status, message: trimmed);
      }
    }

    return ApiError(
      statusCode: status,
      code: status,
      message: _dioFriendlyMessage(e) ?? _statusText(status),
    );
  }

  /// Resolves any error object into a human-readable string. Use in catch
  /// blocks where the error type isn't statically guaranteed and the previous
  /// pattern was `error.toString()` — that fallback can leak Dio's stringified
  /// response body for raw `DioException` instances. Prefer this helper.
  static String messageOf(Object? error) {
    if (error == null) return 'Unknown error';
    if (error is ApiError) return error.message;
    if (error is DioException) {
      final inner = error.error;
      if (inner is ApiError) return inner.message;
      return _dioFriendlyMessage(error) ??
          _statusText(error.response?.statusCode ?? 0);
    }
    final s = error.toString();
    if (s.isEmpty || s.length > 500) return 'Unknown error';
    return s;
  }

  static String? _asString(Object? v) => v is String ? v : null;
  static int? _asInt(Object? v) => v is int ? v : null;
  static Map<String, dynamic>? _asExtra(Object? v) =>
      v is Map ? Map<String, dynamic>.from(v) : null;

  // Dio's default badResponse message is the internal validateStatus blurb;
  // surface friendly status text instead.
  static String? _dioFriendlyMessage(DioException e) {
    final m = e.message;
    if (m == null || m.isEmpty) return null;
    if (m.startsWith('This exception was thrown')) return null;
    return m;
  }

  static String _statusText(int status) {
    if (status == 0) return 'Network error';
    if (status == 401) return 'Authentication required';
    if (status == 403) return 'Permission denied';
    if (status == 404) return 'Not found';
    if (status == 408 || status == 504) return 'Request timed out';
    if (status == 409) return 'Conflict';
    if (status == 422) return 'Validation failed';
    if (status == 429) return 'Rate limited';
    if (status >= 500) return 'Server error';
    if (status >= 400) return 'Request failed';
    return 'OK';
  }

  @override
  String toString() => 'ApiError($statusCode): $message';
}
