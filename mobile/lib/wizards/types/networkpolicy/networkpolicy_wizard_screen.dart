// NetworkPolicy wizard screen. Configure step exposes:
//   * Name + namespace
//   * podSelector (KeyValueTable; matches the empty-selector default
//     case for whole-namespace policies)
//   * policyTypes checkboxes (Ingress / Egress)
//   * ingress + egress rules — each a RepeatingRowGroup of rules,
//     each rule has a peers RepeatingRowGroup and a ports
//     RepeatingRowGroup.
//
// Quarantine UX hint: when policyTypes are checked but no rules are
// added, the YAML preview will surface `ingress: []` / `egress: []`
// — operators recognise that pattern as deny-all.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'networkpolicy_wizard_controller.dart';

class NetworkPolicyWizardScreen extends ConsumerStatefulWidget {
  const NetworkPolicyWizardScreen({super.key});

  @override
  ConsumerState<NetworkPolicyWizardScreen> createState() =>
      _NetworkPolicyWizardScreenState();
}

class _NetworkPolicyWizardScreenState
    extends ConsumerState<NetworkPolicyWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<NetworkPolicyForm>(
      wizardType: 'networkpolicy',
      title: 'New NetworkPolicy',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: networkPolicyWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<NetworkPolicyForm>(
              wizardKey: _wizardKey,
              controllerProvider: networkPolicyWizardProvider,
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
    final state = ref.watch(networkPolicyWizardProvider(wizardKey));
    final controller =
        ref.read(networkPolicyWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'quarantine-default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 24),
        WizardSectionHeader(
          'Pod selector',
          subtitle: 'Match labels — leave empty to apply to all pods',
        ),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.podSelector,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(podSelector: pairs)),
          keyLabel: 'Label key',
          valueLabel: 'Label value',
          errorMessage: stepErrors['podSelector'],
        ),
        const SizedBox(height: 24),
        WizardSectionHeader(
          'Policy types',
          subtitle: 'Direction(s) of traffic this policy controls',
        ),
        const SizedBox(height: 8),
        _PolicyTypesPicker(
          includeIngress: state.form.includeIngress,
          includeEgress: state.form.includeEgress,
          onIngressChanged: (v) =>
              controller.updateForm((f) => f.copyWith(includeIngress: v)),
          onEgressChanged: (v) =>
              controller.updateForm((f) => f.copyWith(includeEgress: v)),
          errorMessage: stepErrors['policyTypes'],
        ),
        if (state.form.includeIngress) ...[
          const SizedBox(height: 24),
          WizardSectionHeader(
            'Ingress rules',
            subtitle:
                'Empty list with Ingress policy type denies all incoming traffic',
          ),
          const SizedBox(height: 8),
          _RuleGroup(
            isIngress: true,
            rules: state.form.ingress,
            onChanged: (rules) =>
                controller.updateForm((f) => f.copyWith(ingress: rules)),
            stepErrors: stepErrors,
          ),
        ],
        if (state.form.includeEgress) ...[
          const SizedBox(height: 24),
          WizardSectionHeader(
            'Egress rules',
            subtitle:
                'Empty list with Egress policy type denies all outgoing traffic',
          ),
          const SizedBox(height: 8),
          _RuleGroup(
            isIngress: false,
            rules: state.form.egress,
            onChanged: (rules) =>
                controller.updateForm((f) => f.copyWith(egress: rules)),
            stepErrors: stepErrors,
          ),
        ],
      ],
    );
  }
}

class _PolicyTypesPicker extends StatelessWidget {
  const _PolicyTypesPicker({
    required this.includeIngress,
    required this.includeEgress,
    required this.onIngressChanged,
    required this.onEgressChanged,
    required this.errorMessage,
  });

  final bool includeIngress;
  final bool includeEgress;
  final ValueChanged<bool> onIngressChanged;
  final ValueChanged<bool> onEgressChanged;
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: CheckboxListTile(
                title: const Text('Ingress'),
                value: includeIngress,
                onChanged: (v) => onIngressChanged(v ?? false),
                controlAffinity: ListTileControlAffinity.leading,
                dense: true,
                contentPadding: EdgeInsets.zero,
              ),
            ),
            Expanded(
              child: CheckboxListTile(
                title: const Text('Egress'),
                value: includeEgress,
                onChanged: (v) => onEgressChanged(v ?? false),
                controlAffinity: ListTileControlAffinity.leading,
                dense: true,
                contentPadding: EdgeInsets.zero,
              ),
            ),
          ],
        ),
        if (errorMessage != null)
          Text(
            errorMessage!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
      ],
    );
  }
}

class _RuleGroup extends StatelessWidget {
  const _RuleGroup({
    required this.isIngress,
    required this.rules,
    required this.onChanged,
    required this.stepErrors,
  });

  final bool isIngress;
  final List<NetworkPolicyRule> rules;
  final ValueChanged<List<NetworkPolicyRule>> onChanged;
  final Map<String, String> stepErrors;

  String _errKey(int i, String suffix) =>
      '${isIngress ? 'ingress' : 'egress'}[$i]$suffix';

  /// Aggregate any rule-level errors (`ingress[N]` / `egress[N]`)
  /// across all rules into a single banner message — there is no
  /// per-rule slot in [RepeatingRowGroup], and dropping these would
  /// silently eat backend cap-violation messages
  /// (`maxPeersPerRule=20`, `maxPortsPerRule=20`).
  String? _aggregatedRuleError(int ruleCount) {
    final messages = <String>[];
    for (var i = 0; i < ruleCount; i++) {
      final m = stepErrors[_errKey(i, '')];
      if (m != null && m.isNotEmpty) messages.add('Rule ${i + 1}: $m');
    }
    if (messages.isEmpty) return null;
    return messages.join('\n');
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return RepeatingRowGroup<NetworkPolicyRule>(
      items: rules,
      addLabel: 'Add rule',
      emptyMessage: 'No rules — denies all ${isIngress ? 'ingress' : 'egress'}',
      onAdd: () =>
          onChanged([...rules, const NetworkPolicyRule()]),
      onRemove: (i) {
        final next = [...rules]..removeAt(i);
        onChanged(next);
      },
      itemBuilder: (ctx, i, rule) {
        return Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border.all(color: colors.borderSubtle),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Rule ${i + 1}',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontWeight: FontWeight.w600,
                  fontSize: 12,
                ),
              ),
              const SizedBox(height: 8),
              _PeerGroup(
                isIngress: isIngress,
                ruleIndex: i,
                peers: rule.peers,
                stepErrors: stepErrors,
                onChanged: (peers) {
                  final next = [...rules];
                  next[i] = next[i].copyWith(peers: peers);
                  onChanged(next);
                },
              ),
              const SizedBox(height: 12),
              Text(
                'Ports',
                style: TextStyle(
                  color: colors.textMuted,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 4),
              _PortGroup(
                isIngress: isIngress,
                ruleIndex: i,
                ports: rule.ports,
                stepErrors: stepErrors,
                onChanged: (ports) {
                  final next = [...rules];
                  next[i] = next[i].copyWith(ports: ports);
                  onChanged(next);
                },
              ),
            ],
          ),
        );
      },
      errorMessage: _aggregatedRuleError(rules.length),
    );
  }
}

class _PeerGroup extends StatelessWidget {
  const _PeerGroup({
    required this.isIngress,
    required this.ruleIndex,
    required this.peers,
    required this.stepErrors,
    required this.onChanged,
  });

  final bool isIngress;
  final int ruleIndex;
  final List<NetworkPolicyPeer> peers;
  final Map<String, String> stepErrors;
  final ValueChanged<List<NetworkPolicyPeer>> onChanged;

  String _peerErrKey(int peerIdx, String suffix) =>
      '${isIngress ? 'ingress' : 'egress'}[$ruleIndex].'
      '${isIngress ? 'from' : 'to'}[$peerIdx]$suffix';

  @override
  Widget build(BuildContext context) {
    return RepeatingRowGroup<NetworkPolicyPeer>(
      items: peers,
      addLabel: 'Add peer',
      emptyMessage: 'No peers — matches all ${isIngress ? 'sources' : 'destinations'}',
      onAdd: () => onChanged([...peers, const NetworkPolicyPeer()]),
      onRemove: (i) {
        final next = [...peers]..removeAt(i);
        onChanged(next);
      },
      itemBuilder: (ctx, i, peer) {
        // Aggregate any except[N] errors (the textarea has one input
        // for many CIDRs; backend reports per-index — surface them all
        // under the field rather than dropping them).
        final exceptErrors = <String>[];
        final exceptPrefix = _peerErrKey(i, '.ipBlock.except[');
        for (final entry in stepErrors.entries) {
          if (entry.key.startsWith(exceptPrefix)) {
            exceptErrors.add(entry.value);
          }
        }
        return _PeerRow(
          peer: peer,
          cidrError: stepErrors[_peerErrKey(i, '.ipBlock.cidr')],
          exceptError:
              exceptErrors.isEmpty ? null : exceptErrors.join('; '),
          onChanged: (next) {
            final list = [...peers];
            list[i] = next;
            onChanged(list);
          },
        );
      },
    );
  }
}

class _PeerRow extends StatelessWidget {
  const _PeerRow({
    required this.peer,
    required this.cidrError,
    required this.exceptError,
    required this.onChanged,
  });

  final NetworkPolicyPeer peer;
  final String? cidrError;
  final String? exceptError;
  final ValueChanged<NetworkPolicyPeer> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SegmentedButton<PeerKind>(
          segments: const [
            ButtonSegment(value: PeerKind.pod, label: Text('Pod')),
            ButtonSegment(value: PeerKind.namespaceSel, label: Text('Namespace')),
            ButtonSegment(value: PeerKind.ipBlock, label: Text('IP block')),
          ],
          selected: {peer.kind},
          showSelectedIcon: false,
          onSelectionChanged: (s) => onChanged(peer.copyWith(kind: s.first)),
        ),
        const SizedBox(height: 8),
        switch (peer.kind) {
          PeerKind.pod => KeyValueTable(
              pairs: peer.podSelector,
              onChanged: (kv) => onChanged(peer.copyWith(podSelector: kv)),
              keyLabel: 'Label key',
              valueLabel: 'Label value',
            ),
          PeerKind.namespaceSel => KeyValueTable(
              pairs: peer.namespaceSelector,
              onChanged: (kv) =>
                  onChanged(peer.copyWith(namespaceSelector: kv)),
              keyLabel: 'Namespace label key',
              valueLabel: 'Namespace label value',
            ),
          PeerKind.ipBlock => Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                TextFormField(
                  initialValue: peer.cidr,
                  decoration: InputDecoration(
                    labelText: 'CIDR',
                    hintText: '10.0.0.0/8',
                    border: const OutlineInputBorder(),
                    errorText: cidrError,
                  ),
                  onChanged: (v) =>
                      onChanged(peer.copyWith(cidr: v.trim())),
                ),
                const SizedBox(height: 6),
                Text(
                  'Except CIDRs (one per line or comma-separated, optional)',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
                const SizedBox(height: 4),
                TextFormField(
                  initialValue: peer.except.join('\n'),
                  maxLines: 3,
                  decoration: InputDecoration(
                    hintText: '10.0.1.0/24\n10.0.2.0/24',
                    border: const OutlineInputBorder(),
                    errorText: exceptError,
                  ),
                  onChanged: (v) {
                    // Accept both newline-separated and comma-separated
                    // input. kubectl examples in the wild come in both
                    // forms; rejecting either silently produces invalid
                    // CIDRs the backend then 422's on.
                    final entries = v
                        .split(RegExp(r'[\n,]'))
                        .map((s) => s.trim())
                        .where((s) => s.isNotEmpty)
                        .toList();
                    onChanged(peer.copyWith(except: entries));
                  },
                ),
              ],
            ),
        },
      ],
    );
  }
}

class _PortGroup extends StatelessWidget {
  const _PortGroup({
    required this.isIngress,
    required this.ruleIndex,
    required this.ports,
    required this.stepErrors,
    required this.onChanged,
  });

  final bool isIngress;
  final int ruleIndex;
  final List<NetworkPolicyPort> ports;
  final Map<String, String> stepErrors;
  final ValueChanged<List<NetworkPolicyPort>> onChanged;

  @override
  Widget build(BuildContext context) {
    return RepeatingRowGroup<NetworkPolicyPort>(
      items: ports,
      addLabel: 'Add port',
      emptyMessage: 'No port restrictions',
      onAdd: () => onChanged([...ports, const NetworkPolicyPort()]),
      onRemove: (i) {
        final next = [...ports]..removeAt(i);
        onChanged(next);
      },
      itemBuilder: (ctx, i, port) {
        final portError = stepErrors[
            '${isIngress ? 'ingress' : 'egress'}[$ruleIndex].ports[$i].port'];
        return _PortRow(
          port: port,
          portError: portError,
          onChanged: (next) {
            final list = [...ports];
            list[i] = next;
            onChanged(list);
          },
        );
      },
    );
  }
}

class _PortRow extends StatefulWidget {
  const _PortRow({
    required this.port,
    required this.portError,
    required this.onChanged,
  });

  final NetworkPolicyPort port;
  final String? portError;
  final ValueChanged<NetworkPolicyPort> onChanged;

  @override
  State<_PortRow> createState() => _PortRowState();
}

class _PortRowState extends State<_PortRow> {
  late final TextEditingController _portCtl = TextEditingController(
      text: widget.port.port == 0 ? '' : '${widget.port.port}');

  @override
  void didUpdateWidget(covariant _PortRow old) {
    super.didUpdateWidget(old);
    final next = widget.port.port == 0 ? '' : '${widget.port.port}';
    if (_portCtl.text != next) _portCtl.text = next;
  }

  @override
  void dispose() {
    _portCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          flex: 3,
          child: TextField(
            controller: _portCtl,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            onChanged: (v) => widget.onChanged(
                widget.port.copyWith(port: int.tryParse(v) ?? 0)),
            decoration: InputDecoration(
              labelText: 'Port',
              hintText: '443',
              isDense: true,
              border: const OutlineInputBorder(),
              errorText: widget.portError,
            ),
          ),
        ),
        const SizedBox(width: 8),
        DropdownButton<String>(
          value: widget.port.protocol,
          underline: const SizedBox.shrink(),
          items: [
            for (final p in kNetworkPolicyProtocols)
              DropdownMenuItem(value: p, child: Text(p)),
          ],
          onChanged: (v) {
            if (v == null) return;
            widget.onChanged(widget.port.copyWith(protocol: v));
          },
        ),
      ],
    );
  }
}
