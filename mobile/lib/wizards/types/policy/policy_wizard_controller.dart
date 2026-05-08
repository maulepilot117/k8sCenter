// Policy wizard controller. Mirrors `frontend/islands/PolicyWizard.tsx`
// and ports the wire contract from
// `backend/internal/wizard/policy_input.go:159`.
//
// Wire format (`PolicyWizardInput`):
//   {
//     templateId, engine: "kyverno" | "gatekeeper",
//     name, action,
//     targetKinds: [string], excludedNamespaces?: [string],
//     description?: string,
//     params?: { ... per-template ... },
//   }
//
// Backend invariant: `engine` must be one of the engines the picked
// template supports; `action` must be one of that engine's allowed
// actions; `targetKinds` must be supplied. Defaults flow:
//   * Pick a template → name auto-fills from id; targetKinds auto-fills
//     from template.targetKinds; description auto-fills from
//     template.description; params auto-fill from template defaults;
//     action auto-fills from the engine's default action for the template.
//   * Switch engine → action re-defaults to that engine's default.
//
// Steps: Template → Configure → Review.
// Engine auto-detect is performed once at wizard open via
// `policyEngineStatusProvider`. The wizard never falls back to a
// hardcoded engine list — when neither Kyverno nor Gatekeeper is
// installed, the screen renders an EmptyState in place of the
// template picker.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';
import 'policy_templates.dart';

class PolicyForm {
  const PolicyForm({
    this.templateId = '',
    this.engine = '',
    this.name = '',
    this.action = '',
    this.targetKinds = const <String>[],
    this.excludedNamespaces = const <String>[
      'kube-system',
      'kube-public',
      'kube-node-lease',
    ],
    this.description = '',
    this.params = const <String, dynamic>{},
  });

  final String templateId;
  final String engine;
  final String name;
  final String action;
  final List<String> targetKinds;
  final List<String> excludedNamespaces;
  final String description;
  final Map<String, dynamic> params;

  PolicyForm copyWith({
    String? templateId,
    String? engine,
    String? name,
    String? action,
    List<String>? targetKinds,
    List<String>? excludedNamespaces,
    String? description,
    Map<String, dynamic>? params,
  }) =>
      PolicyForm(
        templateId: templateId ?? this.templateId,
        engine: engine ?? this.engine,
        name: name ?? this.name,
        action: action ?? this.action,
        targetKinds: targetKinds ?? this.targetKinds,
        excludedNamespaces: excludedNamespaces ?? this.excludedNamespaces,
        description: description ?? this.description,
        params: params ?? this.params,
      );
}

class PolicyWizardController extends WizardController<PolicyForm> {
  @override
  String get wizardType => 'policy';

  @override
  String get resourceListKind => 'clusterpolicies';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Template',
          description: 'Pick a starter template',
        ),
        WizardStep(
          title: 'Configure',
          description: 'Engine, action, scope, parameters',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  PolicyForm buildInitialForm() => const PolicyForm();

  /// Pick a template. Auto-fills name, targetKinds, description, and
  /// params from the template's defaults. If a default engine was
  /// previously set (by `setEngine`), preserve it so the action
  /// computes against the operator's already-picked engine.
  void pickTemplate(String templateId) {
    final t = findPolicyTemplate(templateId);
    if (t == null) return;
    final currentEngine = state.form.engine;
    final nextEngine = t.engines.contains(currentEngine)
        ? currentEngine
        : (t.engines.isNotEmpty ? t.engines.first : '');
    updateForm((f) => f.copyWith(
          templateId: t.id,
          name: f.name.isEmpty ? t.id : f.name,
          targetKinds: List<String>.from(t.targetKinds),
          description: t.description,
          params: defaultParamsFor(t),
          engine: nextEngine,
          action: nextEngine.isEmpty
              ? ''
              : defaultActionFor(t, nextEngine),
        ));
  }

  /// Switch engine. Re-defaults `action` to the new engine's default
  /// for the picked template.
  void setEngine(String engine) {
    if (state.form.engine == engine) return;
    final t = findPolicyTemplate(state.form.templateId);
    final nextAction = t == null ? state.form.action : defaultActionFor(t, engine);
    updateForm((f) => f.copyWith(engine: engine, action: nextAction));
  }

  void setParam(String key, Object value) {
    final next = Map<String, dynamic>.from(state.form.params);
    next[key] = value;
    updateForm((f) => f.copyWith(params: next));
  }

  @override
  Map<String, dynamic> toPreviewBody(PolicyForm form) {
    final body = <String, dynamic>{
      'templateId': form.templateId,
      'engine': form.engine,
      'name': form.name,
      'action': form.action,
      'targetKinds': form.targetKinds,
    };
    if (form.excludedNamespaces.isNotEmpty) {
      body['excludedNamespaces'] = form.excludedNamespaces;
    }
    if (form.description.trim().isNotEmpty) {
      body['description'] = form.description.trim();
    }
    if (form.params.isNotEmpty) {
      body['params'] = form.params;
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'templateId') return 0;
    const known = {
      'engine',
      'name',
      'action',
      'targetKinds',
      'description',
      'excludedNamespaces',
    };
    if (known.contains(fieldPath)) return 1;
    if (fieldPath.startsWith('params.')) return 1;
    if (fieldPath.startsWith('targetKinds[') ||
        fieldPath.startsWith('excludedNamespaces[')) {
      return 1;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(PolicyForm form, int stepIndex) {
    if (stepIndex == 0) {
      if (form.templateId.isEmpty) {
        return {'templateId': 'Pick a template before continuing'};
      }
      return const <String, String>{};
    }
    if (stepIndex == 1) {
      final out = <String, String>{};
      if (form.name.trim().isEmpty) {
        out['name'] = 'Name is required';
      }
      if (form.engine.isEmpty) {
        out['engine'] = 'Engine is required';
      }
      if (form.action.isEmpty) {
        out['action'] = 'Action is required';
      }
      if (form.targetKinds.isEmpty) {
        out['targetKinds'] = 'At least one target kind is required';
      }
      // Required-param gates: registries / labels can't be empty
      // string lists. Mirrors backend's `validateParams` so the
      // operator gets an inline message before the round-trip.
      final t = findPolicyTemplate(form.templateId);
      if (t != null) {
        for (final p in t.paramFields) {
          if (!p.required) continue;
          final v = form.params[p.key];
          if (p.type == 'stringList') {
            if (v is! List || v.isEmpty) {
              out['params.${p.key}'] = '${p.label} is required';
            }
          } else if (p.type == 'string') {
            if (v is! String || v.trim().isEmpty) {
              out['params.${p.key}'] = '${p.label} is required';
            }
          }
        }
      }
      return out;
    }
    return const <String, String>{};
  }
}

final policyWizardProvider = AutoDisposeNotifierProvider.family<
    PolicyWizardController, WizardState<PolicyForm>, WizardKey>(
  PolicyWizardController.new,
);
