// Dio client + interceptor stack for the k8sCenter mobile app.
//
// Mirrors `frontend/lib/api.ts`:
//   - Authorization: Bearer <access token> on every request
//   - X-Cluster-ID injected from the active cluster provider
//   - X-Requested-With: XMLHttpRequest on non-GET requests (CSRF gate)
//   - On 401, dedupe concurrent refreshes via a single Completer, retry
//     the original request once, then propagate failure if refresh fails
//   - All errors surface as ApiError so widgets render consistent wording
//
// The refresh client is a separate Dio instance with no interceptors. This
// breaks the recursion that would otherwise fire when /v1/auth/refresh
// returns 401 itself (e.g., rotated token).

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart' show kDebugMode;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/secure_storage.dart';
import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'auth_token_holder.dart';

/// Backend base URL. Defaults to localhost in debug builds; release
/// and profile builds REQUIRE an HTTPS URL via `--dart-define=BACKEND_URL=...`.
/// Silent fallback to localhost in release was finding P1-4 of the
/// 2026-05-22 security audit — a missing dart-define would have
/// pointed release traffic at a local listener (potentially attacker-
/// controlled on a shared device). The provider now fails loudly at
/// first read in non-debug builds rather than silently degrading.
const String _backendUrlFromEnv = String.fromEnvironment(
  'BACKEND_URL',
  defaultValue: '',
);

final backendUrlProvider = Provider<String>((ref) {
  final raw = _backendUrlFromEnv.trim();
  if (kDebugMode) {
    // Debug builds default to localhost for fast iteration.
    return raw.isEmpty ? 'http://localhost:8080' : raw;
  }
  // Release + profile: require an explicit, non-localhost HTTPS URL.
  if (raw.isEmpty) {
    throw StateError(
      'BACKEND_URL is required in release/profile builds. '
      'Pass --dart-define=BACKEND_URL=https://your-backend.example.com '
      'at build time. (Finding P1-4)',
    );
  }
  final uri = Uri.tryParse(raw);
  if (uri == null || !uri.hasScheme) {
    throw StateError('BACKEND_URL is not a valid URL: $raw (Finding P1-4)');
  }
  if (uri.scheme != 'https') {
    throw StateError(
      'BACKEND_URL must use https in release/profile builds; got "$raw" '
      '(Finding P1-4)',
    );
  }
  final host = uri.host.toLowerCase();
  const loopback = {'localhost', '127.0.0.1', '::1', '0.0.0.0'};
  if (loopback.contains(host)) {
    throw StateError(
      'BACKEND_URL must not point at loopback in release/profile builds; '
      'got "$raw" (Finding P1-4)',
    );
  }
  return raw;
});

/// Refresh-only Dio. No interceptors so /v1/auth/refresh failures don't
/// recurse through [AuthInterceptor]. Used by [AuthInterceptor] when the
/// access token is rejected.
final refreshDioProvider = Provider<Dio>((ref) {
  final base = ref.watch(backendUrlProvider);
  return Dio(BaseOptions(
    baseUrl: base,
    connectTimeout: const Duration(seconds: 10),
    receiveTimeout: const Duration(seconds: 30),
    contentType: 'application/json',
    headers: {'X-Requested-With': 'XMLHttpRequest'},
  ));
});

/// Primary Dio. Interceptors run in registration order.
///
/// Three timeouts matter for the YAML editor + delete paths added in
/// PR-2b:
///   * `connectTimeout` (10s) — TCP handshake, fine on any network.
///   * `sendTimeout` (30s) — protects YAML uploads from stalling
///     forever on flaky mobile connections. ConfigMaps with multi-MB
///     embedded JSON would otherwise hang the Apply button with no
///     error.
///   * `receiveTimeout` (30s) — default for non-delete responses.
///     `executeAction` overrides this to 90s for `ActionId.delete`
///     because pods with `terminationGracePeriodSeconds > 30` legally
///     keep the backend's k8s call open longer.
final dioProvider = Provider<Dio>((ref) {
  final base = ref.watch(backendUrlProvider);
  final dio = Dio(BaseOptions(
    baseUrl: base,
    connectTimeout: const Duration(seconds: 10),
    sendTimeout: const Duration(seconds: 30),
    receiveTimeout: const Duration(seconds: 30),
    contentType: 'application/json',
  ));

  dio.interceptors.add(ClusterInterceptor(ref));
  dio.interceptors.add(CSRFInterceptor());
  dio.interceptors.add(AuthInterceptor(ref, dio));
  dio.interceptors.add(ErrorMappingInterceptor());

  return dio;
});

/// Adds `X-Cluster-ID: <active>` so multi-cluster routing reaches the
/// right backend. Defaults to `local` in PR-1b; PR-1c hooks the real
/// cluster picker into [activeClusterProvider].
///
/// Honors a pre-set `X-Cluster-ID` header so callers that already pin
/// a cluster (wizards' named-resource pickers, scheduled background
/// fetches keyed on a specific cluster) can override the active value
/// per request. Without this carve-out the interceptor would silently
/// reroute the picker's wire request to whichever cluster the operator
/// last toggled in the picker UI, producing wrong-cluster reads under
/// the wizard's pinned-cluster cache slot.
class ClusterInterceptor extends Interceptor {
  ClusterInterceptor(this.ref);

  final Ref ref;

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    if (!options.headers.containsKey('X-Cluster-ID')) {
      options.headers['X-Cluster-ID'] = ref.read(activeClusterProvider);
    }
    handler.next(options);
  }
}

/// Adds CSRF gate header on state-changing methods. Backend middleware
/// rejects state-changing requests without it.
class CSRFInterceptor extends Interceptor {
  static const _safeMethods = {'GET', 'HEAD', 'OPTIONS'};

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final method = options.method.toUpperCase();
    if (!_safeMethods.contains(method)) {
      options.headers['X-Requested-With'] = 'XMLHttpRequest';
    }
    handler.next(options);
  }
}

/// Bearer token injection + 401 retry with refresh dedupe.
///
/// On 401:
///  1. If a refresh is already in flight, await its result and retry once
///  2. Otherwise start a refresh (single concurrent), retry once on success
///  3. On failure, clear the access + refresh tokens and propagate 401 —
///     downstream auth state machine handles the redirect to /login.
class AuthInterceptor extends Interceptor {
  AuthInterceptor(this.ref, this._dio);

  final Ref ref;
  final Dio _dio;
  Completer<bool>? _refreshing;

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final token = ref.read(authTokenHolderProvider).accessToken;
    if (token != null && !options.headers.containsKey('Authorization')) {
      options.headers['Authorization'] = 'Bearer $token';
    }
    handler.next(options);
  }

  @override
  Future<void> onError(
    DioException err,
    ErrorInterceptorHandler handler,
  ) async {
    final response = err.response;
    final shouldRefresh = response?.statusCode == 401 &&
        err.requestOptions.extra['retried'] != true &&
        !err.requestOptions.path.contains('/v1/auth/refresh') &&
        !err.requestOptions.path.contains('/v1/auth/login');

    if (!shouldRefresh) {
      handler.next(err);
      return;
    }

    final refreshed = await _attemptRefresh();
    if (!refreshed) {
      handler.next(err);
      return;
    }

    // Retry the original request through the same dio so that the mock
    // adapter (in tests) and the regular interceptor stack apply
    // consistently. Setting extra['retried']=true prevents recursion.
    final clone = err.requestOptions.copyWith();
    clone.extra['retried'] = true;
    final newToken = ref.read(authTokenHolderProvider).accessToken;
    if (newToken != null) {
      clone.headers['Authorization'] = 'Bearer $newToken';
    }
    try {
      final retried = await _dio.fetch<dynamic>(clone);
      handler.resolve(retried);
    } on DioException catch (e) {
      handler.next(e);
    }
  }

  Future<bool> _attemptRefresh() {
    final inFlight = _refreshing;
    if (inFlight != null) return inFlight.future;

    final completer = Completer<bool>();
    _refreshing = completer;
    // try/finally guarantees `_refreshing` is cleared even if `_refresh`
    // throws synchronously before returning a Future, or if a Zone error
    // bypasses the .catchError path. Without this, a single synchronous
    // throw permanently wedges all subsequent 401 retries. The IIFE is
    // intentionally fire-and-forget — callers wait on `completer.future`,
    // so wrap in `unawaited` to declare intent and survive future
    // analyzer upgrades that enable `unawaited_futures`.
    unawaited(() async {
      try {
        final ok = await _refresh();
        completer.complete(ok);
      } catch (_) {
        completer.complete(false);
      } finally {
        _refreshing = null;
      }
    }());
    return completer.future;
  }

  Future<bool> _refresh() async {
    final store = ref.read(secureTokenStoreProvider);
    final refreshToken = await store.readRefreshToken();
    if (refreshToken == null) return false;

    final refreshDio = ref.read(refreshDioProvider);
    try {
      final res = await refreshDio.post<Map<String, dynamic>>(
        '/api/v1/auth/refresh',
        data: {'refreshToken': refreshToken},
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      final newAccess = data?['accessToken'] as String?;
      final newRefresh = data?['refreshToken'] as String?;
      if (newAccess == null) return false;

      ref.read(authTokenHolderProvider).set(newAccess);
      if (newRefresh != null) {
        await store.writeRefreshToken(newRefresh);
      }
      return true;
    } on DioException {
      // Stale or rotated token — clear local state so the auth machine
      // transitions to Unauthenticated.
      await store.deleteRefreshToken();
      ref.read(authTokenHolderProvider).clear();
      return false;
    }
  }
}

/// Maps DioException → ApiError so callers throw against a single type.
class ErrorMappingInterceptor extends Interceptor {
  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    final apiError = ApiError.fromDio(err);
    handler.reject(DioException(
      requestOptions: err.requestOptions,
      response: err.response,
      type: err.type,
      error: apiError,
      message: apiError.message,
    ));
  }
}
