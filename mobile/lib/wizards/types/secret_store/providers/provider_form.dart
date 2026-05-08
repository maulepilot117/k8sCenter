// Common contract shared by every per-provider form on the SecretStore
// wizard. Each provider form is a stateless widget that:
//   * Reads the current `providerSpec` map (untyped JSON) from props
//   * Reads server-side validation errors keyed by field path
//   * Emits the next spec via `onUpdateSpec` (whole-map replace, since
//     the spec is shallow and per-provider forms own its shape)
//
// Mirrors `frontend/lib/eso-types.ts`'s `ProviderFormProps`. The
// untyped map keeps the controller agnostic — switching providers
// resets the spec and the next form picks up `{}` cleanly.

import 'package:flutter/widgets.dart';

class ProviderFormProps {
  const ProviderFormProps({
    required this.spec,
    required this.errors,
    required this.onUpdateSpec,
  });

  /// Current provider-specific spec map. Untyped because it varies per
  /// provider. Per-provider form code is the local owner of the shape.
  final Map<String, dynamic> spec;

  /// Server-side validation errors. Keys are field paths
  /// (`auth.token.tokenSecretRef.name`, `server`, `region`, etc.) —
  /// per-provider validators emit them bare (not prefixed with
  /// `providerSpec.`), so per-provider forms read them by their own
  /// natural keys.
  final Map<String, String> errors;

  /// Replace the whole spec map. Provider forms compute the next state
  /// (via shallow copies of nested maps when needed) and emit it back
  /// as a single `onUpdateSpec` call.
  final ValueChanged<Map<String, dynamic>> onUpdateSpec;

  /// Convenience: read a string field at a top-level key, or empty.
  String getString(String key) {
    final v = spec[key];
    return v is String ? v : '';
  }

  /// Convenience: read a nested map at a top-level key, or empty.
  Map<String, dynamic> getMap(String key) {
    final v = spec[key];
    return v is Map<String, dynamic> ? v : <String, dynamic>{};
  }

  /// Convenience: replace a top-level field. Removes the key when
  /// [value] is empty so the preview body doesn't carry empty strings
  /// the backend would treat as "set" but invalid.
  void patchTop(String key, String value) {
    final next = Map<String, dynamic>.from(spec);
    if (value.isEmpty) {
      next.remove(key);
    } else {
      next[key] = value;
    }
    onUpdateSpec(next);
  }

  /// Convenience: replace a nested map at a top-level key.
  void patchMap(String key, Map<String, dynamic> value) {
    final next = Map<String, dynamic>.from(spec);
    next[key] = value;
    onUpdateSpec(next);
  }
}

/// A widget builder that produces a per-provider form when given the
/// shared props. Each provider's form module exports one of these.
typedef ProviderFormBuilder = Widget Function(ProviderFormProps props);
