// SecretStore / ClusterSecretStore wizard screen. One screen drives
// both routes — the `scope` param picks which controller provider to
// bind, whether the namespace input renders, and which wizard type
// the scaffold surfaces.
//
// Step 0 (Identity) — name + namespace (when scope is namespaced) +
//   refreshInterval.
// Step 1 (Provider) — provider picker (8 providers).
// Step 2 (Configure) — provider-specific form. Each provider has its
//   own form file under `providers/`; this screen dispatches via
//   [_providerFormFor]. When the provider has no form (cluster operator
//   typed an unsupported id, or the registry is mid-migration), an
//   info banner points to the YAML editor.
// Step 3 (Review) — shared WizardReviewBody.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/duration_input.dart';
import '../../widgets/provider_picker.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import '../../wizard_registry.dart';
import 'providers/aws_provider_form.dart';
import 'providers/awsps_provider_form.dart';
import 'providers/azurekv_provider_form.dart';
import 'providers/doppler_provider_form.dart';
import 'providers/gcpsm_provider_form.dart';
import 'providers/kubernetes_provider_form.dart';
import 'providers/onepassword_provider_form.dart';
import 'providers/provider_form.dart';
import 'providers/vault_provider_form.dart';
import 'secret_store_wizard_controller.dart';

/// Map provider id → form builder. Adding a new provider amounts to
/// adding a row here plus an import. Keeping the table here (rather
/// than in `provider_picker.dart`) keeps the picker decoupled from the
/// per-form imports.
const Map<String, ProviderFormBuilder> _kProviderForms = {
  'vault': vaultProviderForm,
  'aws': awsProviderForm,
  'awsps': awspsProviderForm,
  'azurekv': azurekvProviderForm,
  'gcpsm': gcpsmProviderForm,
  'kubernetes': kubernetesProviderForm,
  'doppler': dopplerProviderForm,
  'onepassword': onepasswordProviderForm,
};

class SecretStoreWizardScreen extends ConsumerStatefulWidget {
  const SecretStoreWizardScreen({
    super.key,
    this.scope = WizardScope.namespaced,
  });

  final WizardScope scope;

  @override
  ConsumerState<SecretStoreWizardScreen> createState() =>
      _SecretStoreWizardScreenState();
}

class _SecretStoreWizardScreenState
    extends ConsumerState<SecretStoreWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return widget.scope == WizardScope.cluster
        ? _buildScaffold(
            wizardType: 'cluster-secret-store',
            title: 'New ClusterSecretStore',
            provider: clusterSecretStoreWizardProvider,
            scope: WizardScope.cluster,
          )
        : _buildScaffold(
            wizardType: 'secret-store',
            title: 'New SecretStore',
            provider: secretStoreWizardProvider,
            scope: WizardScope.namespaced,
          );
  }

  Widget _buildScaffold<C extends WizardController<SecretStoreForm>>({
    required String wizardType,
    required String title,
    required AutoDisposeNotifierProviderFamily<C, WizardState<SecretStoreForm>,
            WizardKey>
        provider,
    required WizardScope scope,
  }) {
    return WizardScreenScaffold<SecretStoreForm>(
      wizardType: wizardType,
      title: title,
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: provider,
      stepBuilders: [
        (ctx) => _IdentityStep(
              wizardKey: _wizardKey,
              provider: provider,
              scope: scope,
            ),
        (ctx) => _ProviderStep(wizardKey: _wizardKey, provider: provider),
        (ctx) => _ConfigureStep(wizardKey: _wizardKey, provider: provider),
        (ctx) => WizardReviewBody<SecretStoreForm>(
              wizardKey: _wizardKey,
              controllerProvider: provider,
            ),
      ],
    );
  }
}

class _IdentityStep<C extends WizardController<SecretStoreForm>>
    extends ConsumerWidget {
  const _IdentityStep({
    required this.wizardKey,
    required this.provider,
    required this.scope,
  });

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<SecretStoreForm>,
      WizardKey> provider;
  final WizardScope scope;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final form = state.form;
    final isCluster = scope == WizardScope.cluster;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: isCluster ? 'shared-vault' : 'app-vault',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        if (!isCluster) ...[
          const SizedBox(height: 16),
          TextFormField(
            initialValue: form.namespace,
            decoration: InputDecoration(
              labelText: 'Namespace',
              hintText: 'app',
              border: const OutlineInputBorder(),
              errorText: stepErrors['namespace'],
            ),
            onChanged: (v) =>
                controller.updateForm((f) => f.copyWith(namespace: v)),
          ),
        ],
        const SizedBox(height: 16),
        DurationInput(
          label: 'Default refresh interval (optional)',
          value: form.refreshInterval,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(refreshInterval: v)),
          hintText: 'e.g. 1h',
          errorText: stepErrors['refreshInterval'],
        ),
      ],
    );
  }
}

class _ProviderStep<C extends WizardController<SecretStoreForm>>
    extends ConsumerWidget {
  const _ProviderStep({required this.wizardKey, required this.provider});

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<SecretStoreForm>,
      WizardKey> provider;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final stepErrors = state.stepErrors[1] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        ProviderPicker(
          selected: state.form.provider,
          onChanged: (id) =>
              (controller as SecretStoreWizardBase).switchProvider(id),
          errorMessage:
              stepErrors['provider'] ?? stepErrors['providerSpec'],
        ),
      ],
    );
  }
}

class _ConfigureStep<C extends WizardController<SecretStoreForm>>
    extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey, required this.provider});

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<SecretStoreForm>,
      WizardKey> provider;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final stepErrors = state.stepErrors[2] ?? const <String, String>{};
    final form = state.form;

    if (form.provider.isEmpty) {
      // Defensive: the controller's validateLocally on step 1 should
      // prevent advance, but if a server error pulls the operator
      // back here without a provider picked, surface the situation
      // clearly rather than rendering an empty Configure body.
      return Padding(
        padding: const EdgeInsets.all(12),
        child: Text(
          'Pick a provider on the previous step before configuring.',
          style: TextStyle(
            color: Theme.of(context).extension<KubeColors>()!.textMuted,
          ),
        ),
      );
    }

    final builder = _kProviderForms[form.provider];
    if (builder == null) {
      return _NoProviderFormFallback(provider: form.provider);
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        WizardSectionHeader(
          'Configure ${form.provider}',
          subtitle: 'Provider-specific connection and authentication',
        ),
        const SizedBox(height: 8),
        builder(ProviderFormProps(
          spec: form.providerSpec,
          errors: stepErrors,
          onUpdateSpec: (next) =>
              (controller as SecretStoreWizardBase).updateProviderSpec(next),
        )),
      ],
    );
  }
}

class _NoProviderFormFallback extends StatelessWidget {
  const _NoProviderFormFallback({required this.provider});

  final String provider;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.info_outline, color: colors.accent),
          const SizedBox(height: 8),
          Text(
            'No guided form for "$provider"',
            style: TextStyle(
              color: colors.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'This provider is recognized by ESO but has no guided form '
            'in this wizard. Use the YAML editor on a desktop to '
            'configure it, or pick a different provider.',
            style: TextStyle(color: colors.textSecondary),
          ),
        ],
      ),
    );
  }
}
