// Lightweight Dio HttpClientAdapter that returns canned responses keyed
// by path. Used by interceptor + repository tests so we don't depend on
// `dio_test_adapter` (extra dep, fewer features than we need).

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';

class MockDioAdapter implements HttpClientAdapter {
  final List<RequestOptions> requests = [];

  /// Map of `METHOD path` → handler returning the canned response.
  /// Handlers are called in order for repeated keys.
  final Map<String, List<ResponseBody Function(RequestOptions)>> _handlers = {};

  void on(
    String method,
    String path,
    ResponseBody Function(RequestOptions) handler,
  ) {
    final key = '${method.toUpperCase()} $path';
    (_handlers[key] ??= []).add(handler);
  }

  /// Convenience for JSON responses.
  void onJson(
    String method,
    String path, {
    int status = 200,
    required Object body,
  }) {
    on(method, path, (req) {
      final encoded = utf8.encode(jsonEncode(body));
      return ResponseBody.fromBytes(
        encoded,
        status,
        headers: {
          Headers.contentTypeHeader: ['application/json'],
        },
      );
    });
  }

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    final key = '${options.method.toUpperCase()} ${options.path}';
    final list = _handlers[key];
    if (list == null || list.isEmpty) {
      return ResponseBody.fromString(
        '{"error":{"code":404,"message":"unmocked: $key"}}',
        404,
        headers: {
          Headers.contentTypeHeader: ['application/json'],
        },
      );
    }
    final handler = list.length > 1 ? list.removeAt(0) : list.first;
    return handler(options);
  }

  @override
  void close({bool force = false}) {}
}
