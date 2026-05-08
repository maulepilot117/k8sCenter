// ExternalSecret wizard controller. Mirrors
// `frontend/islands/ExternalSecretWizard.tsx` and ports the wire
// contract from `backend/internal/wizard/externalsecret.go:59`.
//
// Wire format (`ExternalSecretInput`):
//   {
//     name, namespace,
//     storeRef: { name, kind: "SecretStore" | "ClusterSecretStore" },
//     refreshInterval?:    string  (Go duration; "0" disables auto-refresh),
//     targetSecretName,
//     data?:     [ { secretKey, remoteRef: { key, property?, version? } } ],
//     dataFrom?: [ { extract?: {...} | find?: {...} } ],
//   }
//
// Backend invariant: at least one of data or dataFrom required.
// Web wizard exposes `data` only — `dataFrom` is reachable via the YAML
// editor. Mobile mirrors that scope (R10).
//
// One Configure step + Review.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/store_picker.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// One row in the `data` repeating group. Maps directly to ESO's
/// `spec.data[]` entry on apply: `{ secretKey, remoteRef: {key, property?} }`.
class EsoDataItem {
  const EsoDataItem({
    this.secretKey = '',
    this.remoteKey = '',
    this.remoteProperty = '',
  });

  /// Key written into the synced Secret.
  final String secretKey;

  /// Source key on the remote store (e.g. Vault path).
  final String remoteKey;

  /// Optional property within the remote value (e.g. JSON field name
  /// when the remote value is a JSON blob).
  final String remoteProperty;

  EsoDataItem copyWith({
    String? secretKey,
    String? remoteKey,
    String? remoteProperty,
  }) =>
      EsoDataItem(
        secretKey: secretKey ?? this.secretKey,
        remoteKey: remoteKey ?? this.remoteKey,
        remoteProperty: remoteProperty ?? this.remoteProperty,
      );
}

class ExternalSecretForm {
  const ExternalSecretForm({
    this.name = '',
    this.namespace = '',
    this.storeRef,
    this.refreshInterval = '1h',
    this.targetSecretName = '',
    this.data = const [EsoDataItem()],
  });

  final String name;
  final String namespace;
  final StoreSelection? storeRef;
  final String refreshInterval;
  final String targetSecretName;
  final List<EsoDataItem> data;

  ExternalSecretForm copyWith({
    String? name,
    String? namespace,
    StoreSelection? storeRef,
    bool clearStoreRef = false,
    String? refreshInterval,
    String? targetSecretName,
    List<EsoDataItem>? data,
  }) =>
      ExternalSecretForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        storeRef: clearStoreRef ? null : (storeRef ?? this.storeRef),
        refreshInterval: refreshInterval ?? this.refreshInterval,
        targetSecretName: targetSecretName ?? this.targetSecretName,
        data: data ?? this.data,
      );
}

class ExternalSecretWizardController
    extends WizardController<ExternalSecretForm> {
  @override
  String get wizardType => 'external-secret';

  @override
  String get resourceListKind => 'externalsecrets';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Identity, store, target, data items',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  ExternalSecretForm buildInitialForm() => const ExternalSecretForm();

  @override
  Map<String, dynamic> toPreviewBody(ExternalSecretForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'storeRef': {
        'name': form.storeRef?.name ?? '',
        'kind': form.storeRef?.kind ?? 'SecretStore',
      },
      'targetSecretName': form.targetSecretName,
    };
    if (form.refreshInterval.trim().isNotEmpty) {
      body['refreshInterval'] = form.refreshInterval.trim();
    }
    final items = <Map<String, dynamic>>[];
    for (final d in form.data) {
      // Don't ship items where every field is blank — the operator
      // probably tabbed past a sentinel row. Items with a partial fill
      // still ship so the backend can reject and the operator gets a
      // routed error inline (which is the form's only signal to fix
      // the row) rather than a silent drop.
      if (d.secretKey.trim().isEmpty &&
          d.remoteKey.trim().isEmpty &&
          d.remoteProperty.trim().isEmpty) {
        continue;
      }
      final ref = <String, dynamic>{'key': d.remoteKey.trim()};
      if (d.remoteProperty.trim().isNotEmpty) {
        ref['property'] = d.remoteProperty.trim();
      }
      items.add({
        'secretKey': d.secretKey.trim(),
        'remoteRef': ref,
      });
    }
    body['data'] = items;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    const known = {
      'name',
      'namespace',
      'storeRef.name',
      'storeRef.kind',
      'refreshInterval',
      'targetSecretName',
      'data',
      'dataFrom',
    };
    if (known.contains(fieldPath)) return 0;
    if (fieldPath.startsWith('data[')) return 0;
    if (fieldPath.startsWith('dataFrom[')) return 0;
    if (fieldPath.startsWith('storeRef.')) return 0;
    return null;
  }

  @override
  StepFieldErrors validateLocally(ExternalSecretForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    final ref = form.storeRef;
    if (ref == null || ref.name.isEmpty) {
      out['storeRef.name'] = 'SecretStore is required';
    }
    if (form.targetSecretName.trim().isEmpty) {
      out['targetSecretName'] = 'Target secret name is required';
    }
    // At least one data row must have both secretKey and remoteRef.key.
    // The web wizard enforces "at least one data item"; we mirror.
    final populated = form.data.where(
      (d) => d.secretKey.trim().isNotEmpty && d.remoteKey.trim().isNotEmpty,
    );
    if (populated.isEmpty) {
      out['data'] = 'Add at least one data item with secret key and remote key';
    }
    // Per-row validation: a half-filled row (one of secretKey/remoteKey
    // blank) is a UX trap — the operator probably forgot to finish.
    // Surface that explicitly rather than letting the backend echo it.
    final secretKeyToRows = <String, List<int>>{};
    for (var i = 0; i < form.data.length; i++) {
      final d = form.data[i];
      final hasSecretKey = d.secretKey.trim().isNotEmpty;
      final hasRemoteKey = d.remoteKey.trim().isNotEmpty;
      final hasProperty = d.remoteProperty.trim().isNotEmpty;
      if (!hasSecretKey && !hasRemoteKey && !hasProperty) continue;
      if (!hasSecretKey) {
        out['data[$i].secretKey'] = 'Required';
      } else {
        // Track populated secretKeys for the duplicate gate. Backend
        // rejects duplicates explicitly; surfacing inline here saves
        // a round-trip and points at every offending row, not just
        // the second one.
        secretKeyToRows.putIfAbsent(d.secretKey.trim(), () => []).add(i);
      }
      if (!hasRemoteKey) {
        out['data[$i].remoteRef.key'] = 'Required';
      }
    }
    for (final entry in secretKeyToRows.entries) {
      if (entry.value.length < 2) continue;
      for (final i in entry.value) {
        out['data[$i].secretKey'] = 'Duplicate of row ${entry.value.where((x) => x != i).first + 1}';
      }
    }
    return out;
  }
}

final externalSecretWizardProvider = AutoDisposeNotifierProvider.family<
    ExternalSecretWizardController,
    WizardState<ExternalSecretForm>,
    WizardKey>(
  ExternalSecretWizardController.new,
);
