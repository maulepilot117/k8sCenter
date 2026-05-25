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
