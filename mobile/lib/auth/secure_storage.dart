// Refresh-token persistence via flutter_secure_storage (iOS Keychain /
// Android EncryptedSharedPreferences). Single key. Riverpod-injected so
// tests can substitute an in-memory implementation.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

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
