import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/pkce.dart';

void main() {
  group('generateCodeVerifier', () {
    test('returns 64 chars of RFC 7636 unreserved alphabet', () {
      final v = generateCodeVerifier();
      expect(v.length, 64);
      // RFC 7636: [A-Z][a-z][0-9]-._~
      final allowed = RegExp(r'^[A-Za-z0-9._~-]+$');
      expect(allowed.hasMatch(v), isTrue, reason: 'verifier = $v');
    });

    test('successive calls return distinct values', () {
      final samples = <String>{
        for (var i = 0; i < 32; i++) generateCodeVerifier(),
      };
      // 64 chars from a 64-symbol alphabet → collision probability is
      // astronomically low. Anything less than 32 unique samples means
      // Random.secure() is misbehaving.
      expect(samples.length, 32);
    });
  });

  group('codeChallengeFromVerifier', () {
    test('matches RFC 7636 Appendix B fixture', () {
      // RFC 7636 Appendix B specifies:
      //   verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
      //   challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
      const verifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
      const expected = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
      expect(codeChallengeFromVerifier(verifier), expected);
    });

    test('strips trailing = padding', () {
      // SHA-256 output is always 32 bytes; base64url-encoded that is
      // 44 chars with 1 = of padding. Verify the padding is stripped.
      final challenge = codeChallengeFromVerifier(generateCodeVerifier());
      expect(challenge.contains('='), isFalse);
      // 32 bytes → base64url-encoded → 43 chars after = stripped.
      expect(challenge.length, 43);
    });

    test('challenge is URL-safe (no + or /)', () {
      // base64url substitutes `-` for `+` and `_` for `/`. A naive
      // base64Encode would slip standard-base64 chars through.
      for (var i = 0; i < 16; i++) {
        final challenge = codeChallengeFromVerifier(generateCodeVerifier());
        expect(challenge.contains('+'), isFalse);
        expect(challenge.contains('/'), isFalse);
      }
    });
  });

  group('generateState / generateNonce', () {
    test('both return 32-char hex', () {
      final hexAllowed = RegExp(r'^[0-9a-f]{32}$');
      expect(hexAllowed.hasMatch(generateState()), isTrue);
      expect(hexAllowed.hasMatch(generateNonce()), isTrue);
    });

    test('state and nonce are independently random', () {
      // Generating both should produce distinct values overwhelmingly
      // often. 32 hex chars = 128 bits of entropy; a collision in this
      // pair is a Random.secure() bug.
      for (var i = 0; i < 16; i++) {
        expect(generateState(), isNot(equals(generateNonce())));
      }
    });

    test('successive nonces are unique', () {
      final samples = <String>{
        for (var i = 0; i < 32; i++) generateNonce(),
      };
      expect(samples.length, 32);
    });
  });
}
