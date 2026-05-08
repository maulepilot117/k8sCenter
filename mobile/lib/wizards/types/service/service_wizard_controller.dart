// Service wizard controller. Mirrors `frontend/islands/ServiceWizard.tsx`.
//
// 2 steps: Configure (name + namespace + type + selector + ports) →
// Review.
//
// Wire format (`backend/internal/wizard/service.go:18`):
//   {
//     name, namespace, type,
//     labels?:    Map<String,String>,
//     selector:   Map<String,String>,
//     ports:      [{name?, port, targetPort, protocol?, nodePort?}, ...]
//   }
//
// Server validates: DNS-1123 name/namespace, type ∈ {ClusterIP,
// NodePort, LoadBalancer}, ≥1 selector entry, ≥1 port, port range,
// per-port name regex, no duplicate ports.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// Allowed service types — superset of valid k8s service types,
/// matches `validTypes` in `service.go:Validate`.
const List<String> kServiceTypes = ['ClusterIP', 'NodePort', 'LoadBalancer'];

/// Common protocols. Backend defaults to TCP when empty; we surface
/// the picker explicitly so the operator never wonders what's chosen.
const List<String> kServiceProtocols = ['TCP', 'UDP', 'SCTP'];

class ServicePort {
  const ServicePort({
    this.name = '',
    this.port = 0,
    this.targetPort = 0,
    this.protocol = 'TCP',
    this.nodePort = 0,
  });

  final String name;
  final int port;
  final int targetPort;
  final String protocol;
  final int nodePort;

  ServicePort copyWith({
    String? name,
    int? port,
    int? targetPort,
    String? protocol,
    int? nodePort,
  }) =>
      ServicePort(
        name: name ?? this.name,
        port: port ?? this.port,
        targetPort: targetPort ?? this.targetPort,
        protocol: protocol ?? this.protocol,
        nodePort: nodePort ?? this.nodePort,
      );

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{
      'port': port,
      'targetPort': targetPort,
    };
    if (name.isNotEmpty) out['name'] = name;
    if (protocol.isNotEmpty) out['protocol'] = protocol;
    if (nodePort > 0) out['nodePort'] = nodePort;
    return out;
  }

  bool get isEmpty => port == 0 && targetPort == 0 && name.isEmpty;
}

class ServiceForm {
  const ServiceForm({
    this.name = '',
    this.namespace = '',
    this.type = 'ClusterIP',
    this.labels = const <KeyValuePair>[],
    this.selector = const <KeyValuePair>[],
    this.ports = const <ServicePort>[ServicePort()],
  });

  final String name;
  final String namespace;
  final String type;
  final List<KeyValuePair> labels;
  final List<KeyValuePair> selector;
  final List<ServicePort> ports;

  ServiceForm copyWith({
    String? name,
    String? namespace,
    String? type,
    List<KeyValuePair>? labels,
    List<KeyValuePair>? selector,
    List<ServicePort>? ports,
  }) =>
      ServiceForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        type: type ?? this.type,
        labels: labels ?? this.labels,
        selector: selector ?? this.selector,
        ports: ports ?? this.ports,
      );

  Map<String, String> _kvAsMap(List<KeyValuePair> kv) {
    final out = <String, String>{};
    for (final p in kv) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }

  /// Strip empty trailing port rows; emit JSON list. Empty list is
  /// fine — the server returns a clear "ports required" 422.
  List<Map<String, dynamic>> portsAsJson() {
    return [
      for (final p in ports)
        if (!p.isEmpty) p.toJson(),
    ];
  }

  Map<String, String> selectorAsMap() => _kvAsMap(selector);
  Map<String, String> labelsAsMap() => _kvAsMap(labels);
}

class ServiceWizardController extends WizardController<ServiceForm> {
  @override
  String get wizardType => 'service';

  @override
  String get resourceListKind => 'services';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Name, namespace, type, selector, ports',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  ServiceForm buildInitialForm() => const ServiceForm();

  @override
  Map<String, dynamic> toPreviewBody(ServiceForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'type': form.type,
      'selector': form.selectorAsMap(),
      'ports': form.portsAsJson(),
    };
    final labels = form.labelsAsMap();
    if (labels.isNotEmpty) body['labels'] = labels;
    return body;
  }

  /// Single Configure step. Known paths: `name`, `namespace`, `type`,
  /// `selector`, `labels`, `ports`, `ports[N].port|targetPort|name`.
  /// Unknown paths return null so the controller surfaces them via
  /// [WizardState.unrouted].
  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'type' ||
        fieldPath == 'selector' ||
        fieldPath == 'labels' ||
        fieldPath == 'ports' ||
        fieldPath.startsWith('ports[') ||
        fieldPath.startsWith('selector[') ||
        fieldPath.startsWith('labels[')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(ServiceForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (!kServiceTypes.contains(form.type)) {
      out['type'] = 'Pick a service type';
    }
    if (form.selectorAsMap().isEmpty) {
      out['selector'] = 'Selector must have at least one key-value pair';
    }
    final filledPorts = form.ports.where((p) => !p.isEmpty).toList();
    if (filledPorts.isEmpty) {
      out['ports'] = 'Add at least one port';
    } else {
      for (var i = 0; i < filledPorts.length; i++) {
        final p = filledPorts[i];
        if (p.port < 1 || p.port > 65535) {
          out['ports[$i].port'] = 'Port must be between 1 and 65535';
        }
        if (p.targetPort < 1 || p.targetPort > 65535) {
          out['ports[$i].targetPort'] =
              'Target port must be between 1 and 65535';
        }
      }
    }
    return out;
  }
}

final serviceWizardProvider = AutoDisposeNotifierProvider.family<
    ServiceWizardController,
    WizardState<ServiceForm>,
    WizardKey>(ServiceWizardController.new);
