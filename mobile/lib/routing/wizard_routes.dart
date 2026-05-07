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

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../theme/kube_theme_builder.dart';
import '../wizards/types/configmap/configmap_wizard_screen.dart';
import '../wizards/types/secret/secret_wizard_screen.dart';
import '../wizards/types/service/service_wizard_screen.dart';

/// Public list of wizard routes — caller (app_router.dart) appends
/// these to its top-level routes list.
final List<GoRoute> wizardRoutes = [
  GoRoute(
    path: '/clusters/:clusterId/wizards/:type/new',
    builder: (context, state) {
      final type = state.pathParameters['type'] ?? '';
      switch (type) {
        case 'configmap':
          return const ConfigMapWizardScreen();
        case 'secret':
          return const SecretWizardScreen();
        case 'service':
          return const ServiceWizardScreen();
        default:
          return _ComingSoonScreen(type: type);
      }
    },
  ),
];

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
