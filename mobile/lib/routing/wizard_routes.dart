// Wizard route registration. Mirrors how the resource list/detail
// routes live next to their screens — pulled into a separate file so
// wizard screens are obvious additions without bloating app_router.dart.
//
// Path shape:
//   /clusters/:clusterId/wizards/:type/new
//
// `:type` matches the entries in `wizard_registry.dart`. When a type
// is in the registry but no route exists here, the router falls
// through to a "Coming soon" screen so the drawer never deep-links to
// a 404. PRs 3b–3e replace those with concrete screens.
//
// RBAC gating: the drawer's "Create" submenu already filters by
// [visibleWizards]; this route builder re-checks the same predicate so
// a deep link (notification, paste, history) cannot bypass the gate.
// Unauthorized navigation surfaces a 403 screen instead of opening the
// wizard. Mirrors the web frontend's pattern of gating both the nav
// surface AND the route handler.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../auth/permissions.dart';
import '../theme/kube_theme_builder.dart';
import '../wizards/types/certificate/certificate_wizard_screen.dart';
import '../wizards/types/configmap/configmap_wizard_screen.dart';
import '../wizards/types/cronjob/cronjob_wizard_screen.dart';
import '../wizards/types/daemonset/daemonset_wizard_screen.dart';
import '../wizards/types/deployment/deployment_wizard_screen.dart';
import '../wizards/types/external_secret/external_secret_wizard_screen.dart';
import '../wizards/types/hpa/hpa_wizard_screen.dart';
import '../wizards/types/ingress/ingress_wizard_screen.dart';
import '../wizards/types/issuer/issuer_wizard_screen.dart';
import '../wizards/types/job/job_wizard_screen.dart';
import '../wizards/types/namespace_limits/namespace_limits_wizard_screen.dart';
import '../wizards/types/networkpolicy/networkpolicy_wizard_screen.dart';
import '../wizards/types/pdb/pdb_wizard_screen.dart';
import '../wizards/types/policy/policy_wizard_screen.dart';
import '../wizards/types/pvc/pvc_wizard_screen.dart';
import '../wizards/types/restore_snapshot/restore_snapshot_wizard_screen.dart';
import '../wizards/types/rolebinding/rolebinding_wizard_screen.dart';
import '../wizards/types/scheduled_snapshot/scheduled_snapshot_wizard_screen.dart';
import '../wizards/types/secret/secret_wizard_screen.dart';
import '../wizards/types/secret_store/secret_store_wizard_screen.dart';
import '../wizards/types/service/service_wizard_screen.dart';
import '../wizards/types/snapshot/snapshot_wizard_screen.dart';
import '../wizards/types/statefulset/statefulset_wizard_screen.dart';
import '../wizards/types/storageclass/storageclass_wizard_screen.dart';
import '../wizards/types/velero_backup/velero_backup_wizard_screen.dart';
import '../wizards/types/velero_restore/velero_restore_wizard_screen.dart';
import '../wizards/types/velero_schedule/velero_schedule_wizard_screen.dart';
import '../wizards/wizard_registry.dart';

/// Public list of wizard routes — caller (app_router.dart) appends
/// these to its top-level routes list.
final List<GoRoute> wizardRoutes = [
  GoRoute(
    path: '/clusters/:clusterId/wizards/:type/new',
    builder: (context, state) {
      final type = state.pathParameters['type'] ?? '';
      return _WizardRouteGuard(
        type: type,
        child: _wizardScreenForType(type),
      );
    },
  ),
];

/// Look up the concrete wizard screen for a registered type, falling
/// back to a "Coming soon" placeholder when no screen exists yet (PRs
/// 3b–3e fill these in). Single switch block keeps the registry / route
/// mapping in one searchable spot.
Widget _wizardScreenForType(String type) {
  switch (type) {
    case 'configmap':
      return const ConfigMapWizardScreen();
    case 'secret':
      return const SecretWizardScreen();
    case 'service':
      return const ServiceWizardScreen();
    case 'deployment':
      return const DeploymentWizardScreen();
    case 'job':
      return const JobWizardScreen();
    case 'cronjob':
      return const CronJobWizardScreen();
    case 'daemonset':
      return const DaemonSetWizardScreen();
    case 'statefulset':
      return const StatefulSetWizardScreen();
    case 'ingress':
      return const IngressWizardScreen();
    case 'networkpolicy':
      return const NetworkPolicyWizardScreen();
    case 'hpa':
      return const HpaWizardScreen();
    case 'pdb':
      return const PdbWizardScreen();
    case 'rolebinding':
      return const RoleBindingWizardScreen();
    case 'storageclass':
      return const StorageClassWizardScreen();
    case 'namespace-limits':
      return const NamespaceLimitsWizardScreen();
    case 'pvc':
      return const PvcWizardScreen();
    case 'snapshot':
      return const SnapshotWizardScreen();
    case 'scheduled-snapshot':
      return const ScheduledSnapshotWizardScreen();
    case 'restore-snapshot':
      return const RestoreSnapshotWizardScreen();
    case 'velero-backup':
      return const VeleroBackupWizardScreen();
    case 'velero-restore':
      return const VeleroRestoreWizardScreen();
    case 'velero-schedule':
      return const VeleroScheduleWizardScreen();
    case 'certificate':
      return const CertificateWizardScreen();
    case 'issuer':
      return const IssuerWizardScreen(scope: WizardScope.namespaced);
    case 'cluster-issuer':
      return const IssuerWizardScreen(scope: WizardScope.cluster);
    case 'external-secret':
      return const ExternalSecretWizardScreen();
    case 'secret-store':
      return const SecretStoreWizardScreen(scope: WizardScope.namespaced);
    case 'cluster-secret-store':
      return const SecretStoreWizardScreen(scope: WizardScope.cluster);
    case 'policy':
      return const PolicyWizardScreen();
    default:
      return _ComingSoonScreen(type: type);
  }
}

/// Gates wizard navigation on RBAC. Drawer already filters via
/// [visibleWizards]; this guard fires when an operator deep-links or
/// pastes a wizard URL outside the drawer flow. If the type is
/// unregistered we still let the underlying screen render (the
/// `_ComingSoonScreen` is informational, not RBAC-sensitive); if the
/// type IS registered and the operator lacks `create` on the
/// produced kind, we render a 403 screen instead.
class _WizardRouteGuard extends ConsumerWidget {
  const _WizardRouteGuard({required this.type, required this.child});

  final String type;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final entry = findWizardEntry(type);
    if (entry == null) {
      // Unregistered type — child is `_ComingSoonScreen`, which is
      // safe to render without RBAC.
      return child;
    }
    final auth = ref.watch(authRepositoryProvider);
    final rbac = auth is AuthAuthenticated ? auth.rbac : null;
    // We don't know which namespace the operator intends to write
    // into until the form fills, so the route-level check uses the
    // any-namespace fallback (matching the drawer's `_CreateSubmenu`).
    // The wizard's apply-time backend call is the final authority; an
    // operator who has create in *some* namespace is allowed to open
    // the form and pick a namespace they have permission for. The
    // server returns 403 if they pick wrong.
    // Namespace is unknown at route time (the form picks it). Pass
    // empty so canPerform's `allowAnyNamespaceFallback` toggle drives
    // the decision: namespaced wizards (`requiresNamespace: true`) ->
    // permit if the operator has create on the kind in *any*
    // namespace; cluster-scoped wizards -> permit only if they have
    // cluster-scoped create.
    final permitted = canPerform(
      rbac,
      entry.kind,
      entry.createVerb,
      '',
      allowAnyNamespaceFallback: entry.requiresNamespace,
    );
    if (!permitted) {
      return _ForbiddenScreen(entry: entry);
    }
    return child;
  }
}

/// Placeholder for wizard types registered in `wizard_registry.dart`
/// that don't yet have a screen. Renders a clear "ships later"
/// message rather than a blank page.
class _ComingSoonScreen extends StatelessWidget {
  const _ComingSoonScreen({required this.type});

  final String type;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Scaffold(
      appBar: AppBar(title: Text('Wizard: $type')),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.hourglass_top_outlined,
                  size: 48, color: colors.textMuted),
              const SizedBox(height: 16),
              Text(
                'Coming soon',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'The "$type" wizard ships in a later mobile PR. Open '
                'k8sCenter on a desktop to create this resource for now.',
                textAlign: TextAlign.center,
                style: TextStyle(color: colors.textSecondary),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// 403-equivalent for wizard deep-links the operator's RBAC denies.
/// Backend would reject the eventual write anyway; surfacing the
/// denial up front avoids the operator filling out the form and
/// hitting an apply-time 403.
class _ForbiddenScreen extends StatelessWidget {
  const _ForbiddenScreen({required this.entry});

  final WizardEntry entry;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Scaffold(
      appBar: AppBar(title: Text('Wizard: ${entry.label}')),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.lock_outline, size: 48, color: colors.error),
              const SizedBox(height: 16),
              Text(
                'Permission denied',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'You don\'t have permission to create ${entry.kind} on '
                'the active cluster. Contact your administrator if you '
                'expected access.',
                textAlign: TextAlign.center,
                style: TextStyle(color: colors.textSecondary),
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: () => context.go('/'),
                child: const Text('Go to Dashboard'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
