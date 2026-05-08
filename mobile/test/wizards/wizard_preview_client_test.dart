// Tests for WizardPreviewClient.
//
// Covers:
//   - 200 with `{data: {yaml}}` returns PreviewYaml
//   - 422 with `{error: {detail: <json-encoded array>}}` returns
//     PreviewErrors with parsed field/message entries
//   - 422 with malformed detail falls through to ApiError
//   - 5xx surfaces as ApiError

import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/wizards/wizard_preview_client.dart';
import 'package:kubecenter/wizards/wizard_step.dart';

import '../support/mock_dio_adapter.dart';

void main() {
  group('WizardPreviewClient', () {
    late Dio dio;
    late MockDioAdapter mock;

    setUp(() {
      mock = MockDioAdapter();
      dio = Dio(BaseOptions(baseUrl: 'http://test'));
      dio.httpClientAdapter = mock;
    });

    test('200 with {data:{yaml}} returns PreviewYaml', () async {
      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        body: {
          'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
        },
      );

      final result = await WizardPreviewClient(dio).preview(
        'configmap',
        {'name': 'cfg', 'namespace': 'default', 'data': {'k': 'v'}},
      );

      expect(result, isA<PreviewYaml>());
      expect((result as PreviewYaml).yaml,
          'apiVersion: v1\nkind: ConfigMap\n');
    });

    test('422 with field-level detail returns PreviewErrors', () async {
      final detail = json.encode([
        {'field': 'name', 'message': 'must be a valid DNS label'},
        {'field': 'data', 'message': 'at least one entry required'},
      ]);
      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': detail,
          },
        },
      );

      final result = await WizardPreviewClient(dio).preview(
        'configmap',
        const <String, dynamic>{},
      );

      expect(result, isA<PreviewErrors>());
      final errors = (result as PreviewErrors).errors;
      expect(errors.length, 2);
      expect(errors[0].field, 'name');
      expect(errors[0].message, contains('DNS label'));
      expect(errors[1].field, 'data');
    });

    test(
        '422 with non-JSON detail falls through to ApiError instead of '
        'silently returning empty errors', () async {
      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': 'this is not json',
          },
        },
      );

      expect(
        () => WizardPreviewClient(dio)
            .preview('configmap', const <String, dynamic>{}),
        throwsA(isA<ApiError>()),
      );
    });

    test('5xx surfaces as ApiError carrying status + message', () async {
      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        status: 503,
        body: {
          'error': {
            'code': 503,
            'message': 'backend overloaded',
          },
        },
      );

      try {
        await WizardPreviewClient(dio)
            .preview('configmap', const <String, dynamic>{});
        fail('expected ApiError');
      } on ApiError catch (e) {
        expect(e.statusCode, 503);
        expect(e.message, contains('backend overloaded'));
      }
    });

    test('WizardFieldError.fromJson tolerates missing fields', () {
      final empty = WizardFieldError.fromJson(const <String, dynamic>{});
      expect(empty.field, '');
      expect(empty.message, '');

      final partial = WizardFieldError.fromJson(<String, dynamic>{
        'field': 'name',
      });
      expect(partial.field, 'name');
      expect(partial.message, '');
    });
  });
}
