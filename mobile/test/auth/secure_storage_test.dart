// Tests for FlutterSecureTokenStore — focused on the audit-finding-P3-7
// forward migration from default-options secure-storage to the explicit
// first_unlock_this_device / encryptedSharedPreferences backend.
//
// FlutterSecureStorage itself is platform-channel-backed, so the
// migration is exercised via the @visibleForTesting `fromBackends`
// constructor that accepts SecureTokenStore implementations directly.
// In-memory backends faithfully model the per-instance isolation that
// the production code relies on (writes to one don't appear in reads
// from the other).

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/secure_storage.dart';

void main() {
  group('FlutterSecureTokenStore.readRefreshToken (P3-7 migration)', () {
    test(
        'reads from current backend when present without touching legacy',
        () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      await current.writeRefreshToken('current-token');
      await legacy.writeRefreshToken('legacy-stale');

      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      expect(await store.readRefreshToken(), 'current-token');
      // Legacy is left intact on a current-backend hit. (Hygiene cleanup
      // happens on deleteRefreshToken so this remnant gets cleaned up on
      // the next logout.)
      expect(await legacy.readRefreshToken(), 'legacy-stale');
    });

    test(
        'falls back to legacy backend when current is empty and forward-'
        'migrates the value', () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      await legacy.writeRefreshToken('pre-upgrade-token');

      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      expect(
        await store.readRefreshToken(),
        'pre-upgrade-token',
        reason: 'first post-upgrade read should return the legacy value',
      );
      expect(
        await current.readRefreshToken(),
        'pre-upgrade-token',
        reason: 'value should be migrated to the current-options backend',
      );
      expect(
        await legacy.readRefreshToken(),
        isNull,
        reason: 'legacy entry should be cleared after successful migration',
      );
    });

    test(
        'returns null when neither backend has a value, no writes performed',
        () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      expect(await store.readRefreshToken(), isNull);
      expect(await current.readRefreshToken(), isNull);
      expect(await legacy.readRefreshToken(), isNull);
    });

    test(
        'subsequent reads after migration come from current backend (legacy '
        'already cleared)', () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      await legacy.writeRefreshToken('migrating');
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      // First read triggers migration.
      expect(await store.readRefreshToken(), 'migrating');
      // Second read hits the current backend directly.
      expect(await store.readRefreshToken(), 'migrating');
      expect(await legacy.readRefreshToken(), isNull);
    });

    test(
        'when current-backend write fails during migration, legacy value '
        'is still returned (don\'t lose the session)', () async {
      final current = _WriteFailingStore();
      final legacy = InMemoryTokenStore();
      await legacy.writeRefreshToken('precious');
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      // Read returns the legacy value even though the migration write
      // threw. The legacy entry is left intact (delete is gated by a
      // successful write).
      expect(await store.readRefreshToken(), 'precious');
      expect(
        await legacy.readRefreshToken(),
        'precious',
        reason: 'failed migration must not delete legacy data',
      );
    });
  });

  group('FlutterSecureTokenStore.writeRefreshToken', () {
    test('writes only to current backend, leaving legacy untouched',
        () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      await store.writeRefreshToken('new-token');

      expect(await current.readRefreshToken(), 'new-token');
      expect(
        await legacy.readRefreshToken(),
        isNull,
        reason: 'writes do not flow into legacy backend',
      );
    });
  });

  group('FlutterSecureTokenStore BFU timeout (learnings #1)', () {
    const Duration tightTimeout = Duration(milliseconds: 50);

    test(
        'read returns null when the current-backend read hangs past the '
        'op timeout (Android BFU fallback)', () async {
      final current = _HangingStore();
      final legacy = InMemoryTokenStore();
      await legacy.writeRefreshToken('legacy-value');
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
        opTimeout: tightTimeout,
      );

      final result = await store.readRefreshToken();
      expect(
        result,
        'legacy-value',
        reason: 'hanging current read should fall through to the legacy '
            'fallback path after the op timeout fires',
      );
    });

    test(
        'read returns null when BOTH backends hang past the op timeout '
        '(forces re-auth instead of stalling cold-start)', () async {
      final current = _HangingStore();
      final legacy = _HangingStore();
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
        opTimeout: tightTimeout,
      );

      final result = await store.readRefreshToken();
      expect(
        result,
        isNull,
        reason: 'both reads timing out should yield null so bootstrap '
            'falls through to the unauthenticated path instead of hanging',
      );
    });

    test(
        'migration write timeout preserves legacy value AND returns it '
        'so the session survives a slow current-backend write', () async {
      // current.read() returns null fast; current.write() hangs forever.
      final current = _ReadNullWriteHangsStore();
      final legacy = InMemoryTokenStore();
      await legacy.writeRefreshToken('precious');
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
        opTimeout: tightTimeout,
      );

      final result = await store.readRefreshToken();
      expect(result, 'precious');
      expect(
        await legacy.readRefreshToken(),
        'precious',
        reason: 'failed-write migration must not delete legacy data',
      );
    });
  });

  group('FlutterSecureTokenStore.deleteRefreshToken', () {
    test('clears BOTH backends so a logout before first read can\'t leave '
        'a stale legacy entry that re-migrates on next launch', () async {
      final current = InMemoryTokenStore();
      final legacy = InMemoryTokenStore();
      await current.writeRefreshToken('current-token');
      await legacy.writeRefreshToken('legacy-token');
      final store = FlutterSecureTokenStore.fromBackends(
        current: current,
        legacy: legacy,
      );

      await store.deleteRefreshToken();

      expect(await current.readRefreshToken(), isNull);
      expect(await legacy.readRefreshToken(), isNull);
    });
  });
}

/// SecureTokenStore that throws on write but behaves normally on read /
/// delete. Models a Keychain item that can be queried but whose
/// attributes block update (e.g., device just rebooted, item set to
/// AfterFirstUnlock but first-unlock hasn't happened yet).
class _WriteFailingStore implements SecureTokenStore {
  String? _value;

  @override
  Future<String?> readRefreshToken() async => _value;

  @override
  Future<void> writeRefreshToken(String token) async {
    throw StateError('simulated Keychain write failure');
  }

  @override
  Future<void> deleteRefreshToken() async {
    _value = null;
  }
}

/// SecureTokenStore whose every method returns a never-completing
/// Future. Models the Android EncryptedSharedPreferences platform-
/// channel hang under Before-First-Unlock. Used to exercise the
/// op-timeout fallback path in FlutterSecureTokenStore.
class _HangingStore implements SecureTokenStore {
  @override
  Future<String?> readRefreshToken() => Completer<String?>().future;

  @override
  Future<void> writeRefreshToken(String token) =>
      Completer<void>().future;

  @override
  Future<void> deleteRefreshToken() => Completer<void>().future;
}

/// SecureTokenStore that returns null fast on read but hangs on write.
/// Models the migration-time failure where the current backend can be
/// queried (empty) but a write to it stalls — covers the inner timeout
/// inside the legacy-migrate branch.
class _ReadNullWriteHangsStore implements SecureTokenStore {
  @override
  Future<String?> readRefreshToken() async => null;

  @override
  Future<void> writeRefreshToken(String token) =>
      Completer<void>().future;

  @override
  Future<void> deleteRefreshToken() async {}
}
