// Deep-link parser coverage. Custom-scheme + Universal Link variants,
// cluster-scoped vs namespaced, and rejection of malformed inputs.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/notifications/deep_link_handler.dart';

void main() {
  const handler = DeepLinkHandler(
    universalLinkHosts: {'kubecenter.example.com'},
  );

  group('custom scheme k8scenter://', () {
    test('namespaced resource', () {
      final p = handler.parse(
        Uri.parse('k8scenter://cluster/local/Pod/default/web-7d4f-abc'),
      );
      expect(p.isValid, isTrue);
      expect(p.path, '/clusters/local/workloads/pods/default/web-7d4f-abc');
      expect(p.clusterId, 'local');
    });

    test('cluster-scoped resource (no namespace)', () {
      final p = handler.parse(
        Uri.parse('k8scenter://cluster/local/Node/worker-01'),
      );
      expect(p.isValid, isTrue);
      // Cluster-scoped routes to /<section>/<kind>/<name>; for Node that
      // resolves through kindDetailPath to the cluster section.
      expect(p.path, '/clusters/local/cluster/nodes/worker-01');
    });

    test('notifications shortcut', () {
      final p = handler.parse(Uri.parse('k8scenter://notifications'));
      expect(p.isValid, isTrue);
      expect(p.path, '/notifications');
    });

    test('unknown kind falls through to generic detail', () {
      final p = handler.parse(
        Uri.parse('k8scenter://cluster/local/CustomResource/ns/name'),
      );
      expect(p.isValid, isTrue);
      // Generic catch-all under /clusters/<id>/generic/<kind>/<ns>/<name>.
      expect(
        p.path,
        '/clusters/local/generic/customresource/ns/name',
      );
    });
  });

  group('Universal Link https://kubecenter.example.com/m/...', () {
    test('namespaced resource', () {
      final p = handler.parse(
        Uri.parse(
          'https://kubecenter.example.com/m/cluster/local/Pod/default/x',
        ),
      );
      expect(p.isValid, isTrue);
      expect(p.path, '/clusters/local/workloads/pods/default/x');
    });

    test('rejects non-allowlisted host', () {
      final p = handler.parse(
        Uri.parse('https://attacker.com/m/cluster/local/Pod/default/x'),
      );
      expect(p.isValid, isFalse);
    });
  });

  group('rejection cases', () {
    test('http scheme without registered host', () {
      const empty = DeepLinkHandler();
      final p = empty.parse(
        Uri.parse('https://kubecenter.example.com/m/cluster/local/Pod/ns/n'),
      );
      expect(p.isValid, isFalse);
    });

    test('missing required segments', () {
      final p = handler.parse(Uri.parse('k8scenter://cluster/local/Pod'));
      expect(p.isValid, isFalse);
    });

    test('empty path returns invalid', () {
      final p = handler.parse(Uri.parse('k8scenter://'));
      expect(p.isValid, isFalse);
    });
  });
}
