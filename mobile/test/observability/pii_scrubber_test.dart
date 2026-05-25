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

    test('scrubs diagnostics path shape', () {
      // Diagnostics paths embed namespace as the FIRST segment after the
      // endpoint name, not the third — distinct from the resources API
      // shape. This pattern is applied before the generic regex.
      final out = scrubText(
        'GET /v1/diagnostics/team-payments/Deployment/checkout failed',
      );
      expect(
        out,
        'GET /v1/diagnostics/<namespace>/<kind>/<name> failed',
      );
    });

    test('scrubs /ws/logs/<ns>/<pod>/<container> shape', () {
      final out =
          scrubText('connecting /ws/logs/team-x/redis-0/redis to backend');
      expect(
        out,
        'connecting /ws/logs/<namespace>/<pod>/<container> to backend',
      );
    });

    test('scrubs /ws/exec/<ns>/<pod>/<container> shape', () {
      final out = scrubText('opened /ws/exec/team-y/app-7d4c-abcd/sidecar');
      expect(
        out,
        'opened /ws/exec/<namespace>/<pod>/<container>',
      );
    });

    test('empty input passes through', () {
      expect(scrubText(''), '');
    });

    test('whitespace-only input passes through unchanged', () {
      expect(scrubText('   '), '   ');
    });

    test('two independent /v1/ paths in one message both scrub', () {
      final out = scrubText(
        'rolling /v1/resources/secrets/team-a/cred-1 then '
        '/v1/resources/pods/team-b/runner-2',
      );
      expect(
        out,
        'rolling /v1/resources/secrets/<namespace>/<name> then '
        '/v1/resources/pods/<namespace>/<name>',
      );
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

    test(
        'strips request body, query, cookies, fragment and scrubs url path '
        '(P2-10)', () {
      final event = SentryEvent(
        request: SentryRequest(
          url: 'https://kubecenter.local/v1/resources/secrets/team/v',
          method: 'GET',
          data: {'secret': 'should-not-leak'},
          queryString: 'token=abc',
          cookies: 'session=xyz',
          fragment: 'leaky-anchor',
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.request!.data, isNull);
      expect(scrubbed.request!.queryString, isNull);
      expect(scrubbed.request!.cookies, isNull);
      expect(
        scrubbed.request!.fragment,
        isNull,
        reason: 'fragment is a sibling of url and can carry identifiers',
      );
      expect(scrubbed.request!.method, 'GET');
      expect(
        scrubbed.request!.url,
        'https://kubecenter.local/v1/resources/secrets/<namespace>/<name>',
        reason: 'path segments must be scrubbed, not preserved (P2-10)',
      );
    });

    test('strips query + fragment from SentryRequest.url (P2-10)', () {
      final event = SentryEvent(
        request: SentryRequest(
          url:
              'https://kubecenter.local/v1/resources/secrets/team/v?token=leak#anchor-leak',
          method: 'GET',
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.request!.url,
        'https://kubecenter.local/v1/resources/secrets/<namespace>/<name>',
        reason: 'query and fragment must be sliced before path scrubbing',
      );
    });

    test('SentryRequest.url passes through unchanged when no scrub-worthy '
        'segments are present (P2-10)', () {
      final event = SentryEvent(
        request: SentryRequest(
          url: 'https://pub.dev/packages/sentry_flutter',
          method: 'GET',
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.request!.url,
        'https://pub.dev/packages/sentry_flutter',
        reason: 'non-k8sCenter URLs should be left intact',
      );
    });

    test('SentryRequest.url null passes through as null (P2-10)', () {
      final event = SentryEvent(
        request: SentryRequest(method: 'GET'),
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.request!.url, isNull);
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

    test('scrubs event.transaction', () {
      final event = SentryEvent(
        transaction: 'GET /v1/resources/secrets/ns-a/cred-x',
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.transaction,
        'GET /v1/resources/secrets/<namespace>/<name>',
      );
    });

    test('scrubs event.fingerprint entries', () {
      final event = SentryEvent(
        fingerprint: [
          'namespace=team-x',
          '/v1/resources/secrets/team-y/cred-z',
        ],
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.fingerprint, [
        'namespace=<namespace>',
        '/v1/resources/secrets/<namespace>/<name>',
      ]);
    });

    test('scrubs event.culprit', () {
      final event = SentryEvent(
        culprit: 'crash in /v1/resources/pods/team-x/runner-1',
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.culprit,
        'crash in /v1/resources/pods/<namespace>/<name>',
      );
    });

    test('scrubs event.tags values', () {
      final event = SentryEvent(
        tags: {
          'env': 'prod',
          'route': '/v1/resources/secrets/ns/v',
        },
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.tags!['env'], 'prod');
      expect(scrubbed.tags!['route'], '/v1/resources/secrets/<namespace>/<name>');
    });

    test('scrubs string values in event.extra; non-strings pass through', () {
      final event = SentryEvent(
        // ignore: deprecated_member_use
        extra: {
          'path': '/v1/resources/pods/team-x/runner-1',
          'retry_count': 3,
          'success': false,
        },
      );
      final scrubbed = scrubEventForTest(event);
      // ignore: deprecated_member_use
      expect(scrubbed.extra!['path'],
          '/v1/resources/pods/<namespace>/<name>');
      // ignore: deprecated_member_use
      expect(scrubbed.extra!['retry_count'], 3);
      // ignore: deprecated_member_use
      expect(scrubbed.extra!['success'], false);
    });

    test('scrubs string SentryMessage.params; non-strings pass', () {
      final event = SentryEvent(
        message: SentryMessage(
          'lookup failed for %s in %s after %d tries',
          template: 'lookup failed for %s in %s after %d tries',
          params: [
            'name=vault-token',
            'namespace=team-x',
            3,
          ],
        ),
      );
      final scrubbed = scrubEventForTest(event);
      final params = scrubbed.message!.params!;
      expect(params[0], 'name=<name>');
      expect(params[1], 'namespace=<namespace>');
      expect(params[2], 3);
    });

    test('SentryMessage with null template does not throw', () {
      final event = SentryEvent(
        message: SentryMessage(
          'GET /v1/resources/secrets/ns/v failed',
          template: null,
        ),
      );
      final scrubbed = scrubEventForTest(event);
      expect(
        scrubbed.message!.formatted,
        'GET /v1/resources/secrets/<namespace>/<name> failed',
      );
      expect(scrubbed.message!.template, isNull);
    });

    test('SentryException(value: null) does not throw', () {
      final event = SentryEvent(
        exceptions: [SentryException(type: 'StateError', value: null)],
      );
      final scrubbed = scrubEventForTest(event);
      expect(scrubbed.exceptions!.single.value, isNull);
    });

    test('scrubs string values in non-http breadcrumb data', () {
      final event = SentryEvent(
        breadcrumbs: [
          Breadcrumb(
            type: 'navigation',
            category: 'navigation',
            data: {
              'from': '/v1/resources/secrets/ns/name',
              'to': '/v1/resources/pods/ns/name',
            },
          ),
        ],
      );
      final scrubbed = scrubEventForTest(event);
      final data = scrubbed.breadcrumbs!.single.data!;
      expect(data['from'], '/v1/resources/secrets/<namespace>/<name>');
      expect(data['to'], '/v1/resources/pods/<namespace>/<name>');
    });

    test('drops x-cluster-id and proxy-authorization headers', () {
      final event = SentryEvent(
        request: SentryRequest(
          url: 'https://kubecenter.local/v1/auth/me',
          method: 'GET',
          headers: {
            'X-Cluster-Id': 'prod-east',
            'Proxy-Authorization': 'Basic abc',
            'Content-Type': 'application/json',
          },
        ),
      );
      final scrubbed = scrubEventForTest(event);
      final headers = scrubbed.request!.headers;
      expect(headers.containsKey('X-Cluster-Id'), isFalse);
      expect(headers.containsKey('Proxy-Authorization'), isFalse);
      expect(headers['Content-Type'], 'application/json');
    });
  });
}
