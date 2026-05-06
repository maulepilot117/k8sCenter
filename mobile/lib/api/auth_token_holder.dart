// In-memory access token holder. Riverpod provider so tests can override.
// Access tokens never persist to disk; refresh tokens go through
// SecureStorage (lib/auth/secure_storage.dart).

import 'package:flutter_riverpod/flutter_riverpod.dart';

class AuthTokenHolder {
  String? _accessToken;

  String? get accessToken => _accessToken;

  void set(String? token) {
    _accessToken = token;
  }

  void clear() {
    _accessToken = null;
  }
}

final authTokenHolderProvider = Provider<AuthTokenHolder>((ref) {
  return AuthTokenHolder();
});
