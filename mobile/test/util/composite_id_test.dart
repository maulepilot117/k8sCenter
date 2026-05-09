// Tests for the three composite-ID encoders + the URL-encoding
// transport pair. These IDs flow through go_router path segments,
// so the encode/decode round-trip is the load-bearing invariant.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/util/composite_id.dart';

void main() {
  group('GitOpsId', () {
    test('parses tool:ns:name and round-trips through encode', () {
      final id = GitOpsId.tryParse('argocd:argocd:my-app')!;
      expect(id.tool, 'argocd');
      expect(id.namespace, 'argocd');
      expect(id.name, 'my-app');
      expect(id.encode(), 'argocd:argocd:my-app');
    });

    test('parses fluxcd Kustomization id', () {
      final id = GitOpsId.tryParse('flux-ks:flux-system:my-ks')!;
      expect(id.tool, 'flux-ks');
      expect(id.namespace, 'flux-system');
      expect(id.name, 'my-ks');
    });

    test('returns null on wrong part count', () {
      expect(GitOpsId.tryParse('argocd:my-app'), isNull);
      expect(GitOpsId.tryParse('argocd:argocd:my-app:extra'), isNull);
    });

    test('returns null when tool or name is empty', () {
      expect(GitOpsId.tryParse(':argocd:my-app'), isNull);
      expect(GitOpsId.tryParse('argocd:argocd:'), isNull);
    });

    test('equality holds across re-parses of the same encoded id', () {
      final a = GitOpsId.tryParse('argocd:argocd:my-app');
      final b = GitOpsId.tryParse('argocd:argocd:my-app');
      expect(a, equals(b));
      expect(a.hashCode, equals(b.hashCode));
    });
  });

  group('PolicyId', () {
    test('parses 4-part engine:ns:kind:name', () {
      final id = PolicyId.tryParse('kyverno:app:Pod:disallow-privileged')!;
      expect(id.engine, 'kyverno');
      expect(id.namespace, 'app');
      expect(id.kind, 'Pod');
      expect(id.name, 'disallow-privileged');
    });

    test('accepts empty namespace for cluster-scoped policies', () {
      final id =
          PolicyId.tryParse('kyverno::ClusterPolicy:disallow-root')!;
      expect(id.namespace, isEmpty);
      expect(id.kind, 'ClusterPolicy');
    });

    test('returns null when engine, kind, or name is empty', () {
      expect(PolicyId.tryParse(':app:Pod:foo'), isNull);
      expect(PolicyId.tryParse('kyverno:app::foo'), isNull);
      expect(PolicyId.tryParse('kyverno:app:Pod:'), isNull);
    });

    test('returns null on wrong part count', () {
      expect(PolicyId.tryParse('kyverno:Pod:foo'), isNull);
      expect(PolicyId.tryParse('kyverno:app:Pod:foo:extra'), isNull);
    });
  });

  group('MeshRouteId', () {
    test('parses mesh:ns:kindCode:name', () {
      final id = MeshRouteId.tryParse('istio:bookinfo:vs:reviews')!;
      expect(id.mesh, 'istio');
      expect(id.namespace, 'bookinfo');
      expect(id.kindCode, 'vs');
      expect(id.name, 'reviews');
    });

    test('returns null on any empty part', () {
      expect(MeshRouteId.tryParse(':bookinfo:vs:reviews'), isNull);
      expect(MeshRouteId.tryParse('istio::vs:reviews'), isNull);
      expect(MeshRouteId.tryParse('istio:bookinfo::reviews'), isNull);
      expect(MeshRouteId.tryParse('istio:bookinfo:vs:'), isNull);
    });
  });

  group('encodeIdForPath / decodeIdFromPath', () {
    test('round-trips a colon-bearing GitOpsId encode through both', () {
      final id = GitOpsId(tool: 'argocd', namespace: 'argocd', name: 'my-app');
      final encoded = encodeIdForPath(id.encode());
      // Colons are URL-encoded so go_router path segment matching is
      // safe.
      expect(encoded.contains(':'), isFalse);
      expect(encoded, contains('%3A'));
      final decoded = decodeIdFromPath(encoded);
      expect(decoded, id.encode());
      expect(GitOpsId.tryParse(decoded), id);
    });

    test('round-trips a PolicyId with empty namespace', () {
      const raw = 'kyverno::ClusterPolicy:disallow-root';
      final encoded = encodeIdForPath(raw);
      final decoded = decodeIdFromPath(encoded);
      expect(decoded, raw);
      expect(PolicyId.tryParse(decoded)!.namespace, isEmpty);
    });
  });
}
