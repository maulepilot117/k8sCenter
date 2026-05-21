// OIDC mobile flow controller.
//
// State machine:
//
//   idle ─────tap────▶ launching ─────callback───▶ exchanging ─────success───▶ idle
//                          │                            │                    (auth state machine
//                          │                            │                     transitions to
//                          ▼                            ▼                     AuthAuthenticated)
//                        error                        error
//
// `idle` is the terminal state for both success and error. On success
// the auth state machine takes over (AuthAuthenticated triggers the
// router redirect); on error the login screen reads the controller
// state to render an inline error banner under the OIDC button. The
// caller resets the controller to idle via [clearError] before
// re-attempting.
//
// Cold-start re-entry: when the IdP redirects via Universal Link while
// the app process has been killed, the controller is initialized empty
// on next launch. [completeFlow] reads pending state from
// [PendingOidcStore] (secure_storage), so the flow survives.

import 'dart:async';

import 'package:flutter_custom_tabs/flutter_custom_tabs.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../notifications/deep_link_handler.dart' show universalLinkHostProvider;
import 'auth_repository.dart';
import 'oidc_repository.dart';
import 'pending_oidc_store.dart';
import 'pkce.dart';

/// Path component appended to the universal link host to form the OIDC
/// redirect URI. Inside the existing `/m/*` AASA + assetlinks.json
/// wildcard, so no platform-manifest changes are required.
const String oidcCallbackPath = '/m/auth/callback';

/// Function signature for the platform launch. Default impl wraps
/// flutter_custom_tabs.launchUrl; tests inject a stub.
typedef OidcLaunchUrl = Future<void> Function(Uri url);

Future<void> _defaultLaunch(Uri url) async {
  await launchUrl(url);
}

/// Sealed state for the OIDC flow.
sealed class OIDCFlowState {
  const OIDCFlowState();
}

class OIDCFlowIdle extends OIDCFlowState {
  const OIDCFlowIdle();
}

class OIDCFlowLaunching extends OIDCFlowState {
  const OIDCFlowLaunching(this.providerID);
  final String providerID;
}

class OIDCFlowExchanging extends OIDCFlowState {
  const OIDCFlowExchanging(this.providerID);
  final String providerID;
}

/// Distinct error reasons drive distinct inline error messages on the
/// login screen. Mapped from the controller in [OIDCFlowError.message].
enum OIDCFlowErrorReason {
  /// Build was produced without `--dart-define=UNIVERSAL_LINK_HOST`.
  /// OIDC mobile flow cannot work without a universal-link domain.
  universalLinkNotConfigured,

  /// Backend returned 404 from /mobile-config — provider not registered.
  providerUnknown,

  /// Network failure reaching the backend.
  networkError,

  /// Persisted state from the IdP redirect doesn't match what was sent.
  stateMismatch,

  /// Persisted pending state expired before the callback arrived.
  ttlExpired,

  /// User cancelled the IdP consent screen (error=access_denied in
  /// the redirect URL).
  consentDenied,

  /// Custom-tabs / SFSafariViewController couldn't launch (no browser,
  /// or platform-channel failure).
  customTabsLaunchFailed,

  /// Backend's mobile-exchange returned 401 (PKCE mismatch, code expired).
  exchangeRejected,

  /// Backend's mobile-exchange returned 403 (email domain not allowed).
  domainNotAllowed,

  /// Anything else (parse error, malformed response, etc.). The
  /// underlying ApiError or Exception is in [OIDCFlowError.detail].
  internalError,
}

class OIDCFlowError extends OIDCFlowState {
  const OIDCFlowError({required this.reason, this.detail});

  final OIDCFlowErrorReason reason;
  final String? detail;

  /// Human-readable message for the login screen's inline error banner.
  String get message {
    switch (reason) {
      case OIDCFlowErrorReason.universalLinkNotConfigured:
        return 'Sign-in is not configured for this build. Contact your operator.';
      case OIDCFlowErrorReason.providerUnknown:
        return 'Unknown sign-in provider. Try another option.';
      case OIDCFlowErrorReason.networkError:
        return "Couldn't reach the server. Check your connection and try again.";
      case OIDCFlowErrorReason.stateMismatch:
        return 'Sign-in failed (security check). Please try again.';
      case OIDCFlowErrorReason.ttlExpired:
        return 'Sign-in took too long. Please try again.';
      case OIDCFlowErrorReason.consentDenied:
        return 'Sign-in cancelled.';
      case OIDCFlowErrorReason.customTabsLaunchFailed:
        return "Couldn't open the sign-in browser on this device.";
      case OIDCFlowErrorReason.exchangeRejected:
        return 'Sign-in rejected by identity provider.';
      case OIDCFlowErrorReason.domainNotAllowed:
        return "Your email domain isn't authorized for this app.";
      case OIDCFlowErrorReason.internalError:
        return detail ?? 'Sign-in failed. Please try again.';
    }
  }
}

class OIDCController extends Notifier<OIDCFlowState> {
  OIDCController({
    OidcLaunchUrl? launchUrl,
    String? universalLinkHost,
    DateTime Function()? now,
  })  : _launchUrl = launchUrl ?? _defaultLaunch,
        _universalLinkHostOverride = universalLinkHost,
        _now = now ?? DateTime.now;

  final OidcLaunchUrl _launchUrl;
  final String? _universalLinkHostOverride;
  final DateTime Function() _now;

  // Constructor override seam preserved for unit tests; production reads
  // through the provider so widget tests can override the host without
  // rebuilding the controller.
  String get _universalLinkHost =>
      _universalLinkHostOverride ?? ref.read(universalLinkHostProvider);

  @override
  OIDCFlowState build() => const OIDCFlowIdle();

  /// Resets the controller to idle. The login screen calls this when
  /// the user dismisses the inline error banner or taps a different
  /// provider button.
  void clearError() {
    if (state is OIDCFlowError) {
      state = const OIDCFlowIdle();
    }
  }

  /// Begin an OIDC flow for [providerID]. Fetches the provider's auth
  /// config from the backend, generates PKCE + state + nonce, persists
  /// the pending state, and launches the IdP authorization URL in
  /// flutter_custom_tabs.
  Future<void> startFlow(String providerID) async {
    if (_universalLinkHost.isEmpty) {
      state = const OIDCFlowError(
        reason: OIDCFlowErrorReason.universalLinkNotConfigured,
      );
      return;
    }

    state = OIDCFlowLaunching(providerID);

    final repo = ref.read(oidcRepositoryProvider);
    final pendingStore = ref.read(pendingOidcStoreProvider);

    final OIDCMobileAuthConfig cfg;
    try {
      cfg = await repo.fetchMobileConfig(providerID);
    } on ApiError catch (e) {
      state = OIDCFlowError(
        reason: e.statusCode == 404
            ? OIDCFlowErrorReason.providerUnknown
            : OIDCFlowErrorReason.networkError,
        detail: e.message,
      );
      return;
    }

    final verifier = generateCodeVerifier();
    final challenge = codeChallengeFromVerifier(verifier);
    final stateParam = generateState();
    final nonce = generateNonce();
    final redirectUri = 'https://$_universalLinkHost$oidcCallbackPath';

    // Persist BEFORE launching so a process kill during launch doesn't
    // leave the pending state half-written. A write failure here means
    // the cold-start re-entry path is broken anyway — surface as an
    // internal error rather than launching the IdP into a flow we
    // cannot complete.
    try {
      await pendingStore.write(PendingOidc(
        providerID: providerID,
        state: stateParam,
        codeVerifier: verifier,
        nonce: nonce,
        createdAtMillis: _now().millisecondsSinceEpoch,
      ));
    } catch (e) {
      state = OIDCFlowError(
        reason: OIDCFlowErrorReason.internalError,
        detail: e.toString(),
      );
      return;
    }

    final authUrl = buildAuthUrl(
      authorizationEndpoint: cfg.authorizationEndpoint,
      clientId: cfg.clientId,
      scopes: cfg.scopes,
      redirectUri: redirectUri,
      codeChallenge: challenge,
      state: stateParam,
      nonce: nonce,
    );

    try {
      await _launchUrl(authUrl);
    } catch (e) {
      await pendingStore.clear();
      state = OIDCFlowError(
        reason: OIDCFlowErrorReason.customTabsLaunchFailed,
        detail: e.toString(),
      );
    }
  }

  /// Universal-Link callback handler. Reads pending state, validates
  /// against the redirect, and triggers the body-mode exchange. Public
  /// entry point for both the live-app callback path (router listens
  /// to app_links and dispatches here) and the cold-start re-entry
  /// path (main.dart drains the initial link after auth bootstrap).
  Future<void> completeFlow(Uri callback) async {
    final pendingStore = ref.read(pendingOidcStoreProvider);
    final pending = await pendingStore.read();

    if (pending == null) {
      // No flow in progress. Either the callback arrived after the user
      // already completed/cancelled, or someone tapped a stale link.
      // Treat as no-op rather than an error — surfacing an error here
      // would confuse users who navigated to the app without intending
      // to complete a sign-in.
      return;
    }

    if (pending.isExpired(_now())) {
      await pendingStore.clear();
      state = const OIDCFlowError(reason: OIDCFlowErrorReason.ttlExpired);
      return;
    }

    // IdP cancellation surfaces as `?error=access_denied`. Inspect
    // BEFORE pulling `code` so we route the right error. RFC 6749 §4.1.2.1
    // OAuth error codes + OIDC core §3.1.2.6 mapped to controller reasons
    // so the inline banner reflects the actual condition.
    final errorParam = callback.queryParameters['error'];
    if (errorParam != null && errorParam.isNotEmpty) {
      await pendingStore.clear();
      final reason = switch (errorParam) {
        'access_denied' => OIDCFlowErrorReason.consentDenied,
        // User not signed in at IdP / interaction prompt suppressed —
        // surfaces the same "Sign-in cancelled" UX since the next attempt
        // will succeed once the user authenticates.
        'login_required' => OIDCFlowErrorReason.consentDenied,
        'interaction_required' => OIDCFlowErrorReason.consentDenied,
        // Transient IdP-side failures get the network bucket so the user
        // is told to retry rather than seeing "rejected by IdP".
        'temporarily_unavailable' => OIDCFlowErrorReason.networkError,
        'server_error' => OIDCFlowErrorReason.networkError,
        _ => OIDCFlowErrorReason.exchangeRejected,
      };
      state = OIDCFlowError(
        reason: reason,
        detail: callback.queryParameters['error_description'],
      );
      return;
    }

    final code = callback.queryParameters['code'];
    final callbackState = callback.queryParameters['state'];

    if (code == null || code.isEmpty || callbackState == null) {
      await pendingStore.clear();
      state = const OIDCFlowError(reason: OIDCFlowErrorReason.internalError);
      return;
    }

    if (callbackState != pending.state) {
      await pendingStore.clear();
      state = const OIDCFlowError(reason: OIDCFlowErrorReason.stateMismatch);
      return;
    }

    state = OIDCFlowExchanging(pending.providerID);

    final repo = ref.read(oidcRepositoryProvider);
    final OIDCExchangeResult result;
    try {
      result = await repo.exchangeMobile(
        providerID: pending.providerID,
        code: code,
        codeVerifier: pending.codeVerifier,
        nonce: pending.nonce,
      );
    } on ApiError catch (e) {
      await pendingStore.clear();
      state = OIDCFlowError(
        reason: _classifyExchangeError(e),
        detail: e.message,
      );
      return;
    } catch (e) {
      await pendingStore.clear();
      state = OIDCFlowError(
        reason: OIDCFlowErrorReason.internalError,
        detail: e.toString(),
      );
      return;
    }

    // Success path: write tokens, hydrate /me, clear pending. Order
    // matters — clear pending AFTER applyAuthTokens succeeds so a
    // hydration failure can be retried via the auth state machine
    // without losing the OIDC flow context. In practice applyAuthTokens
    // always returns (it sets state to Unauthenticated on hydration
    // failure), so this is belt-and-braces.
    //
    // The 30s timeout bounds the worst case where the backend hangs on
    // /v1/auth/me — without it the OIDC controller could sit in
    // [OIDCFlowExchanging] forever and lock the login screen out of
    // retries. Times out to networkError so the user is told to retry.
    try {
      await ref
          .read(authRepositoryProvider.notifier)
          .applyAuthTokens(
            accessToken: result.accessToken,
            refreshToken: result.refreshToken,
          )
          .timeout(
            const Duration(seconds: 30),
            onTimeout: () =>
                throw TimeoutException('applyAuthTokens timeout'),
          );
    } on TimeoutException {
      // finally below clears pending; just surface the error here.
      state = const OIDCFlowError(
        reason: OIDCFlowErrorReason.networkError,
        detail: 'auth/me hydration timeout',
      );
      return;
    } finally {
      await pendingStore.clear();
    }
    state = const OIDCFlowIdle();
  }

  OIDCFlowErrorReason _classifyExchangeError(ApiError e) {
    switch (e.statusCode) {
      case 401:
        return OIDCFlowErrorReason.exchangeRejected;
      case 403:
        return OIDCFlowErrorReason.domainNotAllowed;
      case 404:
        return OIDCFlowErrorReason.providerUnknown;
      default:
        return OIDCFlowErrorReason.networkError;
    }
  }

  /// Constructs the IdP authorization URL with PKCE + state + nonce.
  /// Public so tests can verify URL composition without driving the full
  /// flow; production callers use [startFlow].
  static Uri buildAuthUrl({
    required String authorizationEndpoint,
    required String clientId,
    required List<String> scopes,
    required String redirectUri,
    required String codeChallenge,
    required String state,
    required String nonce,
  }) {
    final base = Uri.parse(authorizationEndpoint);
    return base.replace(queryParameters: {
      ...base.queryParameters,
      'client_id': clientId,
      'redirect_uri': redirectUri,
      'response_type': 'code',
      'scope': scopes.isEmpty ? 'openid profile email' : scopes.join(' '),
      'code_challenge': codeChallenge,
      'code_challenge_method': 'S256',
      'state': state,
      'nonce': nonce,
    });
  }
}

final oidcControllerProvider =
    NotifierProvider<OIDCController, OIDCFlowState>(OIDCController.new);
