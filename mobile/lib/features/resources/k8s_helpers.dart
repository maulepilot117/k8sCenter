// Shared helpers for extracting common Kubernetes fields from the
// unstructured resource maps the backend returns. Keeps the per-kind
// list/detail screens thin and uniform.

class K8sMeta {
  const K8sMeta({
    required this.name,
    required this.namespace,
    required this.creationTimestamp,
    this.uid = '',
    this.labels = const {},
    this.annotations = const {},
  });

  factory K8sMeta.from(Map<String, dynamic> resource) {
    final m = resource['metadata'] as Map<String, dynamic>? ?? const {};
    final labels = m['labels'] as Map<String, dynamic>? ?? const {};
    final annotations =
        m['annotations'] as Map<String, dynamic>? ?? const {};
    return K8sMeta(
      name: m['name'] as String? ?? '',
      namespace: m['namespace'] as String? ?? '',
      uid: m['uid'] as String? ?? '',
      creationTimestamp: m['creationTimestamp'] as String? ?? '',
      labels: labels.map((k, v) => MapEntry(k, '$v')),
      annotations: annotations.map((k, v) => MapEntry(k, '$v')),
    );
  }

  final String name;
  final String namespace;
  final String uid;
  final String creationTimestamp;
  final Map<String, String> labels;
  final Map<String, String> annotations;
}

/// Compact human-friendly age from a creationTimestamp like
/// "2025-04-29T10:03:19Z". Returns "—" for missing/invalid input.
String formatAge(String creationTimestamp) {
  if (creationTimestamp.isEmpty) return '—';
  final parsed = DateTime.tryParse(creationTimestamp);
  if (parsed == null) return '—';
  final delta = DateTime.now().toUtc().difference(parsed.toUtc());
  if (delta.isNegative) return '0s';
  if (delta.inDays >= 365) return '${(delta.inDays / 365).floor()}y';
  if (delta.inDays >= 30) return '${(delta.inDays / 30).floor()}mo';
  if (delta.inDays >= 1) return '${delta.inDays}d';
  if (delta.inHours >= 1) return '${delta.inHours}h';
  if (delta.inMinutes >= 1) return '${delta.inMinutes}m';
  return '${delta.inSeconds}s';
}

/// Joins a label/annotation map into a single line for tooltip-style render.
String joinMap(Map<String, String> m, {int maxEntries = 5}) {
  if (m.isEmpty) return '—';
  final entries = m.entries.take(maxEntries).map((e) => '${e.key}=${e.value}');
  final extra = m.length > maxEntries ? ' +${m.length - maxEntries} more' : '';
  return '${entries.join(', ')}$extra';
}

/// Reads a nested value from the resource map by dotted path (e.g.,
/// 'spec.replicas'). Returns null when any segment is missing.
Object? readPath(Map<String, dynamic> resource, String path) {
  Object? cur = resource;
  for (final segment in path.split('.')) {
    if (cur is! Map) return null;
    cur = cur[segment];
  }
  return cur;
}
