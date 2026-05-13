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
import '../features/notifications_center/feed_screen.dart';
import '../features/gitops/application_detail_screen.dart';
import '../features/gitops/applications_list_screen.dart';
import '../features/gitops/applicationset_detail_screen.dart';
import '../features/gitops/applicationsets_list_screen.dart';
import '../features/mesh/mesh_dashboard_screen.dart';
import '../features/mesh/mtls_posture_screen.dart';
import '../features/mesh/route_detail_screen.dart';
import '../features/mesh/routing_list_screen.dart';
import '../features/observability/diagnostics/diagnostics_screen.dart';
import '../features/observability/diagnostics/namespace_summary_screen.dart';
import '../features/observability/logs/log_search_screen.dart';
import '../features/observability/logs/log_tail_screen.dart';
import '../notifications/deep_link_handler.dart';
import '../notifications/fcm_registration.dart';
import '../features/resources/configmap_screens.dart';
import '../features/resources/daemonset_screens.dart';
import '../features/resources/deployment_screens.dart';
import '../features/resources/generic_detail_screen.dart';
import '../features/resources/ingress_screens.dart';
import '../features/resources/namespace_screens.dart';
import '../features/resources/node_screens.dart';
import '../features/resources/pod_screens.dart';
import '../features/resources/pvc_screens.dart';
import '../features/resources/replicaset_screens.dart';
import '../features/resources/rollback_picker_screen.dart';
import '../features/resources/secret_screens.dart';
import '../features/resources/service_screens.dart';
import '../features/resources/statefulset_screens.dart';
import '../features/settings/theme_picker_sheet.dart';
import '../widgets/adaptive_scaffold.dart';
import '../widgets/cluster_pill.dart';
import '../widgets/domain_navigation_drawer.dart';
import '../widgets/empty_states.dart';
import 'domain_sections.dart';
import 'wizard_routes.dart';

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

      // --- PR-1f: notification feed + pod log tail ---
      GoRoute(
        path: '/notifications',
        builder: (context, state) => const NotificationFeedScreen(),
      ),
      GoRoute(
        path: '/clusters/:clusterId/workloads/pods/:namespace/:name/logs/:container',
        builder: (context, state) => LogTailScreen(
          namespace: state.pathParameters['namespace']!,
          pod: state.pathParameters['name']!,
          container: state.pathParameters['container']!,
        ),
      ),

      // --- M4 PR-4c: top-level LogQL editor (multi-pod ad-hoc search).
      // Separate from the M1 single-pod live tail above so neither
      // surface has to compromise on UX. `?namespace=` query param
      // seeds the filter bar for deep links from notifications.
      GoRoute(
        path: '/clusters/:clusterId/logs',
        builder: (context, state) {
          final ns = state.uri.queryParameters['namespace'];
          return LogSearchScreen(
            initialNamespace: ns == null || ns.isEmpty ? null : ns,
          );
        },
      ),

      // --- M4 PR-4d: diagnostics blast-radius surface ---
      // Per-resource diagnostics: `/clusters/<id>/diagnostics/<ns>/<kind>/<name>`.
      // `<kind>` is the canonical Kubernetes Kind ("Pod", "Deployment", ...)
      // so the URL matches the backend's `/v1/diagnostics/{ns}/{kind}/{name}`
      // path param shape exactly.
      //
      // Namespace summary lives one segment up at
      // `/clusters/<id>/diagnostics/<ns>/summary`. The `summary` literal
      // collides with a kind named "summary" in theory, but Kubernetes
      // Kind names are PascalCase identifiers — so "summary" cannot exist
      // as a real kind. The literal-route is matched before the
      // parametrised one because go_router prefers more specific paths.
      GoRoute(
        path: '/clusters/:clusterId/diagnostics/:namespace/summary',
        builder: (context, state) => NamespaceSummaryScreen(
          namespace: state.pathParameters['namespace']!,
        ),
      ),
      GoRoute(
        path: '/clusters/:clusterId/diagnostics/:namespace/:kind/:name',
        builder: (context, state) => DiagnosticsScreen(
          namespace: state.pathParameters['namespace']!,
          kind: state.pathParameters['kind']!,
          name: state.pathParameters['name']!,
        ),
      ),

      // --- GitOps detail surfaces (Argo + Flux + AppSets) ---
      // Applications and ApplicationSets routes are flat under
      // `/clusters/<id>/gitops/{applications,applicationsets}[/<id>]`.
      // The `:id` slot is `Uri.encodeComponent(app.id)` — a composite
      // `tool:ns:name` tuple percent-encoded once. The detail screens
      // round-trip via [GitOpsId.tryParse] in their builders.
      //
      // go_router strips one layer of encoding from the matched segment
      // before handing it to the builder, so the builder sees the raw
      // `tool:ns:name` string. Double-encoding via [GitOpsId.encode]
      // would break this.
      GoRoute(
        path: '/clusters/:clusterId/gitops/applications',
        builder: (context, state) => const ApplicationsListScreen(),
        routes: [
          GoRoute(
            path: ':id',
            builder: (context, state) => ApplicationDetailScreen(
              id: state.pathParameters['id']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/gitops/applicationsets',
        builder: (context, state) => const ApplicationSetsListScreen(),
        routes: [
          GoRoute(
            path: ':id',
            builder: (context, state) => ApplicationSetDetailScreen(
              id: state.pathParameters['id']!,
            ),
          ),
        ],
      ),

      // --- M4 PR-4f: service-mesh surfaces (Istio + Linkerd) ---
      // /clusters/<id>/mesh                       → dashboard
      // /clusters/<id>/mesh/routing[?mesh=istio]  → routing list
      // /clusters/<id>/mesh/routing/<encoded-id>  → route detail
      // /clusters/<id>/mesh/mtls[?namespace=]     → mTLS posture
      //
      // Composite ids on routing detail follow the
      // `mesh:namespace:kindCode:name` shape; the route param carries
      // one layer of percent encoding. The detail screen parses via
      // [MeshRouteId.tryParse] and emits a validation error screen on
      // malformed input rather than 500-ing.
      //
      // Status-gating is screen-level (each surface checks
      // `meshStatusProvider` and falls back to
      // `FeatureUnavailableState.mesh()`) rather than at the router so
      // the drawer entries stay visible and operators get the
      // install-guidance UX consistently.
      GoRoute(
        path: '/clusters/:clusterId/mesh',
        builder: (context, state) => const MeshDashboardScreen(),
        routes: [
          GoRoute(
            path: 'routing',
            builder: (context, state) => MeshRoutingListScreen(
              initialMesh: state.uri.queryParameters['mesh'],
            ),
            routes: [
              GoRoute(
                path: ':id',
                builder: (context, state) => MeshRouteDetailScreen(
                  id: state.pathParameters['id']!,
                ),
              ),
            ],
          ),
          GoRoute(
            path: 'mtls',
            builder: (context, state) => MeshMtlsPostureScreen(
              initialNamespace: state.uri.queryParameters['namespace'],
            ),
          ),
        ],
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
            routes: [
              GoRoute(
                path: 'rollback',
                builder: (context, state) => RollbackPickerScreen(
                  namespace: state.pathParameters['namespace']!,
                  name: state.pathParameters['name']!,
                ),
              ),
            ],
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

      // --- PR-1e: 6 additional specialized kinds ---
      GoRoute(
        path: '/clusters/:clusterId/workloads/replicasets',
        builder: (context, state) => const ReplicaSetListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => ReplicaSetDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/workloads/statefulsets',
        builder: (context, state) => const StatefulSetListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => StatefulSetDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/workloads/daemonsets',
        builder: (context, state) => const DaemonSetListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => DaemonSetDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/networking/ingresses',
        builder: (context, state) => const IngressListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => IngressDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/storage/pvcs',
        builder: (context, state) => const PvcListScreen(),
        routes: [
          GoRoute(
            path: ':namespace/:name',
            builder: (context, state) => PvcDetailScreen(
              namespace: state.pathParameters['namespace']!,
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),
      GoRoute(
        path: '/clusters/:clusterId/cluster/namespaces',
        builder: (context, state) => const NamespaceListScreen(),
        routes: [
          // Cluster-scoped — single :name segment.
          GoRoute(
            path: ':name',
            builder: (context, state) => NamespaceDetailScreen(
              name: state.pathParameters['name']!,
            ),
          ),
        ],
      ),

      // --- M3 PR-3a: wizard routes (ConfigMap, Secret, Service; later
      //     PRs replace the ComingSoon placeholder with concrete screens
      //     for the rest of the registry).
      ...wizardRoutes,

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

    // Drain pending deep links captured by FCM listeners. The link is
    // queued before the router was ready (cold-start tap or background
    // resume); now that we've reached the dashboard, parse it and push
    // onto the navigation stack so the targeted resource opens.
    ref.listen<Uri?>(pendingDeepLinkProvider, (prev, next) {
      if (next == null) return;
      final parsed = kDeepLinkHandler.parse(next);
      if (parsed.isValid) {
        // Defer push to next frame so the AdaptiveScaffold has mounted
        // and go_router has a stable route stack to push onto.
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!context.mounted) return;
          context.push(parsed.path!);
        });
      }
      // Clear regardless — invalid links must not leak to next launch.
      ref.read(pendingDeepLinkProvider.notifier).state = null;
    });

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
