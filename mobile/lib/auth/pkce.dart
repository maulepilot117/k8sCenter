// PKCE + CSRF primitives for the mobile OIDC flow.
//
// All randomness uses Random.secure() (OS CSPRNG). Sizes are picked to
// exceed RFC 7636 / RFC 8252 minimums by a wide margin so a future
// guidance change doesn't silently invalidate existing flows.
//
// RFC 7636 §4.1 — code_verifier: 43-128 chars from the unreserved set
//   `[A-Z][a-z][0-9]-._~`.
// RFC 7636 §4.2 — code_challenge: base64url(sha256(verifier)) with
//   trailing `=` padding stripped.
// RFC 6819 §3.6 — state: opaque CSRF token. We use 32-char hex (128
//   bits of entropy). RFC 6819 doesn't mandate a minimum but 128 bits
//   is the standard ceiling for an unguessable nonce.
// OpenID Connect Core 1.0 §3.1.2.1 — nonce: opaque value bound to
//   the ID token's `nonce` claim. Same 128-bit-hex shape as state but
//   generated independently.

import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';

/// Character set RFC 7636 allows in `code_verifier`. The hyphen comes
/// last so it doesn't accidentally form a character range inside a
/// regex character class if these chars are ever inlined into one.
const String _pkceVerifierAlphabet =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._~-';

/// Length used for [generateCodeVerifier]. 64 chars sits well inside
/// RFC 7636's [43, 128] range and produces ~384 bits of entropy at this
/// alphabet — enough to remain unguessable even under aggressive
/// rainbow-table attacks targeting the SHA-256 challenge.
const int _kVerifierLength = 64;

/// Length used for [generateState] and [generateNonce]. 32 hex chars =
/// 128 bits of entropy.
const int _kHexNonceLength = 32;

final Random _rng = Random.secure();

/// Generates a fresh PKCE `code_verifier` per RFC 7636 §4.1. Each call
/// draws fresh randomness from the OS CSPRNG.
String generateCodeVerifier() {
  final buffer = StringBuffer();
  for (var i = 0; i < _kVerifierLength; i++) {
    buffer.write(_pkceVerifierAlphabet[_rng.nextInt(_pkceVerifierAlphabet.length)]);
  }
  return buffer.toString();
}

/// Derives the PKCE `code_challenge` from a verifier per RFC 7636 §4.2.
/// Returns the URL-safe base64 encoding of SHA-256(verifier) with
/// trailing `=` padding stripped. Matches the Authorization Server
/// expectation for `code_challenge_method=S256`.
String codeChallengeFromVerifier(String verifier) {
  final hash = sha256.convert(utf8.encode(verifier)).bytes;
  // base64UrlEncode emits padded output; trim the `=` characters so the
  // challenge survives URL transit without re-padding logic on the IdP
  // side.
  return base64UrlEncode(hash).replaceAll('=', '');
}

/// Generates a 128-bit random hex string suitable for the OAuth2
/// `state` parameter (CSRF token). The mobile client validates the
/// returned `state` matches the value sent into the authorization
/// request before submitting the body-mode exchange.
String generateState() => _hex(_kHexNonceLength);

/// Generates a 128-bit random hex string suitable for the OpenID
/// Connect `nonce` parameter (ID-token-replay defence). Bound to the
/// `nonce` claim in the issued ID token; the backend validates the
/// claim matches the value the mobile client submits.
String generateNonce() => _hex(_kHexNonceLength);

String _hex(int chars) {
  const alphabet = '0123456789abcdef';
  final buffer = StringBuffer();
  for (var i = 0; i < chars; i++) {
    buffer.write(alphabet[_rng.nextInt(alphabet.length)]);
  }
  return buffer.toString();
}
