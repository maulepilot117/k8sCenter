// API client for the body-mode OIDC mobile flow.
//
// Two backend endpoints:
//   GET  /v1/auth/oidc/{providerID}/mobile-config   → {authorizationEndpoint, clientID, scopes}
//   POST /v1/auth/oidc/{providerID}/mobile-exchange → {accessToken, refreshToken, expiresIn, refreshExpiresIn, user}
//
// Uses [refreshDioProvider] (no interceptors). The mobile-exchange call
// runs before any access token exists, so attaching the standard auth
// interceptor would inject an empty Authorization header for no benefit.
// The X-Requested-With header is set at the BaseOptions level on the
// refresh Dio, satisfying the inline CSRF check the backend enforces on
// the exchange route.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';

/// Result of the GET .../mobile-config call.
class OIDCMobileAuthConfig {
  const OIDCMobileAuthConfig({
    required this.authorizationEndpoint,
    required this.clientID,
    required this.scopes,
  });

  factory OIDCMobileAuthConfig.fromJson(Map<String, dynamic> json) {
    // Load-bearing fields — empty/missing values would cause silent
    // misbehaviour (auth URL built against empty endpoint, blank
    // client_id sent to IdP). Throw a FormatException so the repository
    // can wrap as a 502 ApiError and the controller surfaces a real
    // error to the user instead of launching a broken auth flow.
    final endpoint = json['authorizationEndpoint'] as String?;
    final clientId = json['clientID'] as String?;
    if (endpoint == null || endpoint.isEmpty) {
      throw const FormatException(
        'mobile-config response missing authorizationEndpoint',
      );
    }
    if (clientId == null || clientId.isEmpty) {
      throw const FormatException(
        'mobile-config response missing clientID',
      );
    }
    final scopesRaw = json['scopes'];
    final scopes = scopesRaw is List
        ? scopesRaw.whereType<String>().toList(growable: false)
        : const <String>[];
    return OIDCMobileAuthConfig(
      authorizationEndpoint: endpoint,
      clientID: clientId,
      scopes: scopes,
    );
  }

  final String authorizationEndpoint;
  final String clientID;
  final List<String> scopes;
}

/// Full token + user payload from POST /v1/auth/oidc/{id}/mobile-exchange.
///
/// `accessToken` + `refreshToken` are consumed by [OIDCController.completeFlow]
/// via [AuthRepository.applyAuthTokens]; the user fields (`expiresIn`,
/// `refreshExpiresIn`, `username`, `groups`, `provider`) are exposed for
/// future surfaces — proactive re-auth scheduling via `refreshExpiresIn`,
/// login-screen debug UI, telemetry — and validated by
/// `oidc_repository_test.dart` against the wire contract. Mirrors the
/// backend response shape after issue #277 cleanup — username only; no
/// separate displayName field.
class OIDCExchangeResult {
  const OIDCExchangeResult({
    required this.accessToken,
    required this.refreshToken,
    required this.expiresIn,
    required this.refreshExpiresIn,
    required this.username,
    required this.groups,
    required this.provider,
  });

  factory OIDCExchangeResult.fromJson(Map<String, dynamic> json) {
    final userJson = json['user'] as Map<String, dynamic>? ?? const {};
    final groupsRaw = userJson['groups'];
    final groups = groupsRaw is List
        ? groupsRaw.whereType<String>().toList(growable: false)
        : const <String>[];
    return OIDCExchangeResult(
      accessToken: json['accessToken'] as String? ?? '',
      refreshToken: json['refreshToken'] as String? ?? '',
      expiresIn: (json['expiresIn'] as num?)?.toInt() ?? 0,
      refreshExpiresIn: (json['refreshExpiresIn'] as num?)?.toInt() ?? 0,
      username: userJson['username'] as String? ?? '',
      groups: groups,
      provider: userJson['provider'] as String? ?? '',
    );
  }

  final String accessToken;
  final String refreshToken;
  final int expiresIn;
  final int refreshExpiresIn;
  final String username;
  final List<String> groups;
  final String provider;
}

/// Repository thin wrapper. Single responsibility per method —
/// translating Dio + JSON into typed Dart shapes and ApiError on failure.
class OIDCRepository {
  OIDCRepository(this._dio);

  final Dio _dio;

  /// Fetches the OIDC auth-config the mobile client needs to construct
  /// its authorization URL. Throws [ApiError] on failure.
  Future<OIDCMobileAuthConfig> fetchMobileConfig(String providerID) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/auth/oidc/$providerID/mobile-config',
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      if (data == null) {
        throw ApiError(
          statusCode: 500,
          code: 500,
          message: 'mobile-config response missing data envelope',
        );
      }
      return OIDCMobileAuthConfig.fromJson(data);
    } on DioException catch (e) {
      throw ApiError.fromDio(e);
    } on FormatException catch (e) {
      throw ApiError(
        statusCode: 502,
        code: 502,
        message: 'malformed mobile-config response: ${e.message}',
      );
    }
  }

  /// Exchanges the authorization code + PKCE verifier for a JWT pair.
  /// Throws [ApiError] on failure.
  Future<OIDCExchangeResult> exchangeMobile({
    required String providerID,
    required String code,
    required String codeVerifier,
    required String nonce,
  }) async {
    try {
      final res = await _dio.post<Map<String, dynamic>>(
        '/api/v1/auth/oidc/$providerID/mobile-exchange',
        data: {
          'code': code,
          'codeVerifier': codeVerifier,
          'nonce': nonce,
        },
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      if (data == null) {
        throw ApiError(
          statusCode: 500,
          code: 500,
          message: 'mobile-exchange response missing data envelope',
        );
      }
      return OIDCExchangeResult.fromJson(data);
    } on DioException catch (e) {
      throw ApiError.fromDio(e);
    }
  }
}

/// Provider wires the repository to the no-interceptors refreshDio so
/// callers don't accidentally attach an empty Authorization header
/// pre-auth.
final oidcRepositoryProvider = Provider<OIDCRepository>((ref) {
  return OIDCRepository(ref.read(refreshDioProvider));
});
