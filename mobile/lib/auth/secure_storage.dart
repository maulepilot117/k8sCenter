// Refresh-token persistence via flutter_secure_storage (iOS Keychain /
// Android EncryptedSharedPreferences). Single key. Riverpod-injected so
// tests can substitute an in-memory implementation.

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// secure_storage namespace for the refresh token.
///
/// **No `_v1` suffix** — intentional, see issue #281. The refresh token
/// is an opaque server-issued string with no parseable structure; its
/// shape cannot evolve in a way that breaks an older mobile client.
/// `kc_oidc_pending_v1` (pending_oidc_store.dart) carries a `_v1`
/// suffix because that key stores a versioned JSON blob whose schema
/// may change as the OIDC flow evolves. Asymmetric naming is
/// deliberate: version keys whose contents may evolve; don't version
/// keys whose contents are opaque to the client.
const String refreshTokenKey = 'kc_refresh_token';

abstract class SecureTokenStore {
  Future<String?> readRefreshToken();
  Future<void> writeRefreshToken(String token);
  Future<void> deleteRefreshToken();
}

/// Default-options FlutterSecureStorage instance held module-wide so the
/// "legacy" backend pointer is identity-stable across reads — matters
/// only on iOS where reads with default options can hit the same
/// underlying Keychain item written before P3-7.
const FlutterSecureStorage _legacyDefaults = FlutterSecureStorage();

/// Production backend mirrors the explicit options from
/// [FlutterSecurePendingOidcStore]:
///   - iOS: first_unlock_this_device — readable after first device
///     unlock, not synced to iCloud, not restored from backups. The
///     refresh token is a per-device credential; cross-device sync
///     would defeat session-scoped revocation on the issuing device.
///   - Android: encryptedSharedPreferences — uses the Jetpack
///     EncryptedSharedPreferences backend (AES-GCM-256 with key
///     material in the Android Keystore) instead of the default
///     per-value AES wrapper.
///
/// Audit finding P3-7.
FlutterSecureStorage _defaultCurrentStorage() => const FlutterSecureStorage(
      iOptions: IOSOptions(
        accessibility: KeychainAccessibility.first_unlock_this_device,
      ),
      aOptions: AndroidOptions(encryptedSharedPreferences: true),
    );

class FlutterSecureTokenStore implements SecureTokenStore {
  /// Production constructor: wires the audit-recommended explicit
  /// storage backend AND a default-options legacy backend so values
  /// written by older app versions migrate forward transparently on
  /// first read after upgrade.
  FlutterSecureTokenStore({
    FlutterSecureStorage? storage,
    FlutterSecureStorage? legacyStorage,
  })  : _current = _FlutterBacked(storage ?? _defaultCurrentStorage()),
        _legacy = _FlutterBacked(legacyStorage ?? _legacyDefaults);

  /// Test seam — accepts arbitrary [SecureTokenStore] backends so
  /// migration logic can be exercised without spinning up a real
  /// FlutterSecureStorage method channel. The production constructor
  /// builds these from concrete FlutterSecureStorage instances.
  @visibleForTesting
  FlutterSecureTokenStore.fromBackends({
    required SecureTokenStore current,
    required SecureTokenStore legacy,
  })  : _current = current,
        _legacy = legacy;

  final SecureTokenStore _current;
  final SecureTokenStore _legacy;

  @override
  Future<String?> readRefreshToken() async {
    final current = await _current.readRefreshToken();
    if (current != null) return current;

    // Forward-migrate any value persisted under the pre-P3-7 default
    // options. Best-effort: if the write to the new backend fails, leave
    // the legacy entry intact so the next read tries again rather than
    // losing the user's session.
    final legacy = await _legacy.readRefreshToken();
    if (legacy != null) {
      try {
        await _current.writeRefreshToken(legacy);
        await _legacy.deleteRefreshToken();
      } catch (_) {
        // Swallow migration failures; the legacy value is still
        // returned below so bootstrap can refresh.
      }
    }
    return legacy;
  }

  @override
  Future<void> writeRefreshToken(String token) =>
      _current.writeRefreshToken(token);

  @override
  Future<void> deleteRefreshToken() async {
    // Clear from both backends. Without the legacy clear, a logout that
    // happens before the first post-upgrade read would leave a stale
    // refresh token in default-options storage where a future read
    // would re-migrate it forward.
    await _current.deleteRefreshToken();
    await _legacy.deleteRefreshToken();
  }
}

/// Adapter that exposes a single [FlutterSecureStorage] under the
/// [SecureTokenStore] interface so [FlutterSecureTokenStore] can hold
/// either real or in-memory backends through one shape. Private — the
/// public seam is [FlutterSecureTokenStore.fromBackends].
class _FlutterBacked implements SecureTokenStore {
  const _FlutterBacked(this._storage);
  final FlutterSecureStorage _storage;

  @override
  Future<String?> readRefreshToken() => _storage.read(key: refreshTokenKey);

  @override
  Future<void> writeRefreshToken(String token) =>
      _storage.write(key: refreshTokenKey, value: token);

  @override
  Future<void> deleteRefreshToken() => _storage.delete(key: refreshTokenKey);
}

/// In-memory implementation for tests. Survives across awaits within a
/// single test but resets at construction.
class InMemoryTokenStore implements SecureTokenStore {
  String? _value;

  @override
  Future<String?> readRefreshToken() async => _value;

  @override
  Future<void> writeRefreshToken(String token) async {
    _value = token;
  }

  @override
  Future<void> deleteRefreshToken() async {
    _value = null;
  }
}

final secureTokenStoreProvider = Provider<SecureTokenStore>((ref) {
  return FlutterSecureTokenStore();
});
