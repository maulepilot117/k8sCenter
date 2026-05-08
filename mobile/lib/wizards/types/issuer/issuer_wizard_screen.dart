// Issuer / ClusterIssuer wizard screen. One screen drives both routes —
// the `scope` param picks which controller provider to bind, whether
// the namespace input renders, and which wizard type the scaffold
// surfaces.
//
// Step 0 (Type) — two ChoiceChips: SelfSigned or ACME.
// Step 1 (Configure) — name, namespace (when scope is namespaced), then
//   either nothing (SelfSigned) or the ACME form (server preset radio,
//   email, privateKeySecretRefName, solvers RepeatingRowGroup).
// Step 2 (Review) — shared WizardReviewBody.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import '../../wizard_registry.dart';
import 'issuer_wizard_controller.dart';

class IssuerWizardScreen extends ConsumerStatefulWidget {
  const IssuerWizardScreen({super.key, this.scope = WizardScope.namespaced});

  final WizardScope scope;

  @override
  ConsumerState<IssuerWizardScreen> createState() => _IssuerWizardScreenState();
}

class _IssuerWizardScreenState extends ConsumerState<IssuerWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    final isCluster = widget.scope == WizardScope.cluster;
    if (isCluster) {
      return WizardScreenScaffold<IssuerForm>(
        wizardType: 'cluster-issuer',
        title: 'New ClusterIssuer',
        subtitle: 'cluster: ${_wizardKey.clusterId}',
        wizardKey: _wizardKey,
        controllerProvider: clusterIssuerWizardProvider,
        stepBuilders: [
          (ctx) => _TypeStep(
                wizardKey: _wizardKey,
                provider: clusterIssuerWizardProvider,
              ),
          (ctx) => _ConfigureStep(
                wizardKey: _wizardKey,
                provider: clusterIssuerWizardProvider,
                scope: WizardScope.cluster,
              ),
          (ctx) => WizardReviewBody<IssuerForm>(
                wizardKey: _wizardKey,
                controllerProvider: clusterIssuerWizardProvider,
              ),
        ],
      );
    }
    return WizardScreenScaffold<IssuerForm>(
      wizardType: 'issuer',
      title: 'New Issuer',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: issuerWizardProvider,
      stepBuilders: [
        (ctx) => _TypeStep(
              wizardKey: _wizardKey,
              provider: issuerWizardProvider,
            ),
        (ctx) => _ConfigureStep(
              wizardKey: _wizardKey,
              provider: issuerWizardProvider,
              scope: WizardScope.namespaced,
            ),
        (ctx) => WizardReviewBody<IssuerForm>(
              wizardKey: _wizardKey,
              controllerProvider: issuerWizardProvider,
            ),
      ],
    );
  }
}

/// Type-of-issuer picker. Same widget for both scope variants — the
/// generic param threads the active provider through.
class _TypeStep<C extends WizardController<IssuerForm>>
    extends ConsumerWidget {
  const _TypeStep({required this.wizardKey, required this.provider});

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<IssuerForm>, WizardKey>
      provider;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        const WizardSectionHeader(
          'Issuer type',
          subtitle: 'SelfSigned for testing or self-signed CAs; '
              'ACME for Let\'s Encrypt and other ACME-compatible CAs',
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          children: [
            ChoiceChip(
              label: const Text('SelfSigned'),
              selected: state.form.type == IssuerType.selfSigned,
              onSelected: (_) => controller.updateForm(
                  (f) => f.copyWith(type: IssuerType.selfSigned)),
            ),
            ChoiceChip(
              label: const Text('ACME'),
              selected: state.form.type == IssuerType.acme,
              onSelected: (_) => controller
                  .updateForm((f) => f.copyWith(type: IssuerType.acme)),
            ),
          ],
        ),
        if (stepErrors['type'] != null) ...[
          const SizedBox(height: 8),
          Text(
            stepErrors['type']!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        ],
      ],
    );
  }
}

class _ConfigureStep<C extends WizardController<IssuerForm>>
    extends ConsumerWidget {
  const _ConfigureStep({
    required this.wizardKey,
    required this.provider,
    required this.scope,
  });

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<IssuerForm>, WizardKey>
      provider;
  final WizardScope scope;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final stepErrors = state.stepErrors[1] ?? const <String, String>{};
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
            hintText: isCluster ? 'letsencrypt-prod' : 'app-issuer',
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
        const SizedBox(height: 24),
        if (form.type == IssuerType.selfSigned)
          _SelfSignedSummary(stepErrors: stepErrors)
        else
          _AcmeForm(
            wizardKey: wizardKey,
            provider: provider,
            stepErrors: stepErrors,
          ),
      ],
    );
  }
}

class _SelfSignedSummary extends StatelessWidget {
  const _SelfSignedSummary({required this.stepErrors});
  final Map<String, String> stepErrors;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(color: theme.colorScheme.outlineVariant),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'SelfSigned issuer — no further configuration required.',
            style: theme.textTheme.bodyMedium,
          ),
          const SizedBox(height: 4),
          Text(
            'cert-manager mints certificates immediately without an external CA.',
            style: theme.textTheme.bodySmall,
          ),
          if (stepErrors['selfSigned'] != null) ...[
            const SizedBox(height: 8),
            Text(
              stepErrors['selfSigned']!,
              style: TextStyle(color: theme.colorScheme.error),
            ),
          ],
        ],
      ),
    );
  }
}

class _AcmeForm<C extends WizardController<IssuerForm>>
    extends ConsumerWidget {
  const _AcmeForm({
    required this.wizardKey,
    required this.provider,
    required this.stepErrors,
  });

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<C, WizardState<IssuerForm>, WizardKey>
      provider;
  final Map<String, String> stepErrors;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(provider(wizardKey));
    final controller = ref.read(provider(wizardKey).notifier);
    final acme = state.form.acme;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('ACME server'),
        const SizedBox(height: 8),
        for (final preset in kAcmePresets)
          RadioListTile<String>(
            title: Text(preset.label),
            subtitle: Text(preset.url, style: const TextStyle(fontSize: 11)),
            value: preset.url,
            // The controller-level callback (modern Material API) — the
            // group is identified by the shared `groupValue` resolution
            // below via [selected] toggling.
            // ignore: deprecated_member_use
            groupValue: acme.server,
            // ignore: deprecated_member_use
            onChanged: (v) {
              if (v == null) return;
              controller.updateForm(
                  (f) => f.copyWith(acme: f.acme.copyWith(server: v)));
            },
            dense: true,
          ),
        // Custom server URL — the operator can override the preset.
        const SizedBox(height: 8),
        TextFormField(
          initialValue: acme.server,
          decoration: InputDecoration(
            labelText: 'Server URL',
            hintText: 'https://acme-v02.api.letsencrypt.org/directory',
            border: const OutlineInputBorder(),
            errorText: stepErrors['acme.server'],
          ),
          onChanged: (v) => controller.updateForm(
              (f) => f.copyWith(acme: f.acme.copyWith(server: v))),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: acme.email,
          keyboardType: TextInputType.emailAddress,
          decoration: InputDecoration(
            labelText: 'ACME registration email',
            hintText: 'ops@example.com',
            border: const OutlineInputBorder(),
            errorText: stepErrors['acme.email'],
          ),
          onChanged: (v) => controller.updateForm(
              (f) => f.copyWith(acme: f.acme.copyWith(email: v))),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: acme.privateKeySecretRefName,
          decoration: InputDecoration(
            labelText: 'Private key secret name',
            hintText: 'letsencrypt-account-key',
            helperText:
                'cert-manager stores the ACME account private key in this Secret.',
            border: const OutlineInputBorder(),
            errorText: stepErrors['acme.privateKeySecretRefName'],
          ),
          onChanged: (v) => controller.updateForm((f) =>
              f.copyWith(acme: f.acme.copyWith(privateKeySecretRefName: v))),
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Solvers',
          subtitle: 'Each row is one HTTP01 ingress solver. '
              'DNS01 is not supported in this wizard — use the YAML editor.',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<AcmeSolver>(
          items: acme.solvers,
          addLabel: 'Add solver',
          onAdd: () => controller.updateForm((f) => f.copyWith(
                acme: f.acme.copyWith(
                  solvers: [...f.acme.solvers, const AcmeSolver()],
                ),
              )),
          onRemove: (i) {
            final next = [...acme.solvers]..removeAt(i);
            controller.updateForm(
                (f) => f.copyWith(acme: f.acme.copyWith(solvers: next)));
          },
          itemBuilder: (ctx, i, s) => _SolverRow(
            initialClassName: s.ingressClassName,
            error: stepErrors['acme.solvers[$i].http01Ingress.ingressClassName'],
            onChanged: (v) {
              final next = [...acme.solvers];
              next[i] = next[i].copyWith(ingressClassName: v);
              controller.updateForm(
                  (f) => f.copyWith(acme: f.acme.copyWith(solvers: next)));
            },
          ),
          errorMessage: stepErrors['acme.solvers'],
        ),
      ],
    );
  }
}

class _SolverRow extends StatefulWidget {
  const _SolverRow({
    required this.initialClassName,
    required this.error,
    required this.onChanged,
  });

  final String initialClassName;
  final String? error;
  final ValueChanged<String> onChanged;

  @override
  State<_SolverRow> createState() => _SolverRowState();
}

class _SolverRowState extends State<_SolverRow> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.initialClassName);

  @override
  void didUpdateWidget(covariant _SolverRow old) {
    super.didUpdateWidget(old);
    if (widget.initialClassName != _ctl.text &&
        widget.initialClassName != old.initialClassName) {
      _ctl.text = widget.initialClassName;
    }
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: _ctl,
      decoration: InputDecoration(
        labelText: 'Ingress class (optional)',
        hintText: 'nginx',
        isDense: true,
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
      onChanged: widget.onChanged,
    );
  }
}
