// Auth state machine + the calls that mutate it. Mirrors
// `frontend/lib/auth.ts`:
//   login()          → /v1/auth/login + fetchCurrentUser()
//   bootstrap()      → body-mode /v1/auth/refresh on cold start
//   logout()         → /v1/auth/logout, then clear local state
//   fetchCurrentUser → /v1/auth/me to populate user + RBAC

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/auth_token_holder.dart';
import '../api/dio_client.dart';
import 'auth_state.dart';
import 'secure_storage.dart';
import 'user.dart';

/// Identifies a credential provider for the login form's dropdown.
class AuthProvider {
  const AuthProvider({required this.id, required this.name, required this.kind});

  factory AuthProvider.fromJson(Map<String, dynamic> json) {
    return AuthProvider(
      id: json['id'] as String? ?? 'local',
      name: json['name'] as String? ?? 'Local',
      kind: json['kind'] as String? ?? 'credential',
    );
  }

  final String id;
  final String name;
  final String kind;

  bool get isCredentialProvider => kind == 'credential';
}

class AuthRepository extends Notifier<AuthState> {
  @override
  AuthState build() => const AuthInitializing();

  /// Cold-start bootstrap: try to exchange the persisted refresh token
  /// for an access token via body-mode /v1/auth/refresh, then fetch /me.
  /// On any failure, transition to Unauthenticated.
  Future<void> bootstrap() async {
    final store = ref.read(secureTokenStoreProvider);
    final refreshToken = await store.readRefreshToken();
    if (refreshToken == null) {
      state = const AuthUnauthenticated();
      return;
    }

    try {
      final refreshDio = ref.read(refreshDioProvider);
      final res = await refreshDio.post<Map<String, dynamic>>(
        '/api/v1/auth/refresh',
        data: {'refreshToken': refreshToken},
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      final newAccess = data?['accessToken'] as String?;
      final newRefresh = data?['refreshToken'] as String?;
      if (newAccess == null) {
        await store.deleteRefreshToken();
        state = const AuthUnauthenticated();
        return;
      }
      ref.read(authTokenHolderProvider).set(newAccess);
      if (newRefresh != null) {
        await store.writeRefreshToken(newRefresh);
      }
      await _hydrateUser();
    } on DioException {
      await store.deleteRefreshToken();
      ref.read(authTokenHolderProvider).clear();
      state = const AuthUnauthenticated();
    }
  }

  Future<void> login({
    required String username,
    required String password,
    String provider = 'local',
  }) async {
    state = const AuthAuthenticating();

    final dio = ref.read(refreshDioProvider);
    try {
      final res = await dio.post<Map<String, dynamic>>(
        '/api/v1/auth/login',
        data: {
          'username': username,
          'password': password,
          if (provider != 'local') 'provider': provider,
        },
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      final accessToken = data?['accessToken'] as String?;
      final refreshToken = data?['refreshToken'] as String?;
      if (accessToken == null) {
        state = const AuthUnauthenticated(
          errorMessage: 'Login response missing access token.',
        );
        return;
      }

      ref.read(authTokenHolderProvider).set(accessToken);
      if (refreshToken != null) {
        await ref.read(secureTokenStoreProvider).writeRefreshToken(refreshToken);
      }
      await _hydrateUser();
      // Rollback on hydration failure — mirrors [applyAuthTokens]. Without
      // this, the refresh token survives in secure_storage and the access
      // token survives in authTokenHolder when /v1/auth/me errors after
      // /v1/auth/login succeeds. Next cold-start would silently
      // re-authenticate via /v1/auth/refresh even though the user was
      // told "Sign-in failed". Issue #279.
      if (state is AuthUnauthenticated) {
        ref.read(authTokenHolderProvider).clear();
        await ref.read(secureTokenStoreProvider).deleteRefreshToken();
      }
    } on DioException catch (e) {
      final apiError = ApiError.fromDio(e);
      state = AuthUnauthenticated(errorMessage: apiError.message);
    }
  }

  Future<void> logout() async {
    final dio = ref.read(dioProvider);
    try {
      await dio.post<void>('/api/v1/auth/logout');
    } on DioException {
      // Best-effort — proceed to clear local state.
    }
    ref.read(authTokenHolderProvider).clear();
    await ref.read(secureTokenStoreProvider).deleteRefreshToken();
    state = const AuthUnauthenticated();
  }

  /// Lists ALL configured auth providers (credential + OIDC). The login
  /// screen filters by `kind` to decide which UI surface to render:
  /// credential-style providers feed the dropdown next to the form;
  /// OIDC providers render as separate "Sign in with X" buttons.
  ///
  /// Empty list means the backend is unreachable or no providers are
  /// configured — the login screen falls back to local-only behaviour.
  Future<List<AuthProvider>> listProviders() async {
    final dio = ref.read(refreshDioProvider);
    try {
      final res = await dio.get<Map<String, dynamic>>('/api/v1/auth/providers');
      final data = res.data?['data'];
      if (data is List) {
        return data
            .whereType<Map<String, dynamic>>()
            .map(AuthProvider.fromJson)
            .toList();
      }
    } on DioException {
      // Network or backend down — login screen falls back to local-only.
    }
    return const [];
  }

  /// Public entry point for token-bearing auth flows that bypass
  /// [login] (currently: the OIDC mobile body-mode exchange). Persists
  /// the tokens and triggers a /v1/auth/me hydration so the rest of the
  /// app sees [AuthAuthenticated] with user + RBAC populated.
  ///
  /// Mirrors the post-login tail of [login] without forcing the caller
  /// to also reimplement token storage. On hydration failure the state
  /// transitions to [AuthUnauthenticated] with an error message.
  Future<void> applyAuthTokens({
    required String accessToken,
    required String refreshToken,
  }) async {
    state = const AuthAuthenticating();
    ref.read(authTokenHolderProvider).set(accessToken);
    await ref.read(secureTokenStoreProvider).writeRefreshToken(refreshToken);
    await _hydrateUser();
    // Rollback on hydration failure — don't leave the user "silently
    // signed in" via persisted tokens after a flow they were told failed.
    // Without this, the refresh token survives in secure_storage and the
    // access token survives in authTokenHolder → next cold-start silently
    // re-authenticates against /v1/auth/refresh.
    if (state is AuthUnauthenticated) {
      ref.read(authTokenHolderProvider).clear();
      await ref.read(secureTokenStoreProvider).deleteRefreshToken();
    }
  }

  Future<void> _hydrateUser() async {
    final dio = ref.read(dioProvider);
    try {
      final res = await dio.get<Map<String, dynamic>>('/api/v1/auth/me');
      final data = res.data?['data'] as Map<String, dynamic>?;
      final userJson = data?['user'] as Map<String, dynamic>?;
      final rbacJson = data?['rbac'] as Map<String, dynamic>?;
      if (userJson == null) {
        state = const AuthUnauthenticated(
          errorMessage: '/auth/me missing user payload.',
        );
        return;
      }
      state = AuthAuthenticated(
        user: UserInfo.fromJson(userJson),
        rbac: RBACSummary.fromJson(rbacJson),
      );
    } on DioException catch (e) {
      final apiError = ApiError.fromDio(e);
      state = AuthUnauthenticated(errorMessage: apiError.message);
    }
  }
}

final authRepositoryProvider = NotifierProvider<AuthRepository, AuthState>(
  AuthRepository.new,
);
