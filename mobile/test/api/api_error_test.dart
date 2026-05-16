// Hardening guarantees for ApiError.fromDio and ApiError.messageOf.
// See issue #261: previously, non-canonical response bodies could surface
// `Map.toString()` braces-and-commas in user-visible error toasts.

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';

DioException _badResponse({
  required int status,
  required Object? body,
  String? message,
}) {
  final req = RequestOptions(path: '/api/v1/test');
  return DioException(
    requestOptions: req,
    type: DioExceptionType.badResponse,
    message:
        message ??
        'This exception was thrown because the response has a status code '
            'of $status and RequestOptions.validateStatus was configured to '
            'throw for this status code.',
    response: Response<Object?>(
      requestOptions: req,
      statusCode: status,
      data: body,
    ),
  );
}

void main() {
  group('ApiError.fromDio canonical envelope', () {
    test('reads {error:{code,message,detail}} as the canonical shape', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 422,
          body: {
            'error': {
              'code': 4001,
              'message': 'Validation failed',
              'detail': 'field name required',
            },
          },
        ),
      );
      expect(err.statusCode, 422);
      expect(err.code, 4001);
      expect(err.message, 'Validation failed');
      expect(err.detail, 'field name required');
      expect(err.reason, isNull);
    });

    test('reads reason and ignores extra/unknown fields safely', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 403,
          body: {
            'error': {
              'code': 403,
              'message': 'Forbidden',
              'reason': 'rbac_denied',
              'extra': {'namespace': 'default'},
            },
          },
        ),
      );
      expect(err.message, 'Forbidden');
      expect(err.reason, 'rbac_denied');
    });
  });

  group('ApiError.fromDio defensive parsing', () {
    test(
      'non-String nested message falls back to status text, no Map leak',
      () {
        final err = ApiError.fromDio(
          _badResponse(
            status: 500,
            body: {
              'error': {
                'code': 500,
                'message': {'nested': 'object'},
              },
            },
          ),
        );
        expect(err.message, 'Server error');
        expect(err.message, isNot(contains('{')));
        expect(err.message, isNot(contains('nested')));
      },
    );

    test('non-String nested detail is dropped, not stringified', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 422,
          body: {
            'error': {
              'message': 'Validation failed',
              'detail': {
                'fields': ['name', 'age'],
              },
            },
          },
        ),
      );
      expect(err.message, 'Validation failed');
      expect(err.detail, isNull);
    });

    test('top-level {message,code,detail} envelope is parsed as fallback', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 503,
          body: {
            'message': 'Backend offline',
            'code': 503,
            'detail': 'circuit breaker open',
            'reason': 'upstream_unavailable',
          },
        ),
      );
      expect(err.message, 'Backend offline');
      expect(err.code, 503);
      expect(err.detail, 'circuit breaker open');
      expect(err.reason, 'upstream_unavailable');
    });

    test('Map body with no recognized shape never leaks Map.toString()', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 500,
          body: {
            'detail': 'something happened',
            'reason': 'unknown_problem',
            'trace_id': 'abc123',
          },
        ),
      );
      expect(err.message, 'Server error');
      expect(err.message, isNot(contains('{')));
      expect(err.message, isNot(contains(',')));
      expect(err.message, isNot(contains('trace_id')));
    });

    test('plain string body is used as message when short and non-HTML', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 500,
          body: 'Internal server error from upstream proxy',
        ),
      );
      expect(err.message, 'Internal server error from upstream proxy');
    });

    test('HTML body falls back to status text', () {
      final err = ApiError.fromDio(
        _badResponse(
          status: 502,
          body: '<html><body><h1>502 Bad Gateway</h1></body></html>',
        ),
      );
      expect(err.message, 'Server error');
      expect(err.message, isNot(contains('<')));
    });

    test('very long string body falls back to status text', () {
      final err = ApiError.fromDio(_badResponse(status: 500, body: 'x' * 500));
      expect(err.message, 'Server error');
    });

    test('empty body falls back to status text', () {
      final err = ApiError.fromDio(_badResponse(status: 404, body: null));
      expect(err.message, 'Not found');
    });

    test(
      "Dio's internal validateStatus blurb is replaced with status text",
      () {
        final err = ApiError.fromDio(_badResponse(status: 429, body: null));
        expect(err.message, 'Rate limited');
        expect(err.message, isNot(contains('validateStatus')));
      },
    );
  });

  group('ApiError.fromDio network failures', () {
    test('null response surfaces Dio diagnostic message when present', () {
      final req = RequestOptions(path: '/api/v1/test');
      final err = ApiError.fromDio(
        DioException(
          requestOptions: req,
          type: DioExceptionType.connectionError,
          message: 'SocketException: connection refused',
        ),
      );
      expect(err.statusCode, 0);
      expect(err.message, 'SocketException: connection refused');
    });

    test('connection error with no message yields "Network error"', () {
      final req = RequestOptions(path: '/api/v1/test');
      final err = ApiError.fromDio(
        DioException(
          requestOptions: req,
          type: DioExceptionType.connectionError,
        ),
      );
      expect(err.message, 'Network error');
    });

    test('408 timeout yields friendly text', () {
      final err = ApiError.fromDio(_badResponse(status: 408, body: null));
      expect(err.message, 'Request timed out');
    });
  });

  group('ApiError.messageOf', () {
    test('returns ApiError.message for ApiError', () {
      final e = ApiError(statusCode: 500, code: 500, message: 'boom');
      expect(ApiError.messageOf(e), 'boom');
    });

    test('unwraps ApiError from DioException.error', () {
      final inner = ApiError(statusCode: 500, code: 500, message: 'wrapped');
      final req = RequestOptions(path: '/x');
      final dio = DioException(
        requestOptions: req,
        type: DioExceptionType.unknown,
        error: inner,
      );
      expect(ApiError.messageOf(dio), 'wrapped');
    });

    test('raw DioException with no inner ApiError uses friendly text', () {
      final dio = _badResponse(status: 500, body: {'random': 'shape'});
      expect(ApiError.messageOf(dio), 'Server error');
      expect(ApiError.messageOf(dio), isNot(contains('{')));
    });

    test('null error yields "Unknown error"', () {
      expect(ApiError.messageOf(null), 'Unknown error');
    });

    test('arbitrary string-coercible error round-trips toString', () {
      expect(
        ApiError.messageOf(StateError('bad state')),
        contains('bad state'),
      );
    });

    test('pathologically long error toString is suppressed', () {
      final huge = Exception('x' * 1000);
      expect(ApiError.messageOf(huge), 'Unknown error');
    });
  });
}
