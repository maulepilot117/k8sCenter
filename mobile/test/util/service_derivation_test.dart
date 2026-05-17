// Unit tests for findServicesForResource — the PR-5f service-name
// derivation that powers the Golden Signals tab on Pod and Deployment
// detail screens.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/util/service_derivation.dart';

Map<String, dynamic> _service({
  required String namespace,
  required String name,
  Map<String, String>? selector,
}) {
  return {
    'metadata': {'namespace': namespace, 'name': name},
    if (selector != null) 'spec': {'selector': selector},
    if (selector == null) 'spec': <String, dynamic>{},
  };
}

void main() {
  group('findServicesForResource', () {
    test('returns empty list when resource has no labels', () {
      final result = findServicesForResource(
        services: [
          _service(namespace: 'web', name: 'svc', selector: {'app': 'web'}),
        ],
        namespace: 'web',
        resourceLabels: const {},
      );
      expect(result, isEmpty,
          reason: 'No labels means no selector can match');
    });

    test('returns empty list when namespace is empty', () {
      final result = findServicesForResource(
        services: const [],
        namespace: '',
        resourceLabels: const {'app': 'web'},
      );
      expect(result, isEmpty);
    });

    test('matches a single Service whose selector is a subset', () {
      final result = findServicesForResource(
        services: [
          _service(namespace: 'web', name: 'web-svc', selector: {'app': 'web'}),
          _service(
              namespace: 'web', name: 'db-svc', selector: {'app': 'db'}),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web', 'tier': 'frontend'},
      );
      expect(result, hasLength(1));
      expect(result.first.name, 'web-svc');
    });

    test('skips Services in other namespaces', () {
      final result = findServicesForResource(
        services: [
          _service(namespace: 'web', name: 'svc-a', selector: {'app': 'web'}),
          _service(
              namespace: 'staging', name: 'svc-b', selector: {'app': 'web'}),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web'},
      );
      expect(result.map((s) => s.name), ['svc-a']);
    });

    test('skips Services with empty selectors', () {
      final result = findServicesForResource(
        services: [
          _service(namespace: 'web', name: 'headless', selector: const {}),
          _service(
              namespace: 'web', name: 'externalname-svc', selector: null),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web'},
      );
      expect(result, isEmpty,
          reason: 'Empty/missing selectors must never flood the picker');
    });

    test('ranks more-specific selectors first', () {
      final result = findServicesForResource(
        services: [
          _service(
              namespace: 'web',
              name: 'broad',
              selector: {'app': 'web'}),
          _service(
              namespace: 'web',
              name: 'narrow',
              selector: {'app': 'web', 'tier': 'frontend'}),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web', 'tier': 'frontend'},
      );
      expect(result.map((s) => s.name), ['narrow', 'broad']);
    });

    test(
        'breaks specificity ties alphabetically so the picker order is '
        'deterministic across builds', () {
      final result = findServicesForResource(
        services: [
          _service(
              namespace: 'web',
              name: 'zeta',
              selector: {'app': 'web'}),
          _service(
              namespace: 'web',
              name: 'alpha',
              selector: {'app': 'web'}),
          _service(
              namespace: 'web',
              name: 'beta',
              selector: {'app': 'web'}),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web'},
      );
      expect(result.map((s) => s.name), ['alpha', 'beta', 'zeta']);
    });

    test('rejects malformed selectors with non-string values', () {
      final result = findServicesForResource(
        services: [
          {
            'metadata': {'namespace': 'web', 'name': 'malformed'},
            'spec': {
              'selector': {'app': 42},
            },
          },
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web'},
      );
      expect(result, isEmpty,
          reason:
              'Malformed selectors must not produce a match — guessing '
              'a stringified compare would mask backend bugs.');
    });

    test('requires every selector key/value to be present on the resource',
        () {
      final result = findServicesForResource(
        services: [
          _service(
            namespace: 'web',
            name: 'wants-version',
            selector: {'app': 'web', 'version': 'v2'},
          ),
        ],
        namespace: 'web',
        resourceLabels: const {'app': 'web', 'version': 'v1'},
      );
      expect(result, isEmpty,
          reason:
              'A Service whose selector references a missing or '
              'differently-valued label must not match.');
    });
  });
}
