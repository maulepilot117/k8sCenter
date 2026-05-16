// Tests for the PendingOidc value object + the in-memory store. The
// FlutterSecureStorage variant is exercised end-to-end by the
// OIDCController flow tests; these focus on the value-object boundaries
// (isComplete, isExpired) plus the store round-trip.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/pending_oidc_store.dart';

void main() {
  group('PendingOidc.fromJson / toJson', () {
    test('round-trips a complete payload', () {
      final original = PendingOidc(
        providerID: 'authelia',
        state: 'STATE',
        codeVerifier: 'VERIFIER',
        nonce: 'NONCE',
        createdAtMillis: 1715900000000,
      );

      final round = PendingOidc.fromJson(original.toJson());
      expect(round.providerID, 'authelia');
      expect(round.state, 'STATE');
      expect(round.codeVerifier, 'VERIFIER');
      expect(round.nonce, 'NONCE');
      expect(round.createdAtMillis, 1715900000000);
    });

    test('coerces missing fields to defaults (string empty, millis 0)', () {
      final parsed = PendingOidc.fromJson(const {});
      expect(parsed.providerID, '');
      expect(parsed.state, '');
      expect(parsed.codeVerifier, '');
      expect(parsed.nonce, '');
      expect(parsed.createdAtMillis, 0);
    });
  });

  group('PendingOidc.isComplete', () {
    PendingOidc make({
      String providerID = 'authelia',
      String state = 'STATE',
      String codeVerifier = 'V',
      String nonce = 'N',
      int createdAtMillis = 1715900000000,
    }) {
      return PendingOidc(
        providerID: providerID,
        state: state,
        codeVerifier: codeVerifier,
        nonce: nonce,
        createdAtMillis: createdAtMillis,
      );
    }

    test('all fields populated: true', () {
      expect(make().isComplete, isTrue);
    });

    test('empty providerID: false', () {
      expect(make(providerID: '').isComplete, isFalse);
    });

    test('empty state: false', () {
      expect(make(state: '').isComplete, isFalse);
    });

    test('empty codeVerifier: false', () {
      expect(make(codeVerifier: '').isComplete, isFalse);
    });

    test('empty nonce: false', () {
      expect(make(nonce: '').isComplete, isFalse);
    });

    test('zero createdAtMillis: false', () {
      expect(make(createdAtMillis: 0).isComplete, isFalse);
    });
  });

  group('PendingOidc.isExpired', () {
    final created = DateTime.utc(2026, 5, 16, 12, 0, 0);
    final pending = PendingOidc(
      providerID: 'authelia',
      state: 'S',
      codeVerifier: 'V',
      nonce: 'N',
      createdAtMillis: created.millisecondsSinceEpoch,
    );

    test('exact TTL boundary: fresh', () {
      // age == pendingOidcTtl → NOT expired (strict greater-than).
      final now = created.add(pendingOidcTtl);
      expect(pending.isExpired(now), isFalse);
    });

    test('one ms past TTL: expired', () {
      final now = created.add(pendingOidcTtl).add(const Duration(milliseconds: 1));
      expect(pending.isExpired(now), isTrue);
    });

    test('within TTL: fresh', () {
      final now = created.add(const Duration(minutes: 4));
      expect(pending.isExpired(now), isFalse);
    });

    test('clock rewind (negative age): expired', () {
      // System clock moved backwards — treat the pending state as
      // expired to force a re-prompt rather than risk replaying stale
      // PKCE state against a fresh authorization code.
      final now = created.subtract(const Duration(seconds: 10));
      expect(pending.isExpired(now), isTrue);
    });
  });

  group('InMemoryPendingOidcStore', () {
    test('write → read returns the same value', () async {
      final store = InMemoryPendingOidcStore();
      final value = PendingOidc(
        providerID: 'authelia',
        state: 'S',
        codeVerifier: 'V',
        nonce: 'N',
        createdAtMillis: 1715900000000,
      );

      await store.write(value);
      final read = await store.read();
      expect(read, isNotNull);
      expect(read!.providerID, 'authelia');
      expect(read.state, 'S');
    });

    test('clear removes the value', () async {
      final store = InMemoryPendingOidcStore();
      await store.write(PendingOidc(
        providerID: 'x',
        state: 'S',
        codeVerifier: 'V',
        nonce: 'N',
        createdAtMillis: 1,
      ));
      await store.clear();
      expect(await store.read(), isNull);
    });

    test('fresh instance has nothing to read', () async {
      expect(await InMemoryPendingOidcStore().read(), isNull);
    });
  });
}
