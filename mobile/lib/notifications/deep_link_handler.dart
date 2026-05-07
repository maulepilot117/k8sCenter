// Deep-link parser for k8scenter:// custom-scheme URIs and the future
// Universal Link variant. Resolves to a path the existing go_router
// configuration already serves, so deep-link routing reuses the same
// detail screens the drawer/notification-feed taps use.
//
// Custom scheme:
//   k8scenter://cluster/<clusterId>/<Kind>/<namespace>/<name>
//   k8scenter://cluster/<clusterId>/<Kind>/<name>           — cluster-scoped
//   k8scenter://notifications                               — feed
//
// Universal Link (PR-1g wires the actual domain on the platform side):
//   https://<host>/m/cluster/<clusterId>/<Kind>/<namespace>/<name>
//   https://<host>/m/notifications
//
// The parser is forgiving with kind segments (singular/plural,
// case-insensitive) — kindDetailPath() owns the canonicalization.

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../routing/domain_sections.dart';

/// Result of parsing a candidate deep link. `path` is null when the URI
/// did not match any supported shape; callers should ignore those.
class ParsedDeepLink {
  const ParsedDeepLink._(this.path, this.clusterId);
  final String? path;
  final String? clusterId;

  bool get isValid => path != null;
}

class DeepLinkHandler {
  const DeepLinkHandler({this.universalLinkHosts = const {}});

  /// Hosts that should be treated as Universal Link entry points (e.g.
  /// `kubecenter.example.com`). Empty in M1 — populate from operator
  /// config in PR-1g.
  final Set<String> universalLinkHosts;

  ParsedDeepLink parse(Uri uri) {
    if (uri.scheme == 'k8scenter') {
      return _parseSegments(uri.host, uri.pathSegments);
    }
    if ((uri.scheme == 'https' || uri.scheme == 'http') &&
        universalLinkHosts.contains(uri.host) &&
        uri.pathSegments.isNotEmpty &&
        uri.pathSegments.first == 'm') {
      // Drop the leading 'm' marker; everything else mirrors the
      // custom-scheme shape with the resource-class as host.
      final rest = uri.pathSegments.skip(1).toList();
      if (rest.isEmpty) return const ParsedDeepLink._(null, null);
      return _parseSegments(rest.first, rest.skip(1).toList());
    }
    return const ParsedDeepLink._(null, null);
  }

  ParsedDeepLink _parseSegments(String firstSeg, List<String> rest) {
    switch (firstSeg) {
      case 'notifications':
        return const ParsedDeepLink._('/notifications', null);
      case 'cluster':
        if (rest.length < 3) return const ParsedDeepLink._(null, null);
        final clusterId = rest[0];
        // Canonicalize the Kind to a route segment. Notifications and
        // FCM payloads typically carry the canonical Kubernetes Kind
        // ("Pod", "Ingress"); routes are keyed by lowercase plural
        // ("pods", "ingresses"). Try the segment as-given, then a few
        // pluralizations, against domain_sections' registered kinds.
        final kindRaw = rest[1];
        final kind = _canonicalRouteKind(kindRaw);
        // 3-arg form: cluster/<id>/<Kind>/<name> (cluster-scoped)
        // 4-arg form: cluster/<id>/<Kind>/<namespace>/<name>
        final namespace = rest.length >= 4 ? rest[2] : '';
        final name = rest.length >= 4 ? rest[3] : rest[2];
        if (clusterId.isEmpty || kind.isEmpty || name.isEmpty) {
          return const ParsedDeepLink._(null, null);
        }
        final path = kindDetailPath(
          clusterId: clusterId,
          kind: kind,
          namespace: namespace,
          name: name,
        );
        return ParsedDeepLink._(path, clusterId);
      default:
        return const ParsedDeepLink._(null, null);
    }
  }

  /// Maps a canonical Kubernetes Kind ("Pod", "Ingress", "Node") OR an
  /// already-route-shaped lowercase plural ("pods") to the route
  /// segment that `kindDetailPath` understands. When no specialized
  /// route matches, returns the lowercase input — `kindDetailPath`'s
  /// generic-detail catch-all handles unknown kinds.
  String _canonicalRouteKind(String input) {
    final lower = input.toLowerCase();
    final candidates = [lower, '${lower}s', '${lower}es'];
    for (final c in candidates) {
      if (findDomainSection(c) != null) return c;
    }
    return lower;
  }

  /// Apply the parsed link via go_router. No-op when the link is
  /// malformed; safe to call from FCM/url_launcher callbacks.
  void route(BuildContext context, Uri uri) {
    final parsed = parse(uri);
    if (!parsed.isValid) return;
    context.push(parsed.path!);
  }
}
