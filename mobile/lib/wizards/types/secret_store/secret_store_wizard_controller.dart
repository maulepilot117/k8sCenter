// SecretStore / ClusterSecretStore wizard controller.
//
// Mirrors `frontend/islands/SecretStoreWizard.tsx` and ports the wire
// contract from `backend/internal/wizard/secretstore.go:55`.
//
// Scope-variant pattern: a single form/UI shape is shared between
// namespaced (SecretStore) and cluster-scoped (ClusterSecretStore)
// stores. Two concrete subclasses ([SecretStoreWizardController] /
// [ClusterSecretStoreWizardController]) supply the registered wizard
// type and scope; everything else lives in [_SecretStoreWizardBase].
//
// Wire format (`SecretStoreInput`):
//   {
//     name, namespace?, provider, refreshInterval?,
//     providerSpec: { ...provider-specific keys... },
//   }
//
// Provider keys: vault | aws | awsps | azurekv | gcpsm | kubernetes |
//                doppler | onepassword
// (The backend also accepts bitwardensecretsmanager, conjur, infisical
// but ships no validator for those — both web and mobile route those
// operators to the YAML editor. R10 isomorphism.)
//
// Steps: Identity → Provider → Configure → Review
// (Provider switch resets `providerSpec` so stale fields can't leak
// into the next provider's form.)
//
// Error routing:
//   step 0 (Identity):  name, namespace, refreshInterval, scope
//   step 1 (Provider):  provider, providerSpec
//   step 2 (Configure): every other path — provider-spec validators
//                       emit bare paths (e.g. `auth.kubernetes.role`,
//                       `server`, `region`).

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_registry.dart';
import '../../wizard_step.dart';

class SecretStoreForm {
  const SecretStoreForm({
    this.name = '',
    this.namespace = '',
    this.provider = '',
    this.refreshInterval = '',
    this.providerSpec = const <String, dynamic>{},
  });

  final String name;
  final String namespace;

  /// Provider id — empty until step 1 picks one.
  final String provider;

  /// Optional Go duration string (e.g. `1h`). Backend accepts empty.
  final String refreshInterval;

  /// Provider-specific JSON. Per-provider forms read/write directly via
  /// [updateProviderSpec] so each form file stays self-contained.
  final Map<String, dynamic> providerSpec;

  SecretStoreForm copyWith({
    String? name,
    String? namespace,
    String? provider,
    String? refreshInterval,
    Map<String, dynamic>? providerSpec,
  }) =>
      SecretStoreForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        provider: provider ?? this.provider,
        refreshInterval: refreshInterval ?? this.refreshInterval,
        providerSpec: providerSpec ?? this.providerSpec,
      );
}

abstract class SecretStoreWizardBase extends WizardController<SecretStoreForm> {
  /// Set by the concrete subclass: namespaced vs cluster.
  WizardScope get scope;

  @override
  String get resourceListKind => scope == WizardScope.cluster
      ? 'clustersecretstores'
      : 'secretstores';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Identity',
          description: 'Name and namespace',
        ),
        WizardStep(
          title: 'Provider',
          description: 'Pick the secret backend',
        ),
        WizardStep(
          title: 'Configure',
          description: 'Provider-specific connection and auth',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  SecretStoreForm buildInitialForm() => const SecretStoreForm();

  /// Convenience: replace the provider-spec map. Provider forms call
  /// this from their `onUpdateSpec` callback.
  void updateProviderSpec(Map<String, dynamic> next) {
    updateForm((f) => f.copyWith(providerSpec: next));
  }

  /// Switch the active provider. Resets `providerSpec` to an empty map
  /// so a stale form's fields can't leak into the new provider's body.
  /// Mirrors web's `SecretStoreWizard.tsx` provider-switch behavior.
  void switchProvider(String next) {
    if (state.form.provider == next) return;
    updateForm((f) => f.copyWith(
          provider: next,
          providerSpec: const <String, dynamic>{},
        ));
  }

  @override
  Map<String, dynamic> toPreviewBody(SecretStoreForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'provider': form.provider,
      'providerSpec': form.providerSpec,
    };
    if (scope != WizardScope.cluster) {
      body['namespace'] = form.namespace;
    }
    if (form.refreshInterval.trim().isNotEmpty) {
      body['refreshInterval'] = form.refreshInterval.trim();
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    // Step 0: identity / refreshInterval / scope.
    const step0 = {
      'name',
      'namespace',
      'refreshInterval',
      'scope',
    };
    // Step 1: provider selection itself.
    const step1 = {
      'provider',
      'providerSpec',
    };
    if (step0.contains(fieldPath)) return 0;
    if (step1.contains(fieldPath)) return 1;
    // Everything else is per-provider spec — Configure step.
    // Examples: server, region, vaultUrl, tenantId, projectID,
    // connectHost, auth.X, secretRef.X, vaults.X, oidcConfig.X.
    return 2;
  }

  @override
  StepFieldErrors validateLocally(SecretStoreForm form, int stepIndex) {
    if (stepIndex == 0) {
      return validateNameAndNamespace(
        form.name,
        form.namespace,
        requireNamespace: scope != WizardScope.cluster,
      );
    }
    if (stepIndex == 1) {
      if (form.provider.isEmpty) {
        return {'provider': 'Pick a provider before continuing'};
      }
    }
    return const <String, String>{};
  }
}

class SecretStoreWizardController extends SecretStoreWizardBase {
  @override
  String get wizardType => 'secret-store';

  @override
  WizardScope get scope => WizardScope.namespaced;
}

class ClusterSecretStoreWizardController extends SecretStoreWizardBase {
  @override
  String get wizardType => 'cluster-secret-store';

  @override
  WizardScope get scope => WizardScope.cluster;
}

final secretStoreWizardProvider = AutoDisposeNotifierProvider.family<
    SecretStoreWizardController, WizardState<SecretStoreForm>, WizardKey>(
  SecretStoreWizardController.new,
);

final clusterSecretStoreWizardProvider = AutoDisposeNotifierProvider.family<
    ClusterSecretStoreWizardController,
    WizardState<SecretStoreForm>,
    WizardKey>(
  ClusterSecretStoreWizardController.new,
);
