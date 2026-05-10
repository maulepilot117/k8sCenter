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
// `encode()` percent-encodes each segment with `Uri.encodeComponent`
// before joining on `:`. `tryParse()` percent-decodes each segment
// after splitting. This makes the encoding self-contained: callers
// can drop the result into a go_router path segment without an
// additional `Uri.encodeComponent` step, and segments that contain a
// literal `:` (Gatekeeper constraint names like `myrule:v2`) round-
// trip cleanly. The runtime contract is enforced by the API rather
// than the call-site documentation.
//
// Empty-namespace policy:
//   - GitOpsId allows an empty namespace segment (Argo CD apps in the
//     argocd namespace and Flux-style cluster-scoped objects).
//   - PolicyId allows an empty namespace (Kyverno ClusterPolicy and
//     Gatekeeper cluster-scoped templates).
//   - MeshRouteId requires all four segments — no Istio/Linkerd
//     routing CRD is cluster-scoped without a namespace.

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
    final decoded = parts.map(Uri.decodeComponent).toList();
    if (decoded[0].isEmpty || decoded[2].isEmpty) return null;
    return GitOpsId(
      tool: decoded[0],
      namespace: decoded[1],
      name: decoded[2],
    );
  }

  String encode() => [tool, namespace, name].map(Uri.encodeComponent).join(':');

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
    final decoded = parts.map(Uri.decodeComponent).toList();
    if (decoded[0].isEmpty || decoded[2].isEmpty || decoded[3].isEmpty) {
      return null;
    }
    return PolicyId(
      engine: decoded[0],
      namespace: decoded[1],
      kind: decoded[2],
      name: decoded[3],
    );
  }

  String encode() =>
      [engine, namespace, kind, name].map(Uri.encodeComponent).join(':');

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
    final decoded = parts.map(Uri.decodeComponent).toList();
    if (decoded[0].isEmpty ||
        decoded[1].isEmpty ||
        decoded[2].isEmpty ||
        decoded[3].isEmpty) {
      return null;
    }
    return MeshRouteId(
      mesh: decoded[0],
      namespace: decoded[1],
      kindCode: decoded[2],
      name: decoded[3],
    );
  }

  String encode() =>
      [mesh, namespace, kindCode, name].map(Uri.encodeComponent).join(':');

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

// go_router path-segment integration: `encode()` already returns a
// URL-safe string (each segment percent-encoded; `:` separators
// preserved). Push the result directly into a path segment and call
// `tryParse` on the receiving side without an additional
// encode/decode round-trip.
