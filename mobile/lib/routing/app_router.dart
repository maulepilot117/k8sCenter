// go_router config + redirect guard.
//
// PR-1d adds resource list/detail routes for the 6 specialized kinds
// plus a generic detail fallback. Routes are flat under
// `/clusters/:clusterId/<domain>/<kind>[/:namespace/:name]` mirroring
// the web frontend's URL shape so deep links land identically.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../features/dashboard/dashboard_screen.dart';
import '../features/login/login_screen.dart';
import '../features/resources/configmap_screens.dart';
import '../features/resources/deployment_screens.dart';
import '../features/resources/generic_detail_screen.dart';
import '../features/resources/node_screens.dart';
import '../features/resources/pod_screens.dart';
import '../features/resources/secret_screens.dart';
import '../features/resources/service_screens.dart';
import '../features/settings/theme_picker_sheet.dart';
import '../widgets/adaptive_scaffold.dart';
import '../widgets/cluster_pill.dart';
import '../widgets/domain_navigation_drawer.dart';
import '../widgets/empty_states.dart';
import 'domain_sections.dart';

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

      // --- Resource list routes (PR-1d: 6 specialized kinds) ---
      GoRoute(
        path: '/clusters/:clusterId/workloads/pods',
        builder: (context, state) => const PodListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => PodDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/workloads/deployments',
        builder: (context, state) => const DeploymentListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => DeploymentDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/networking/services',
        builder: (context, state) => const ServiceListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => ServiceDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/config/configmaps',
        builder: (context, state) => const ConfigMapListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => ConfigMapDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/config/secrets',
        builder: (context, state) => const SecretListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => SecretDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/cluster/nodes',
        builder: (context, state) => const NodeListScreen(),
        routes: [
          // Node is cluster-scoped — single :name segment.
          GoRoute(
            path: ':name',
            builder: (context, state) => NodeDetailScreen(
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),

      // --- Generic detail fallback for any unspecialized kind ---
      // Path shape: /clusters/<id>/generic/<kind>/<namespace>/<name>
      // (cluster-scoped uses [clusterScopedNamespaceSentinel] = '_'
      // because DNS-1123 labels can't start with underscore so it can't
      // collide with a real namespace name).
      GoRoute(
        path: '/clusters/:clusterId/generic/:kind/:namespace/:name',
        builder: (context, state) {
          final ns = state.pathParameters['namespace']!;
          return GenericDetailScreen(
            kind: state.pathParameters['kind']!,
            namespace: ns == clusterScopedNamespaceSentinel ? '' : ns,
            name: state.pathParameters['name']!,
          );
        },
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
      body: const DashboardScreen(),
      drawer: const DomainNavigationDrawer(),
      actions: [
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 8),
          child: ClusterPill(),
        ),
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
