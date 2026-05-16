// Persistent storage for in-flight OIDC flow state.
//
// Why secure_storage, not in-memory: Android can kill the app process
// while flutter_custom_tabs is in the foreground (low-memory, "Don't
// keep activities" developer mode, background reclaim). When the
// Universal Link redirect arrives, the app cold-starts, the Riverpod
// ProviderContainer is fresh, and OIDCController initializes empty.
// Without persistence, the CSRF check fails (state==null) for every
// cold-start re-entry — invisible on simulator/emulator and broken in
// production on older Android devices.
//
// TTL: 5 minutes. Beyond that, the flow is considered expired and the
// callback is rejected. The IdP's own authorization-code TTL is
// typically shorter (60–600s), so this cap is more about garbage
// collection than security — but it bounds the window where stale
// pending state can collide with a fresh flow.

import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// secure_storage key namespace. Single JSON-encoded blob is cheaper
/// (one platform-channel round-trip per read) than four separate keys.
const String pendingOidcStorageKey = 'kc_oidc_pending_v1';

/// Maximum age of a pending flow before it's considered expired.
const Duration pendingOidcTtl = Duration(minutes: 5);

/// Snapshot of the state required to complete an OIDC flow after a
/// cold-start re-entry. All fields are required — partial state is
/// always an error.
class PendingOidc {
  const PendingOidc({
    required this.providerID,
    required this.state,
    required this.codeVerifier,
    required this.nonce,
    required this.createdAtMillis,
  });

  factory PendingOidc.fromJson(Map<String, dynamic> json) {
    return PendingOidc(
      providerID: json['providerID'] as String? ?? '',
      state: json['state'] as String? ?? '',
      codeVerifier: json['codeVerifier'] as String? ?? '',
      nonce: json['nonce'] as String? ?? '',
      createdAtMillis: (json['createdAtMillis'] as num?)?.toInt() ?? 0,
    );
  }

  final String providerID;
  final String state;
  final String codeVerifier;
  final String nonce;
  final int createdAtMillis;

  Map<String, dynamic> toJson() => {
        'providerID': providerID,
        'state': state,
        'codeVerifier': codeVerifier,
        'nonce': nonce,
        'createdAtMillis': createdAtMillis,
      };

  bool get isComplete =>
      providerID.isNotEmpty &&
      state.isNotEmpty &&
      codeVerifier.isNotEmpty &&
      nonce.isNotEmpty &&
      createdAtMillis > 0;

  bool isExpired(DateTime now) {
    final created = DateTime.fromMillisecondsSinceEpoch(createdAtMillis);
    return now.difference(created) > pendingOidcTtl;
  }
}

/// Persistence surface for in-flight OIDC state. Tests substitute the
/// in-memory implementation.
abstract class PendingOidcStore {
  Future<PendingOidc?> read();
  Future<void> write(PendingOidc pending);
  Future<void> clear();
}

class FlutterSecurePendingOidcStore implements PendingOidcStore {
  FlutterSecurePendingOidcStore({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  final FlutterSecureStorage _storage;

  @override
  Future<PendingOidc?> read() async {
    final raw = await _storage.read(key: pendingOidcStorageKey);
    if (raw == null || raw.isEmpty) return null;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, dynamic>) return null;
      final pending = PendingOidc.fromJson(decoded);
      return pending.isComplete ? pending : null;
    } on FormatException {
      // Corrupted blob (rare; would only happen if some other code wrote
      // garbage to the same key). Treat as no pending state — the
      // controller will surface a "state mismatch" error on the next
      // callback, which is the right UX.
      return null;
    }
  }

  @override
  Future<void> write(PendingOidc pending) async {
    await _storage.write(
      key: pendingOidcStorageKey,
      value: jsonEncode(pending.toJson()),
    );
  }

  @override
  Future<void> clear() async {
    await _storage.delete(key: pendingOidcStorageKey);
  }
}

/// Test-only implementation. Visible behaviour matches the secure-storage
/// variant; survives across awaits within a single test, resets on each
/// new instance.
class InMemoryPendingOidcStore implements PendingOidcStore {
  PendingOidc? _value;

  @override
  Future<PendingOidc?> read() async => _value;

  @override
  Future<void> write(PendingOidc pending) async {
    _value = pending;
  }

  @override
  Future<void> clear() async {
    _value = null;
  }
}

final pendingOidcStoreProvider = Provider<PendingOidcStore>((ref) {
  return FlutterSecurePendingOidcStore();
});
