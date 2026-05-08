// Ingress wizard controller. Mirrors
// `frontend/islands/IngressWizard.tsx` and ports the wire contract
// from `backend/internal/wizard/ingress.go:25`.
//
// Wire format (`IngressInput`):
//   {
//     name, namespace,
//     ingressClassName?: string,
//     rules: [{host, paths: [{path, pathType, serviceName, servicePort}]}],
//     tls?:  [{hosts: [...], secretName}]
//   }
//
// One Configure step + Review. Field paths the backend surfaces:
//   `name`, `namespace`, `ingressClassName`,
//   `rules[N].host`, `rules[N].paths`,
//   `rules[N].paths[N].path`, `rules[N].paths[N].pathType`,
//   `rules[N].paths[N].serviceName`, `rules[N].paths[N].servicePort`,
//   `tls[N].hosts`, `tls[N].secretName`.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// Path types the backend accepts. Order matches the web wizard's
/// dropdown — `Prefix` is the most common default.
const List<String> kIngressPathTypes = [
  'Prefix',
  'Exact',
  'ImplementationSpecific',
];

class IngressPath {
  const IngressPath({
    this.path = '/',
    this.pathType = 'Prefix',
    this.serviceName = '',
    this.servicePort = 0,
  });

  final String path;
  final String pathType;
  final String serviceName;
  final int servicePort;

  IngressPath copyWith({
    String? path,
    String? pathType,
    String? serviceName,
    int? servicePort,
  }) =>
      IngressPath(
        path: path ?? this.path,
        pathType: pathType ?? this.pathType,
        serviceName: serviceName ?? this.serviceName,
        servicePort: servicePort ?? this.servicePort,
      );

  Map<String, dynamic> toJson() => {
        'path': path,
        'pathType': pathType,
        'serviceName': serviceName,
        'servicePort': servicePort,
      };
}

class IngressRule {
  const IngressRule({
    this.host = '',
    this.paths = const <IngressPath>[IngressPath()],
  });

  final String host;
  final List<IngressPath> paths;

  IngressRule copyWith({String? host, List<IngressPath>? paths}) =>
      IngressRule(host: host ?? this.host, paths: paths ?? this.paths);

  Map<String, dynamic> toJson() => {
        'host': host,
        'paths': [for (final p in paths) p.toJson()],
      };
}

class IngressTls {
  const IngressTls({this.hosts = const <String>[], this.secretName = ''});

  final List<String> hosts;
  final String secretName;

  IngressTls copyWith({List<String>? hosts, String? secretName}) =>
      IngressTls(
        hosts: hosts ?? this.hosts,
        secretName: secretName ?? this.secretName,
      );

  Map<String, dynamic> toJson() => {
        'hosts': hosts,
        'secretName': secretName,
      };
}

class IngressForm {
  const IngressForm({
    this.name = '',
    this.namespace = '',
    this.ingressClassName = '',
    this.rules = const <IngressRule>[IngressRule()],
    this.tls = const <IngressTls>[],
  });

  final String name;
  final String namespace;
  final String ingressClassName;
  final List<IngressRule> rules;
  final List<IngressTls> tls;

  IngressForm copyWith({
    String? name,
    String? namespace,
    String? ingressClassName,
    List<IngressRule>? rules,
    List<IngressTls>? tls,
  }) =>
      IngressForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        ingressClassName: ingressClassName ?? this.ingressClassName,
        rules: rules ?? this.rules,
        tls: tls ?? this.tls,
      );
}

class IngressWizardController extends WizardController<IngressForm> {
  @override
  String get wizardType => 'ingress';

  @override
  String get resourceListKind => 'ingresses';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Name, rules, and optional TLS',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  IngressForm buildInitialForm() => const IngressForm();

  @override
  Map<String, dynamic> toPreviewBody(IngressForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'rules': [for (final r in form.rules) r.toJson()],
    };
    if (form.ingressClassName.isNotEmpty) {
      body['ingressClassName'] = form.ingressClassName;
    }
    // Strip TLS rows that are entirely empty (operator clicked "Add"
    // then typed nothing). Backend `IngressTLS` validation rejects
    // empty hosts/secretName, so leaving an empty row in the body
    // would 422 even though the operator never meant to add TLS.
    final tlsEmitted = [
      for (final t in form.tls)
        if (t.hosts.isNotEmpty || t.secretName.isNotEmpty) t.toJson(),
    ];
    if (tlsEmitted.isNotEmpty) body['tls'] = tlsEmitted;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'ingressClassName' ||
        fieldPath == 'rules' ||
        fieldPath.startsWith('rules[') ||
        fieldPath.startsWith('tls[') ||
        fieldPath == 'tls') {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(IngressForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    // Validate any partially-filled TLS rows so the operator gets a
    // pre-preview cue instead of a backend 422 round-trip. Rows that
    // are wholly empty are stripped in `toPreviewBody`.
    for (var i = 0; i < form.tls.length; i++) {
      final t = form.tls[i];
      final partiallyFilled =
          t.hosts.isNotEmpty || t.secretName.isNotEmpty;
      if (!partiallyFilled) continue;
      if (t.secretName.trim().isEmpty) {
        out['tls[$i].secretName'] = 'TLS secret name is required';
      }
      if (t.hosts.isEmpty) {
        out['tls[$i].hosts'] = 'At least one host is required';
      }
    }
    if (form.rules.isEmpty) {
      out['rules'] = 'Add at least one rule';
    } else {
      for (var i = 0; i < form.rules.length; i++) {
        final rule = form.rules[i];
        if (rule.paths.isEmpty) {
          out['rules[$i].paths'] = 'At least one path is required';
          continue;
        }
        for (var j = 0; j < rule.paths.length; j++) {
          final p = rule.paths[j];
          if (p.serviceName.trim().isEmpty) {
            out['rules[$i].paths[$j].serviceName'] = 'Service name is required';
          }
          if (p.servicePort < 1 || p.servicePort > 65535) {
            out['rules[$i].paths[$j].servicePort'] =
                'Port must be 1–65535';
          }
        }
      }
    }
    return out;
  }
}

final ingressWizardProvider = AutoDisposeNotifierProvider.family<
    IngressWizardController,
    WizardState<IngressForm>,
    WizardKey>(IngressWizardController.new);
