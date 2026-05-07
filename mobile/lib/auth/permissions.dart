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
  String namespace,
) {
  if (rbac == null) return true;
  final raw = rbac.raw;

  // Cluster-scoped permissions apply across all namespaces.
  final clusterScoped = raw['clusterScoped'];
  if (clusterScoped is Map) {
    final verbs = clusterScoped[kind];
    if (_verbsAllow(verbs, verb)) return true;
  }

  final namespaces = raw['namespaces'];
  // Empty namespace == "All Namespaces" view: allow if the user has the
  // verb in *any* loaded namespace. Otherwise the All-Namespaces list view
  // would be unable to surface row-level actions for users with per-ns
  // permissions but no cluster-scoped grant.
  if (namespace.isEmpty) {
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
