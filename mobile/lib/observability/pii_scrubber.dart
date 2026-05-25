// Sentry `beforeSend` PII scrubber.
//
// Sentry is opt-in (default off) per R5. Even when opted-in, the only data
// the operator should see in our shared crash-reporting project is what
// helps fix bugs — not k8s namespace names, secret names, FCM tokens, or
// user identity. This module is the single point of egress: every event
// passes through [scrubEvent] before leaving the device.
//
// Layered policy (do NOT collapse into a single regex):
//
//   1. user.username / user.email / user.ipAddress: stripped unconditionally.
//      Sentry's `sendDefaultPii: false` covers most of this, but we wipe
//      `event.user` regardless to defend against accidental SDK upgrades.
//   2. Request bodies and breadcrumb URL query parameters: wholesale strip.
//      The k8sCenter API echoes resource names, namespace names, and YAML
//      back to the operator; none of that belongs in a shared crash project.
//   3. Exception messages + breadcrumb messages: positional k8s-path scrub.
//      We replace path segments AFTER known keys (`/v1/resources/<kind>/<ns>/<name>`,
//      `namespace=<ns>`, `name=<name>`) — NOT a generic name regex. A naive
//      `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` matcher over 12-char tokens has too
//      high a false-positive rate on Dart symbols and URL paths AND fails
//      to catch under-12-char k8s resource names (`vault-token`).
//   4. FCM device tokens: replaced via a tight character-class pattern that
//      effectively only matches FCM tokens (100+ base64-url chars).
//   5. Stack frame fields (abs_path, filename, module, function) are NOT
//      scrubbed. They contain Dart source paths, not runtime k8s state, and
//      scrubbing them destroys crash debuggability for no privacy gain.
//
// Drops non-release events entirely (profile + debug) so developer and
// integration sessions don't pollute the shared project.

import 'package:flutter/foundation.dart';
import 'package:sentry_flutter/sentry_flutter.dart';

/// Token used to replace scrubbed k8s identifiers in messages.
const String kScrubbedNamespace = '<namespace>';
const String kScrubbedName = '<name>';
const String kScrubbedToken = '<fcm-token>';

/// Matches FCM registration tokens. Real-world FCM tokens are
/// 140+ chars of `[A-Za-z0-9_:-]`. We use a 100-char floor to avoid
/// matching shorter identifiers that happen to share the alphabet.
final RegExp _fcmTokenPattern = RegExp(r'[A-Za-z0-9_:-]{100,}');

/// Matches `/v1/<some-bucket>/<kind>/<namespace>[/<name>]` and similar
/// paths where the segments after a kind-like token carry resource
/// identifiers. The capture groups isolate the kind segment from the
/// segments to scrub so we only rewrite what's positional, not the
/// surrounding path.
///
/// Anchored on `/v1/`-prefixed paths because that's the k8sCenter API
/// shape; unrelated URL paths (Sentry SDK breadcrumbs, https://, etc.)
/// fall through unchanged. Segment-internal characters are restricted
/// to DNS-1123-label-friendly (`a-z 0-9 . _ -`) plus uppercase so the
/// regex stops at URL delimiters (`:`, `?`, `#`, `,`, `)`) that would
/// otherwise be greedily absorbed by `[^/\s]+`.
final RegExp _k8sPathSegmentPattern = RegExp(
  r'(/v1/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)(?:/([A-Za-z0-9._-]+))?',
);

/// Matches `/v1/diagnostics/<namespace>/<kind>/<name>`. The diagnostics
/// API uses a different shape than the resources API — the namespace is
/// the FIRST segment after the endpoint name, not the third — so it needs
/// its own pattern. Applied before [_k8sPathSegmentPattern] in [_scrubText]
/// to take precedence over the generic shape.
final RegExp _k8sDiagnosticsPathPattern = RegExp(
  r'(/v1/diagnostics)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)',
);

/// Matches WebSocket endpoints that embed `<namespace>/<pod>/<container>`
/// after `/ws/<endpoint>`. Covers both `/ws/logs/...` and `/ws/exec/...`.
/// Replaced with `<namespace>/<pod>/<container>` placeholders, preserving
/// the endpoint name for debuggability.
final RegExp _wsPathPattern = RegExp(
  r'(/ws/[A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)',
);

/// Matches `namespace=<value>` and `name=<value>` key-value pairs in
/// messages. Value is captured as one segment of non-delimiter characters
/// so we don't over-eat surrounding text or trailing brackets/quotes.
/// Case-insensitive because some sources emit `Namespace=`.
final RegExp _k8sKeyValuePattern = RegExp(
  r'\b(namespace|name)=([^,;&\s)\]\}"' "'" r']+)',
  caseSensitive: false,
);

/// Hook used by [SentryFlutter.init]'s `beforeSend` callback. Returns
/// `null` to drop the event entirely (debug mode); returns a sanitized
/// event otherwise.
SentryEvent? scrubEvent(SentryEvent event, Hint hint) {
  // Drop non-release-mode events on the floor. Developer-built APKs and
  // simulator runs share the same DSN as TestFlight builds in dev, so
  // this is what keeps the shared project clean. Profile-mode runs are
  // also dropped — they're used for performance harnesses and shouldn't
  // pollute the shared project either.
  if (!kReleaseMode) return null;

  return _scrubEventBody(event);
}

/// Visible-for-test variant that runs the full scrub regardless of
/// build mode. Tests assert on the scrub behavior; production gating
/// happens in [scrubEvent].
@visibleForTesting
SentryEvent scrubEventForTest(SentryEvent event) => _scrubEventBody(event);

SentryEvent _scrubEventBody(SentryEvent event) {
  // (1) Strip user identity. The Sentry SDK's copyWith uses
  // `value ?? this.value` semantics, so passing null preserves the
  // existing user — we must construct a fresh SentryEvent to clear it.
  //
  // (2) Strip request body + query + cookies; scrub headers.
  // (3) Scrub exception messages (stack frames intentionally preserved).
  // (4) Scrub breadcrumb messages + URLs.
  // (5) Scrub top-level message.
  final origRequest = event.request;
  final scrubbedRequest = origRequest == null
      ? null
      : SentryRequest(
          // Strip query + fragment, then run the path through the same
          // positional scrubber that handles message text and breadcrumb
          // URLs. Without this, SentryRequest.url leaks namespace/name
          // segments and any ?token=… that the request happened to carry,
          // even though the sibling queryString field is nulled below.
          // Audit finding P2-10.
          url: origRequest.url == null ? null : _scrubUrl(origRequest.url!),
          method: origRequest.method,
          // data/queryString/cookies/fragment dropped; SentryRequest
          // constructor accepts null for each, which clears them. The
          // fragment field is a sibling of `url` and can carry the same
          // identifiers, so it must be nulled even when url is scrubbed.
          data: null,
          queryString: null,
          cookies: null,
          fragment: null,
          apiTarget: origRequest.apiTarget,
          headers: _scrubHeaders(origRequest.headers),
          env: origRequest.env,
        );

  final exceptions = event.exceptions?.map(_scrubException).toList();
  final breadcrumbs = event.breadcrumbs?.map(_scrubBreadcrumb).toList();

  final origMessage = event.message;
  final message = origMessage == null
      ? null
      : SentryMessage(
          _scrubText(origMessage.formatted),
          template: origMessage.template == null
              ? null
              : _scrubText(origMessage.template!),
          // SentryMessage.params is List<dynamic>?. Strings need scrubbing;
          // numeric/bool params pass through.
          params: _scrubMessageParams(origMessage.params),
        );

  // SentryEvent.contexts is a typed `Contexts` object (device, app,
  // runtime, OS, gpu, browser, response, etc.) — almost all platform
  // metadata, not user PII. Sentry's `sendDefaultPii: false` already
  // strips the IP-bearing fields. Scrubbing the typed sub-fields would
  // require constructing each Contexts.* component by hand for negligible
  // privacy gain, so we pass contexts through unchanged. Revisit if a
  // future SDK upgrade starts carrying request bodies under
  // Contexts.response — see OBSERVABILITY.md.
  return SentryEvent(
    eventId: event.eventId,
    timestamp: event.timestamp,
    platform: event.platform,
    logger: event.logger,
    serverName: event.serverName,
    release: event.release,
    dist: event.dist,
    environment: event.environment,
    modules: event.modules,
    message: message,
    transaction:
        event.transaction == null ? null : _scrubText(event.transaction!),
    throwable: event.throwableMechanism,
    level: event.level,
    culprit: event.culprit == null ? null : _scrubText(event.culprit!),
    // user intentionally omitted (defaults to null).
    tags: _scrubTags(event.tags),
    // ignore: deprecated_member_use
    extra: _scrubExtra(event.extra),
    fingerprint:
        event.fingerprint?.map(_scrubText).toList(growable: false),
    contexts: event.contexts,
    breadcrumbs: breadcrumbs,
    sdk: event.sdk,
    request: scrubbedRequest,
    debugMeta: event.debugMeta,
    exceptions: exceptions,
    threads: event.threads,
    type: event.type,
  );
}

Map<String, String>? _scrubTags(Map<String, String>? tags) {
  if (tags == null) return null;
  return tags.map((k, v) => MapEntry(k, _scrubText(v)));
}

Map<String, dynamic>? _scrubExtra(Map<String, dynamic>? extra) {
  if (extra == null) return null;
  final clean = <String, dynamic>{};
  extra.forEach((key, value) {
    if (value is String) {
      clean[key] = _scrubText(value);
    } else {
      // Non-string payloads (numbers, bools, maps) pass through. Maps of
      // strings would also benefit from scrubbing, but `extra` is
      // deprecated SDK-side and we don't generate it ourselves.
      clean[key] = value;
    }
  });
  return clean;
}

List<dynamic>? _scrubMessageParams(List<dynamic>? params) {
  if (params == null) return null;
  return params
      .map<dynamic>((p) => p is String ? _scrubText(p) : p)
      .toList(growable: false);
}

Map<String, String>? _scrubHeaders(Map<String, String>? headers) {
  if (headers == null) return null;
  // Authorization / cookies / X-CSRF-Token might leak session credentials.
  // Drop them; we don't need them to debug a crash.
  final clean = <String, String>{};
  headers.forEach((key, value) {
    final lower = key.toLowerCase();
    if (lower == 'authorization' ||
        lower == 'proxy-authorization' ||
        lower == 'cookie' ||
        lower == 'set-cookie' ||
        lower.startsWith('x-csrf') ||
        lower == 'x-requested-with' ||
        lower == 'x-cluster-id') {
      return;
    }
    clean[key] = value;
  });
  return clean;
}

SentryException _scrubException(SentryException exc) {
  return exc.copyWith(
    value: exc.value == null ? null : _scrubText(exc.value!),
    // Stack frames are intentionally NOT scrubbed — see header doc.
  );
}

Breadcrumb _scrubBreadcrumb(Breadcrumb crumb) {
  return crumb.copyWith(
    message: crumb.message == null ? null : _scrubText(crumb.message!),
    data: _scrubBreadcrumbData(crumb.data),
  );
}

Map<String, dynamic>? _scrubBreadcrumbData(Map<String, dynamic>? data) {
  if (data == null) return null;
  final clean = <String, dynamic>{};
  data.forEach((key, value) {
    // HTTP breadcrumbs carry the URL under `url`. Shares the same
    // strip-and-scrub treatment as SentryRequest.url so the two surfaces
    // can't diverge.
    if (key == 'url' && value is String) {
      clean[key] = _scrubUrl(value);
      return;
    }
    if (value is String) {
      clean[key] = _scrubText(value);
    } else {
      // Non-string payloads (status_code, method, etc.) pass through.
      clean[key] = value;
    }
  });
  return clean;
}

/// Strips `?query` and `#fragment` from [url], then runs the bare path
/// through [_scrubText]. We slice off `?`/`#` rather than calling
/// `Uri.replace(query: '')` so the output is canonical (no dangling `?`
/// from an empty query string) and stays a valid URL when the path
/// scrubber rewrites segments. Single source of truth for URL scrub:
/// SentryRequest.url and HTTP breadcrumb `url` both pass through here.
///
/// Percent-decodes via `Uri.decodeFull` BEFORE slicing so an attacker
/// can't smuggle a fake query string into the path with `%3F` or `%23`.
/// Without the decode pass, `/v1/resources/secrets/ns/name%3Ftoken=leak`
/// would land in `_scrubText` unchanged — the regex char class
/// `[A-Za-z0-9._-]` stops at `%`, so the segment scrub catches only
/// `.../ns/name`, and `%3Ftoken=leak` survives in the output. Code-
/// review finding (adversarial ADV-3 + testing T-2, Phase 5).
String _scrubUrl(String url) {
  String decoded;
  try {
    decoded = Uri.decodeFull(url);
  } catch (_) {
    // Malformed percent-encoding (e.g., bare `%` not followed by hex).
    // Fall back to the raw string — better to scrub literal text than
    // to skip scrubbing entirely.
    decoded = url;
  }
  var stripped = decoded;
  final qIdx = stripped.indexOf('?');
  if (qIdx >= 0) stripped = stripped.substring(0, qIdx);
  final fIdx = stripped.indexOf('#');
  if (fIdx >= 0) stripped = stripped.substring(0, fIdx);
  return _scrubText(stripped);
}

/// Public for tests. Applies positional k8s-path + key-value + FCM
/// scrubbing to a free-text string.
@visibleForTesting
String scrubText(String input) => _scrubText(input);

String _scrubText(String input) {
  var out = input;

  // FCM tokens first — the alphabet overlaps base64 and the long-length
  // floor (100+ chars) means we won't accidentally scrub Dart symbols
  // or URL fragments.
  out = out.replaceAll(_fcmTokenPattern, kScrubbedToken);

  // Diagnostics paths first — their `/v1/diagnostics/<ns>/<kind>/<name>`
  // shape differs from the resources API (`/v1/<bucket>/<kind>/<ns>...`)
  // so we apply the diagnostics pattern BEFORE the generic resources
  // pattern. Otherwise the generic regex would absorb it and treat the
  // namespace as a kind.
  out = out.replaceAllMapped(_k8sDiagnosticsPathPattern, (m) {
    final prefix = m.group(1)!; // "/v1/diagnostics"
    return '$prefix/$kScrubbedNamespace/<kind>/$kScrubbedName';
  });

  // WebSocket endpoints embedding namespace/pod/container after the
  // endpoint name. Covers `/ws/logs/<ns>/<pod>/<container>` and
  // `/ws/exec/<ns>/<pod>/<container>`. Other `/ws/...` shapes
  // (notifications, alerts) lack the trailing tuple so they pass through.
  out = out.replaceAllMapped(_wsPathPattern, (m) {
    final endpoint = m.group(1)!; // "/ws/logs" or "/ws/exec"
    return '$endpoint/$kScrubbedNamespace/<pod>/<container>';
  });

  // Positional k8s path segments — `/v1/<bucket>/<kind>/<ns>[/<name>]`.
  // `m.group(2)` is the namespace segment (always present per the regex);
  // `m.group(3)` is the optional name segment.
  out = out.replaceAllMapped(_k8sPathSegmentPattern, (m) {
    final prefix = m.group(1)!; // "/v1/resources/secrets"
    final name = m.group(3);
    if (name == null) {
      return '$prefix/$kScrubbedNamespace';
    }
    return '$prefix/$kScrubbedNamespace/$kScrubbedName';
  });

  // Key-value pairs (`namespace=foo`, `name=bar`).
  out = out.replaceAllMapped(_k8sKeyValuePattern, (m) {
    final key = m.group(1)!.toLowerCase();
    final replacement = key == 'namespace' ? kScrubbedNamespace : kScrubbedName;
    return '${m.group(1)}=$replacement';
  });

  return out;
}
