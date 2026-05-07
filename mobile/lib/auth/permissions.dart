// Permission predicate — Dart port of `frontend/lib/permissions.ts:canPerform`.
//
// UX optimization only — the backend re-checks every write. When [rbac] is
// null (RBAC not yet loaded), returns true so the UI doesn't lock out
// optimistically before /v1/auth/me responds.
//
// RBACSummary shape (from `backend/internal/auth/rbac.go:24`):
//   {
//     "clusterScoped": { "<kind>": ["<verb>", ...] },
//     "namespaces":    { "<ns>": { "<kind>": ["<verb>", ...] } }
//   }
// Mobile holds this opaquely as `RBACSummary.raw` (Map<String,dynamic>).

import 'user.dart';

bool canPerform(
  RBACSummary? rbac,
  String kind,
  String verb,
  String namespace, {
  /// When true, an empty [namespace] means "All-Namespaces list view" and
  /// the predicate returns true if the verb is granted in *any* loaded
  /// namespace. When false (the default), an empty [namespace] is
  /// interpreted strictly as "cluster-scoped resource" and only
  /// `clusterScoped` grants apply. Detail-view callers (like
  /// ResourceActionsButton) should leave this false; list-view callers
  /// (ResourceTable's All-Namespaces kebab) should set it true.
  bool allowAnyNamespaceFallback = false,
}) {
  if (rbac == null) return true;
  final raw = rbac.raw;

  // Cluster-scoped permissions apply across all namespaces.
  final clusterScoped = raw['clusterScoped'];
  if (clusterScoped is Map) {
    final verbs = clusterScoped[kind];
    if (_verbsAllow(verbs, verb)) return true;
  }

  final namespaces = raw['namespaces'];
  if (namespace.isEmpty) {
    if (!allowAnyNamespaceFallback) return false;
    if (namespaces is Map) {
      for (final nsPerms in namespaces.values) {
        if (nsPerms is Map) {
          final verbs = nsPerms[kind];
          if (_verbsAllow(verbs, verb)) return true;
        }
      }
    }
    return false;
  }

  if (namespaces is Map) {
    final nsPerms = namespaces[namespace];
    if (nsPerms is Map) {
      final verbs = nsPerms[kind];
      if (_verbsAllow(verbs, verb)) return true;
    }
  }

  return false;
}

bool _verbsAllow(Object? verbs, String verb) {
  if (verbs is! List) return false;
  for (final v in verbs) {
    if (v is String && (v == verb || v == '*')) return true;
  }
  return false;
}
