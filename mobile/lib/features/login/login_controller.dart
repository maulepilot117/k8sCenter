// Login screen controller — fetches available credential providers once on
// mount, exposes them via a FutureProvider, and proxies login/logout to
// AuthRepository.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/auth_repository.dart';

/// Cached provider list. Refreshed when the login screen mounts.
final authProvidersProvider = FutureProvider<List<AuthProvider>>((ref) async {
  final repo = ref.read(authRepositoryProvider.notifier);
  return repo.listProviders();
});
