// Composite-ID helpers for backend resources whose canonical
// identifier is a colon-delimited tuple. Three schemes are in use
// across the M4 read surfaces:
//
//   GitOps applications:  "tool:namespace:name"        (3 parts)
//   Policy normalized:    "engine:namespace:kind:name" (4 parts; namespace may be empty for cluster-scoped policies)
//   Service mesh routing: "mesh:namespace:kindCode:name" (4 parts)
//
// These IDs are emitted by the backend. The mobile app receives them
// as opaque strings on list endpoints, encodes them into go_router
// path segments for detail-screen deep-links, and decodes them on
// detail-screen mount.
//
// The encode step exists because go_router segment matching mishandles
// raw colons in some configurations — `Uri.encodeComponent` is the
// safe transport. Decode reverses the encoding before split.

class GitOpsId {
  const GitOpsId({
    required this.tool,
    required this.namespace,
    required this.name,
  });

  /// "argocd" | "fluxcd" | "flux-ks" | "flux-hr" — the backend's
  /// own discriminator. Mobile passes through opaquely.
  final String tool;
  final String namespace;
  final String name;

  /// Parses `tool:namespace:name`. Returns `null` for malformed input
  /// rather than throwing — the caller decides whether to surface a
  /// 404 screen or a generic error.
  static GitOpsId? tryParse(String raw) {
    final parts = raw.split(':');
    if (parts.length != 3) return null;
    if (parts[0].isEmpty || parts[2].isEmpty) return null;
    return GitOpsId(
      tool: parts[0],
      namespace: parts[1],
      name: parts[2],
    );
  }

  String encode() => '$tool:$namespace:$name';

  @override
  String toString() => encode();

  @override
  bool operator ==(Object other) =>
      other is GitOpsId &&
      other.tool == tool &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(tool, namespace, name);
}

class PolicyId {
  const PolicyId({
    required this.engine,
    required this.namespace,
    required this.kind,
    required this.name,
  });

  /// "kyverno" | "gatekeeper".
  final String engine;

  /// Empty for cluster-scoped policies (e.g. Kyverno ClusterPolicy).
  final String namespace;
  final String kind;
  final String name;

  static PolicyId? tryParse(String raw) {
    final parts = raw.split(':');
    if (parts.length != 4) return null;
    if (parts[0].isEmpty || parts[2].isEmpty || parts[3].isEmpty) return null;
    return PolicyId(
      engine: parts[0],
      namespace: parts[1],
      kind: parts[2],
      name: parts[3],
    );
  }

  String encode() => '$engine:$namespace:$kind:$name';

  @override
  String toString() => encode();

  @override
  bool operator ==(Object other) =>
      other is PolicyId &&
      other.engine == engine &&
      other.namespace == namespace &&
      other.kind == kind &&
      other.name == name;

  @override
  int get hashCode => Object.hash(engine, namespace, kind, name);
}

class MeshRouteId {
  const MeshRouteId({
    required this.mesh,
    required this.namespace,
    required this.kindCode,
    required this.name,
  });

  /// "istio" | "linkerd".
  final String mesh;
  final String namespace;

  /// Backend short-code for the routing CRD kind (e.g. "vs" for
  /// VirtualService). Mobile passes through opaquely.
  final String kindCode;
  final String name;

  static MeshRouteId? tryParse(String raw) {
    final parts = raw.split(':');
    if (parts.length != 4) return null;
    if (parts[0].isEmpty ||
        parts[1].isEmpty ||
        parts[2].isEmpty ||
        parts[3].isEmpty) {
      return null;
    }
    return MeshRouteId(
      mesh: parts[0],
      namespace: parts[1],
      kindCode: parts[2],
      name: parts[3],
    );
  }

  String encode() => '$mesh:$namespace:$kindCode:$name';

  @override
  String toString() => encode();

  @override
  bool operator ==(Object other) =>
      other is MeshRouteId &&
      other.mesh == mesh &&
      other.namespace == namespace &&
      other.kindCode == kindCode &&
      other.name == name;

  @override
  int get hashCode => Object.hash(mesh, namespace, kindCode, name);
}

/// URL-encode a composite ID for safe go_router path embedding.
/// Wraps `Uri.encodeComponent` so callers don't have to import
/// `dart:core`'s URI helpers directly.
String encodeIdForPath(String compositeId) =>
    Uri.encodeComponent(compositeId);

/// Reverse of [encodeIdForPath]. Use on the receiving side of a
/// go_router `:id` parameter before passing to a `tryParse` method.
String decodeIdFromPath(String encoded) => Uri.decodeComponent(encoded);
