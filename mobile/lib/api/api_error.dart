// Typed parse of the backend's error envelope: `{error:{code, message, detail}}`.
// Mirrors `frontend/lib/api.ts` ApiError so widget error states render
// identical wording on both surfaces.

import 'package:dio/dio.dart';

class ApiError implements Exception {
  ApiError({
    required this.statusCode,
    required this.code,
    required this.message,
    this.detail,
  });

  final int statusCode;
  final int code;
  final String message;
  final String? detail;

  /// Builds an ApiError from a DioException. When the response carries the
  /// canonical envelope, fields populate from `error.{code,message,detail}`;
  /// otherwise they fall back to HTTP status + Dio's default message.
  factory ApiError.fromDio(DioException e) {
    final res = e.response;
    final status = res?.statusCode ?? 0;

    final body = res?.data;
    if (body is Map) {
      final error = body['error'];
      if (error is Map) {
        return ApiError(
          statusCode: status,
          code: (error['code'] as int?) ?? status,
          message: (error['message'] as String?) ?? _statusText(status),
          detail: error['detail'] as String?,
        );
      }
    }

    return ApiError(
      statusCode: status,
      code: status,
      message: e.message ?? _statusText(status),
    );
  }

  static String _statusText(int status) {
    if (status == 0) return 'Network error';
    if (status >= 500) return 'Server error';
    if (status >= 400) return 'Request failed';
    return 'OK';
  }

  @override
  String toString() => 'ApiError($statusCode): $message';
}
