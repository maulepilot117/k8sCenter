// go_router config + redirect guard.
//
// Two routes in PR-1b: `/login` and `/`. The redirect guard sends
// unauthenticated users to /login, and authenticated users away from
// /login back to /. AuthRepository is the source of truth — the guard
// rebuilds whenever auth state transitions.
//
// PR-1c+ adds resource routes that the guard treats the same way as `/`.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../features/dashboard/dashboard_placeholder.dart';
import '../features/login/login_screen.dart';
import '../features/settings/theme_picker_sheet.dart';
import '../widgets/adaptive_scaffold.dart';
import '../widgets/empty_states.dart';

final appRouterProvider = Provider<GoRouter>((ref) {
  // Listening to authRepositoryProvider rebuilds the router on transitions.
  final authState = ref.watch(authRepositoryProvider);

  return GoRouter(
    initialLocation: '/',
    refreshListenable: _AuthListenable(ref),
    redirect: (context, state) {
      final loggedIn = authState is AuthAuthenticated;
      final initializing = authState is AuthInitializing;
      final atLogin = state.matchedLocation == '/login';

      if (initializing) return null; // splash screen handles this state
      if (!loggedIn && !atLogin) return '/login';
      if (loggedIn && atLogin) return '/';
      return null;
    },
    routes: [
      GoRoute(
        path: '/login',
        builder: (context, state) => const LoginScreen(),
      ),
      GoRoute(
        path: '/',
        builder: (context, state) => const _RootScreen(),
      ),
    ],
  );
});

class _RootScreen extends ConsumerWidget {
  const _RootScreen();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final authState = ref.watch(authRepositoryProvider);

    if (authState is AuthInitializing) {
      return const Scaffold(body: LoadingState(message: 'Starting up'));
    }

    return AdaptiveScaffold(
      title: 'k8sCenter',
      body: const DashboardPlaceholder(),
      actions: [
        IconButton(
          icon: const Icon(Icons.palette_outlined),
          tooltip: 'Theme',
          onPressed: () => ThemePickerSheet.show(context),
        ),
        IconButton(
          icon: const Icon(Icons.logout),
          tooltip: 'Sign out',
          onPressed: () =>
              ref.read(authRepositoryProvider.notifier).logout(),
        ),
      ],
    );
  }
}

/// Bridges Riverpod's auth state into a [Listenable] that go_router watches.
/// Without this, go_router doesn't recompute its redirect when auth state
/// changes after a login or logout.
class _AuthListenable extends ChangeNotifier {
  _AuthListenable(this._ref) {
    _sub = _ref.listen<AuthState>(
      authRepositoryProvider,
      (_, _) => notifyListeners(),
    );
  }

  final Ref _ref;
  late final ProviderSubscription<AuthState> _sub;

  @override
  void dispose() {
    _sub.close();
    super.dispose();
  }
}
