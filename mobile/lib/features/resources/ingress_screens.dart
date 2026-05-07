// Ingress list + detail. Renders the host → backend rule list and the
// TLS section (only when configured). Surfaces the loadBalancer status
// IP/hostname so operators can confirm the controller has wired up DNS
// without flipping to a kubectl shell.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _IngressRow {
  _IngressRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get className =>
      readPath(raw, 'spec.ingressClassName') as String? ?? '<default>';

  List<_IngressRule> get rules {
    final raw = (readPath(this.raw, 'spec.rules') as List?) ?? const [];
    return [for (final r in raw) if (r is Map) _IngressRule.fromMap(r)];
  }

  List<_IngressTls> get tls {
    final raw = (readPath(this.raw, 'spec.tls') as List?) ?? const [];
    return [for (final t in raw) if (t is Map) _IngressTls.fromMap(t)];
  }

  String get hosts {
    final hs = rules.map((r) => r.host).where((h) => h.isNotEmpty).toSet();
    if (hs.isEmpty) return '<wildcard>';
    return hs.join(', ');
  }

  /// Best-effort externally-visible address: tries loadBalancer ingress
  /// IP first, then hostname. Returns null when the controller hasn't
  /// updated status yet (common during first reconcile). UI uses
  /// [hasAddress] to drive status, [address] only for display.
  String? get addressOrNull {
    final ing = (readPath(raw, 'status.loadBalancer.ingress') as List?) ??
        const [];
    for (final i in ing) {
      if (i is Map) {
        final ip = i['ip'] as String?;
        if (ip != null && ip.isNotEmpty) return ip;
        final host = i['hostname'] as String?;
        if (host != null && host.isNotEmpty) return host;
      }
    }
    return null;
  }

  bool get hasAddress => addressOrNull != null;
  String get address => addressOrNull ?? '—';
}

class _IngressRule {
  const _IngressRule({required this.host, required this.paths});
  factory _IngressRule.fromMap(Map<dynamic, dynamic> m) {
    final host = m['host'] as String? ?? '';
    final http = m['http'] as Map?;
    final paths = (http?['paths'] as List?) ?? const [];
    final out = <_IngressPath>[];
    for (final p in paths) {
      if (p is Map) out.add(_IngressPath.fromMap(p));
    }
    return _IngressRule(host: host, paths: out);
  }
  final String host;
  final List<_IngressPath> paths;
}

class _IngressPath {
  const _IngressPath({
    required this.path,
    required this.pathType,
    required this.serviceName,
    required this.servicePort,
  });
  factory _IngressPath.fromMap(Map<dynamic, dynamic> m) {
    final backend = m['backend'] as Map?;
    final svc = backend?['service'] as Map?;
    final port = svc?['port'] as Map?;
    final p = (port?['number'] as num?)?.toInt();
    return _IngressPath(
      path: m['path'] as String? ?? '/',
      pathType: m['pathType'] as String? ?? 'ImplementationSpecific',
      serviceName: svc?['name'] as String? ?? '—',
      servicePort: p != null ? '$p' : (port?['name'] as String? ?? '—'),
    );
  }
  final String path;
  final String pathType;
  final String serviceName;
  final String servicePort;
}

class _IngressTls {
  const _IngressTls({required this.hosts, required this.secretName});
  factory _IngressTls.fromMap(Map<dynamic, dynamic> m) {
    final hosts = (m['hosts'] as List?) ?? const [];
    return _IngressTls(
      hosts: [for (final h in hosts) if (h is String) h],
      secretName: m['secretName'] as String? ?? '—',
    );
  }
  final List<String> hosts;
  final String secretName;
}

class IngressListScreen extends ConsumerWidget {
  const IngressListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
            namespace == null ? 'Ingresses' : 'Ingresses · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'ingresses',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_IngressRow.new).toList();
          return ResourceTable<_IngressRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(label: 'Class', value: (r) => r.className),
              ResourceColumn(label: 'Hosts', value: (r) => r.hosts),
              ResourceColumn(label: 'Address', value: (r) => r.address),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'ingresses',
                namespace: r.meta.namespace,
                name: r.meta.name,
              ),
            ),
          );
        },
      ),
    );
  }
}

class IngressDetailScreen extends ConsumerWidget {
  const IngressDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'ingresses',
      namespace: namespace,
      name: name,
    );
    final get = ref.watch(resourceGetProvider(getKey));
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
      ),
      data: (raw) {
        final ing = _IngressRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'Ingress',
          name: ing.meta.name,
          namespace: ing.meta.namespace,
          uid: ing.meta.uid,
          icon: Icons.alt_route_outlined,
          statusLabel: ing.hasAddress ? 'Ready' : 'Pending',
          statusColor: ing.hasAddress ? colors.success : colors.warning,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'ingresses',
            namespace: ing.meta.namespace,
            name: ing.meta.name,
            resource: raw,
          ),
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'OVERVIEW',
                child: Column(
                  children: [
                    DetailRow(label: 'Class', value: ing.className),
                    DetailRow(label: 'Address', value: ing.address),
                  ],
                ),
              ),
              DetailSection(
                title: 'RULES',
                child: ing.rules.isEmpty
                    ? Text('No rules',
                        style: TextStyle(color: colors.textMuted))
                    : Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final r in ing.rules)
                            _RuleBlock(rule: r),
                        ],
                      ),
              ),
              // TLS section is hidden entirely when not configured —
              // showing an empty section just adds noise.
              if (ing.tls.isNotEmpty)
                DetailSection(
                  title: 'TLS',
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      for (final t in ing.tls)
                        DetailRow(
                          label: t.secretName,
                          value: t.hosts.isEmpty
                              ? '<all hosts>'
                              : t.hosts.join(', '),
                        ),
                    ],
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}

class _RuleBlock extends StatelessWidget {
  const _RuleBlock({required this.rule});
  final _IngressRule rule;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            rule.host.isEmpty ? '<wildcard>' : rule.host,
            style: TextStyle(
              color: colors.textPrimary,
              fontWeight: FontWeight.w500,
              fontSize: 13,
            ),
          ),
          for (final p in rule.paths)
            Padding(
              padding: const EdgeInsets.only(left: 12, top: 4),
              child: Text(
                '${p.pathType} ${p.path} → ${p.serviceName}:${p.servicePort}',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                  fontFamily: 'monospace',
                ),
              ),
            ),
        ],
      ),
    );
  }
}
