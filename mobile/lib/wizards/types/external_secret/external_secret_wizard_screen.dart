// ExternalSecret wizard screen. One Configure step covering name +
// namespace + store ref + target secret name + refresh interval +
// repeating data rows. Review delegates to WizardReviewBody.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/duration_input.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/store_picker.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'external_secret_wizard_controller.dart';

class ExternalSecretWizardScreen extends ConsumerStatefulWidget {
  const ExternalSecretWizardScreen({super.key});

  @override
  ConsumerState<ExternalSecretWizardScreen> createState() =>
      _ExternalSecretWizardScreenState();
}

class _ExternalSecretWizardScreenState
    extends ConsumerState<ExternalSecretWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<ExternalSecretForm>(
      wizardType: 'external-secret',
      title: 'New ExternalSecret',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: externalSecretWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<ExternalSecretForm>(
              wizardKey: _wizardKey,
              controllerProvider: externalSecretWizardProvider,
            ),
      ],
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(externalSecretWizardProvider(wizardKey));
    final controller =
        ref.read(externalSecretWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final form = state.form;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'db-creds',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'app',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) {
            // Namespace change invalidates a prior namespaced store
            // pick — same defense as the certificate wizard's
            // namespace→issuer reset. ClusterSecretStore picks would
            // technically still resolve, but clearing forces the
            // operator to re-confirm in the new namespace context.
            controller.updateForm(
                (f) => f.copyWith(namespace: v, clearStoreRef: true));
          },
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: form.targetSecretName,
          decoration: InputDecoration(
            labelText: 'Target secret name',
            hintText: 'db-creds',
            helperText:
                'ESO writes the synced Secret to this name in the same namespace.',
            border: const OutlineInputBorder(),
            errorText: stepErrors['targetSecretName'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(targetSecretName: v)),
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Source store',
          subtitle: 'The SecretStore (namespaced) or ClusterSecretStore '
              'this ExternalSecret pulls from',
        ),
        const SizedBox(height: 8),
        StorePicker(
          clusterId: wizardKey.clusterId,
          namespace: form.namespace,
          selected: form.storeRef,
          onChanged: (sel) =>
              controller.updateForm((f) => f.copyWith(storeRef: sel)),
          label: 'Store',
          errorMessage: stepErrors['storeRef.name'] ??
              stepErrors['storeRef.kind'] ??
              stepErrors['storeRef'],
        ),
        const SizedBox(height: 16),
        DurationInput(
          label: 'Refresh interval',
          value: form.refreshInterval,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(refreshInterval: v)),
          hintText: 'e.g. 1h',
          errorText: stepErrors['refreshInterval'],
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Data',
          subtitle: 'Each row maps a key in the source store to a key in '
              'the target Kubernetes Secret.',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<EsoDataItem>(
          items: form.data,
          addLabel: 'Add data item',
          onAdd: () => controller.updateForm(
              (f) => f.copyWith(data: [...f.data, const EsoDataItem()])),
          onRemove: (i) {
            final next = [...form.data]..removeAt(i);
            controller.updateForm((f) => f.copyWith(data: next));
          },
          itemBuilder: (ctx, i, item) => _DataRow(
            initial: item,
            errorSecretKey: stepErrors['data[$i].secretKey'],
            errorRemoteKey: stepErrors['data[$i].remoteRef.key'],
            errorRemoteProperty:
                stepErrors['data[$i].remoteRef.property'],
            onChanged: (next) {
              final list = [...form.data];
              list[i] = next;
              controller.updateForm((f) => f.copyWith(data: list));
            },
          ),
          errorMessage: stepErrors['data'],
        ),
      ],
    );
  }
}

class _DataRow extends StatefulWidget {
  const _DataRow({
    required this.initial,
    required this.errorSecretKey,
    required this.errorRemoteKey,
    required this.errorRemoteProperty,
    required this.onChanged,
  });

  final EsoDataItem initial;
  final String? errorSecretKey;
  final String? errorRemoteKey;
  final String? errorRemoteProperty;
  final ValueChanged<EsoDataItem> onChanged;

  @override
  State<_DataRow> createState() => _DataRowState();
}

class _DataRowState extends State<_DataRow> {
  late final TextEditingController _secret =
      TextEditingController(text: widget.initial.secretKey);
  late final TextEditingController _remote =
      TextEditingController(text: widget.initial.remoteKey);
  late final TextEditingController _prop =
      TextEditingController(text: widget.initial.remoteProperty);

  @override
  void didUpdateWidget(covariant _DataRow old) {
    super.didUpdateWidget(old);
    // Resync only when the parent forced an external change (e.g. row
    // shifted by a delete). Don't resync on every keystroke or the
    // cursor jumps. Mirrors the _DnsNameRow pattern.
    if (widget.initial.secretKey != _secret.text &&
        widget.initial.secretKey != old.initial.secretKey) {
      _secret.text = widget.initial.secretKey;
    }
    if (widget.initial.remoteKey != _remote.text &&
        widget.initial.remoteKey != old.initial.remoteKey) {
      _remote.text = widget.initial.remoteKey;
    }
    if (widget.initial.remoteProperty != _prop.text &&
        widget.initial.remoteProperty != old.initial.remoteProperty) {
      _prop.text = widget.initial.remoteProperty;
    }
  }

  @override
  void dispose() {
    _secret.dispose();
    _remote.dispose();
    _prop.dispose();
    super.dispose();
  }

  void _emit() {
    widget.onChanged(EsoDataItem(
      secretKey: _secret.text,
      remoteKey: _remote.text,
      remoteProperty: _prop.text,
    ));
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(children: [
          Expanded(
            child: TextField(
              controller: _secret,
              decoration: InputDecoration(
                labelText: 'Secret key',
                hintText: 'password',
                isDense: true,
                border: const OutlineInputBorder(),
                errorText: widget.errorSecretKey,
              ),
              onChanged: (_) => _emit(),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: TextField(
              controller: _remote,
              decoration: InputDecoration(
                labelText: 'Remote key',
                hintText: 'kv/db',
                isDense: true,
                border: const OutlineInputBorder(),
                errorText: widget.errorRemoteKey,
              ),
              onChanged: (_) => _emit(),
            ),
          ),
        ]),
        const SizedBox(height: 8),
        TextField(
          controller: _prop,
          decoration: InputDecoration(
            labelText: 'Remote property (optional)',
            hintText: 'password',
            helperText: 'Field within the remote value when it is a JSON blob',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText: widget.errorRemoteProperty,
          ),
          onChanged: (_) => _emit(),
        ),
      ],
    );
  }
}
