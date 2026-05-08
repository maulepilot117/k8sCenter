// Certificate wizard screen. One Configure step covering name +
// namespace + secretName + issuerRef + DNS names + commonName +
// duration + renewBefore + private key. Review step delegates to the
// shared WizardReviewBody.
//
// Algorithm/size coupling: when the operator changes algorithm, the
// size auto-resets to that algorithm's default. Otherwise an operator
// switching from RSA-2048 to Ed25519 would leave a stray `size: 2048`
// on the body that the backend rejects (Ed25519 disallows size).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/duration_input.dart';
import '../../widgets/issuer_picker.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'certificate_wizard_controller.dart';

class CertificateWizardScreen extends ConsumerStatefulWidget {
  const CertificateWizardScreen({super.key});

  @override
  ConsumerState<CertificateWizardScreen> createState() =>
      _CertificateWizardScreenState();
}

class _CertificateWizardScreenState
    extends ConsumerState<CertificateWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<CertificateForm>(
      wizardType: 'certificate',
      title: 'New Certificate',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: certificateWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<CertificateForm>(
              wizardKey: _wizardKey,
              controllerProvider: certificateWizardProvider,
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
    final state = ref.watch(certificateWizardProvider(wizardKey));
    final controller =
        ref.read(certificateWizardProvider(wizardKey).notifier);
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
            hintText: 'web-tls',
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
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: form.secretName,
          decoration: InputDecoration(
            labelText: 'Secret name',
            hintText: 'web-tls-secret',
            helperText:
                'Cert-manager writes the issued cert + private key here.',
            border: const OutlineInputBorder(),
            errorText: stepErrors['secretName'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(secretName: v)),
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Issuer',
          subtitle: 'The Issuer or ClusterIssuer that signs this certificate',
        ),
        const SizedBox(height: 8),
        IssuerPicker(
          clusterId: wizardKey.clusterId,
          namespace: form.namespace,
          selected: form.issuerRef,
          onChanged: (sel) =>
              controller.updateForm((f) => f.copyWith(issuerRef: sel)),
          label: 'Issuer',
          errorMessage:
              stepErrors['issuerRef.name'] ?? stepErrors['issuerRef.kind'],
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Identifiers',
          subtitle: 'At least one DNS name OR a common name is required',
        ),
        const SizedBox(height: 8),
        RepeatingRowGroup<String>(
          items: form.dnsNames,
          addLabel: 'Add DNS name',
          onAdd: () => controller.updateForm(
              (f) => f.copyWith(dnsNames: [...f.dnsNames, ''])),
          onRemove: (i) {
            final next = [...form.dnsNames]..removeAt(i);
            controller.updateForm((f) => f.copyWith(dnsNames: next));
          },
          itemBuilder: (ctx, i, name) => _DnsNameRow(
            initialValue: name,
            error: stepErrors['dnsNames[$i]'],
            onChanged: (v) {
              final next = [...form.dnsNames];
              next[i] = v;
              controller.updateForm((f) => f.copyWith(dnsNames: next));
            },
          ),
          errorMessage: stepErrors['dnsNames'],
        ),
        const SizedBox(height: 12),
        TextFormField(
          initialValue: form.commonName,
          decoration: InputDecoration(
            labelText: 'Common name (optional)',
            hintText: 'web.example.com',
            border: const OutlineInputBorder(),
            errorText: stepErrors['commonName'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(commonName: v)),
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Lifetime',
          subtitle: 'Optional. Defaults: 2160h duration / 360h renewBefore',
        ),
        const SizedBox(height: 8),
        Row(children: [
          Expanded(
            child: DurationInput(
              label: 'Duration',
              value: form.duration,
              onChanged: (v) =>
                  controller.updateForm((f) => f.copyWith(duration: v)),
              hintText: 'e.g. 2160h',
              errorText: stepErrors['duration'],
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: DurationInput(
              label: 'Renew before',
              value: form.renewBefore,
              onChanged: (v) =>
                  controller.updateForm((f) => f.copyWith(renewBefore: v)),
              hintText: 'e.g. 360h',
              errorText: stepErrors['renewBefore'],
            ),
          ),
        ]),
        const SizedBox(height: 24),
        const WizardSectionHeader('Private key'),
        const SizedBox(height: 8),
        DropdownButtonFormField<String>(
          initialValue: form.privateKey.algorithm,
          decoration: InputDecoration(
            labelText: 'Algorithm',
            border: const OutlineInputBorder(),
            errorText: stepErrors['privateKey.algorithm'],
          ),
          items: [
            for (final a in kCertPrivateKeyAlgorithms)
              DropdownMenuItem(value: a, child: Text(a)),
          ],
          onChanged: (v) {
            if (v == null) return;
            // Reset size to that algorithm's default — Ed25519 omits
            // size entirely; RSA/ECDSA pick up their canonical default.
            final defaultSize = kCertDefaultKeySize[v];
            controller.updateForm((f) => f.copyWith(
                  privateKey: CertPrivateKey(
                    algorithm: v,
                    size: defaultSize,
                  ),
                ));
          },
        ),
        if (form.privateKey.algorithm != 'Ed25519') ...[
          const SizedBox(height: 12),
          _KeySizeField(
            algorithm: form.privateKey.algorithm,
            value: form.privateKey.size ?? 0,
            error: stepErrors['privateKey.size'],
            onChanged: (n) => controller.updateForm(
              (f) => f.copyWith(
                privateKey: f.privateKey.copyWith(size: n),
              ),
            ),
          ),
        ],
      ],
    );
  }
}

class _DnsNameRow extends StatefulWidget {
  const _DnsNameRow({
    required this.initialValue,
    required this.error,
    required this.onChanged,
  });

  final String initialValue;
  final String? error;
  final ValueChanged<String> onChanged;

  @override
  State<_DnsNameRow> createState() => _DnsNameRowState();
}

class _DnsNameRowState extends State<_DnsNameRow> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.initialValue);

  @override
  void didUpdateWidget(covariant _DnsNameRow old) {
    super.didUpdateWidget(old);
    // Only resync if the parent forced an external change (e.g. row
    // shifted by a delete). Don't resync on every keystroke or the
    // cursor jumps.
    if (widget.initialValue != _ctl.text && widget.initialValue != old.initialValue) {
      _ctl.text = widget.initialValue;
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
        hintText: 'web.example.com',
        isDense: true,
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
      onChanged: widget.onChanged,
    );
  }
}

/// RSA / ECDSA size field. Hidden for Ed25519 (which the backend
/// rejects when size is set). Renders as a dropdown with the algorithm-
/// specific allowed sizes so the operator can't pick an invalid one.
class _KeySizeField extends StatelessWidget {
  const _KeySizeField({
    required this.algorithm,
    required this.value,
    required this.error,
    required this.onChanged,
  });

  final String algorithm;
  final int value;
  final String? error;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    final sizes = algorithm == 'ECDSA'
        ? const [256, 384, 521]
        : const [2048, 3072, 4096];
    // If [value] doesn't match the algorithm's allowed sizes (e.g.,
    // mid-switch state) fall back to the first valid size for the
    // dropdown's `value`. The controller's algorithm-change handler
    // resets size, so this is defensive.
    final selected = sizes.contains(value) ? value : sizes.first;
    return DropdownButtonFormField<int>(
      initialValue: selected,
      decoration: InputDecoration(
        labelText: 'Key size',
        border: const OutlineInputBorder(),
        errorText: error,
      ),
      items: [
        for (final s in sizes)
          DropdownMenuItem(value: s, child: Text('$s')),
      ],
      onChanged: (v) {
        if (v != null) onChanged(v);
      },
    );
  }
}

