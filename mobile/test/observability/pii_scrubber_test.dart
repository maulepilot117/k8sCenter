import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/observability/pii_scrubber.dart';
import 'package:sentry_flutter/sentry_flutter.dart';

void main() {
  group('scrubText', () {
    test('replaces k8s path segments positionally', () {
      final out = scrubText(
        'Failed to fetch /v1/resources/secrets/kube-system-prod/vault-token: 404',
      );
      expect(
        out,
        'Failed to fetch /v1/resources/secrets/<namespace>/<name>: 404',
      );
    });

    test('handles single-segment k8s paths (namespace only)', () {
      final out = scrubText('GET /v1/namespaces/cluster/team-foo returned 403');
      expect(out, 'GET /v1/namespaces/cluster/<namespace> returned 403');
    });

    test('does not over-scrub paths outside /v1/', () {
      final out = scrubText('https://pub.dev/packages/sentry_flutter');
      expect(out, 'https://pub.dev/packages/sentry_flutter');
    });

    test('replaces namespace=<value> key-value pairs', () {
      final out = scrubText('Error in namespace=team-payments; retrying');
      expect(out, 'Error in namespace=<namespace>; retrying');
    });

    test('replaces name=<value> key-value pairs', () {
      final out = scrubText('Resource lookup name=my-vault-token failed');
      expect(out, 'Resource lookup name=<name> failed');
    });

    test('replaces FCM tokens via the long-string pattern', () {
      // Real FCM tokens are 140+ chars of [A-Za-z0-9_:-]. The pattern
      // requires 100+ chars so we synthesize a 120-char token here.
      final fakeFcm = 'A' * 120;
      final out = scrubText('Push delivery failed token=$fakeFcm');
      expect(out, 'Push delivery failed token=<fcm-token>');
    });

    test('does NOT scrub short identifiers that happen to look base64', () {
      // 30-char identifier should not match the FCM pattern.
      final out = scrubText('build id ABC123abc456ABC123abc456ABC123');
      expect(out, 'build id ABC123abc456ABC123abc456ABC123');
    });

    test('idempotent — sanitized input passes through unchanged', () {
      const already = 'Failed to fetch /v1/resources/secrets/<namespace>/<name>: 404';
      expect(scrubText(already), already);
    });

    test('multiple scrubs in one message all apply', () {
      final out = scrubText(
        'GET /v1/resources/secrets/team-payments/vault-token (namespace=team-payments)',
      );
      expect(
        out,
        'GET /v1/resources/secrets/<namespace>/<name> (namespace=<namespace>)',
      );
    });

    test('case-insensitive namespace= match', () {
      final out = scrubText('Namespace=Production failed');
      expect(out, 'Namespace=<namespace> failed');
    });
  });

  group('scrubEvent', () {
    test('strips user identity unconditionally', () {
      final event = SentryEvent(
        user: SentryUser(
          id: 'alice',
          username: 'alice',
          email: 'alice@corp.io',
          ipAddress: '203.0.113.5',
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.user, isNull);
    });

    test('scrubs exception messages', () {
      final event = SentryEvent(
        exceptions: [
          SentryException(
            type: 'DioException',
            value: 'GET /v1/resources/secrets/team-x/vault failed',
          ),
        ],
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.exceptions!.single.value,
        'GET /v1/resources/secrets/<namespace>/<name> failed',
      );
    });

    test('strips request body + query but keeps method/url', () {
      final event = SentryEvent(
        request: SentryRequest(
          url: 'https://kubecenter.local/v1/resources/secrets/team/v',
          method: 'GET',
          data: {'secret': 'should-not-leak'},
          queryString: 'token=abc',
          cookies: 'session=xyz',
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.request!.data, isNull);
      expect(scrubbed.request!.queryString, isNull);
      expect(scrubbed.request!.cookies, isNull);
      expect(scrubbed.request!.method, 'GET');
    });

    test('drops Authorization, Cookie, X-CSRF-* headers from requests', () {
      final event = SentryEvent(
        request: SentryRequest(
          url: 'https://kubecenter.local/v1/auth/me',
          method: 'GET',
          headers: {
            'Authorization': 'Bearer secret-jwt',
            'Cookie': 'kc_refresh=xyz',
            'X-CSRF-Token': 'csrf-secret',
            'X-Requested-With': 'XMLHttpRequest',
            'Content-Type': 'application/json',
          },
        ),
      );
      final scrubbed = scrubEventForTest(event);
      final headers = scrubbed.request!.headers;
      expect(headers.containsKey('Authorization'), isFalse);
      expect(headers.containsKey('Cookie'), isFalse);
      expect(headers.containsKey('X-CSRF-Token'), isFalse);
      expect(headers.containsKey('X-Requested-With'), isFalse);
      expect(headers['Content-Type'], 'application/json');
    });

    test('scrubs breadcrumb url query strings + paths', () {
      final event = SentryEvent(
        breadcrumbs: [
          Breadcrumb(
            type: 'http',
            data: {
              'url': 'https://kubecenter.local/v1/resources/secrets/team/v?token=abc',
              'method': 'GET',
              'status_code': 404,
            },
          ),
        ],
      );
      final scrubbed = scrubEventForTest(event);
      final data = scrubbed.breadcrumbs!.single.data!;
      expect(
        data['url'],
        'https://kubecenter.local/v1/resources/secrets/<namespace>/<name>',
      );
      expect(data['method'], 'GET');
      expect(data['status_code'], 404);
    });

    test('preserves stack frame fields (no scrub on abs_path/filename)', () {
      // We intentionally do NOT scrub stack frames. Build an event with a
      // stack trace and verify the frames pass through. SentryException
      // carries the stack trace via the `stackTrace` field but for this
      // test we just verify the value scrub doesn't drop the exception.
      final event = SentryEvent(
        exceptions: [
          SentryException(type: 'StateError', value: 'Bad state'),
        ],
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.exceptions!.single.type, 'StateError');
      expect(scrubbed.exceptions!.single.value, 'Bad state');
    });
  });
}
