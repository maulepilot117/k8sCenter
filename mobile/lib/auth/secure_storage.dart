// Refresh-token persistence via flutter_secure_storage (iOS Keychain /
// Android EncryptedSharedPreferences). Single key. Riverpod-injected so
// tests can substitute an in-memory implementation.

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

class FlutterSecureTokenStore implements SecureTokenStore {
  FlutterSecureTokenStore({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

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
