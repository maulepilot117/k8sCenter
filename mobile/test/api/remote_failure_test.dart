// Maps the R-8 reason codes (KTD4 error envelopes, KTD5 status payloads)
// to the typed RemoteFailure the screens render. The reason set is the
// backend's closed k8s.ReasonCode (backend/internal/k8s/target_status.go).

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/remote_failure.dart';

ApiError _err(int status, String? reason) => ApiError(
      statusCode: status,
      code: status,
      message: 'backend message',
      reason: reason,
    );

void main() {
  group('RemoteFailure.fromError (KTD4 error envelopes)', () {
    final cases = <String, (int, RemoteFailureKind, bool)>{
      'unreachable': (502, RemoteFailureKind.unreachable, true),
      'credentials_invalid': (502, RemoteFailureKind.credentialsInvalid, false),
      'cluster_unknown': (404, RemoteFailureKind.clusterUnknown, false),
      'db_unavailable': (503, RemoteFailureKind.dbUnavailable, true),
      'unsupported_platform': (501, RemoteFailureKind.unsupportedPlatform, false),
      'discovery_unavailable': (502, RemoteFailureKind.discoveryUnavailable, true),
      'discovery_missing': (404, RemoteFailureKind.notInstalled, false),
    };

    cases.forEach((reason, expected) {
      final (status, kind, retryable) = expected;
      test('$reason maps to $kind (retryable: $retryable)', () {
        final f = RemoteFailure.fromError(_err(status, reason));
        expect(f, isNotNull);
        expect(f!.kind, kind);
        expect(f.retryable, retryable);
        expect(f.title, isNotEmpty);
        expect(f.message, isNotEmpty);
      });
    });

    test('unwraps the ApiError the ErrorMappingInterceptor puts on a '
        'DioException', () {
      final dio = DioException(
        requestOptions: RequestOptions(path: '/api/v1/diagnostics/ns/summary'),
        error: _err(501, 'unsupported_platform'),
      );
      expect(
        RemoteFailure.fromError(dio)?.kind,
        RemoteFailureKind.unsupportedPlatform,
      );
    });

    test('an error with no reason is not a remote failure', () {
      expect(RemoteFailure.fromError(_err(500, null)), isNull);
    });

    test('a reason outside the R-8 set is not a remote failure', () {
      // forbidden keeps its own backend message; endpoint reasons such as
      // active_job_exists belong to their screens.
      expect(RemoteFailure.fromError(_err(403, 'forbidden')), isNull);
      expect(RemoteFailure.fromError(_err(409, 'active_job_exists')), isNull);
    });

    test('a 501 without unsupported_platform is not claimed as remote-only',
        () {
      // Other 501s (e.g. Trivy-only vulnerability detail) are not about the
      // cluster being remote; their screens own the copy.
      expect(RemoteFailure.fromError(_err(501, null)), isNull);
    });

    test('non-API errors are not remote failures', () {
      expect(RemoteFailure.fromError(null), isNull);
      expect(RemoteFailure.fromError(StateError('boom')), isNull);
    });
  });

  group('RemoteFailure.fromReason (KTD5 status payloads)', () {
    test('a local payload (no reason) is a plain verdict', () {
      expect(RemoteFailure.fromReason(null), isNull);
      expect(RemoteFailure.fromReason(''), isNull);
    });

    test('discovery_missing is a real "not installed"', () {
      expect(
        RemoteFailure.fromReason('discovery_missing')?.kind,
        RemoteFailureKind.notInstalled,
      );
    });

    test('could-not-tell reasons are not a "not installed" verdict', () {
      for (final reason in const [
        'unreachable',
        'discovery_unavailable',
        'credentials_invalid',
        'cluster_unknown',
        'db_unavailable',
      ]) {
        final f = RemoteFailure.fromReason(reason);
        expect(f, isNotNull, reason: reason);
        expect(f!.kind, isNot(RemoteFailureKind.notInstalled), reason: reason);
        expect(f.presenceUnknown, isTrue, reason: reason);
      }
    });

    test('an unknown reason falls back to the family negative value', () {
      expect(RemoteFailure.fromReason('stale_observation'), isNull);
    });
  });
}
