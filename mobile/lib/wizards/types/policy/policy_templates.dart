// Dart port of `frontend/lib/policy-templates.ts`. Registry of the 8
// policy templates the backend wizard supports. Adding a template
// here without adding it to the web frontend (or vice versa) is a
// drift bug — keep both files in lockstep.
//
// Each template carries its own param schema; the wizard's Configure
// step renders the param form generically by walking `paramFields`.

class PolicyParamField {
  const PolicyParamField({
    required this.key,
    required this.label,
    required this.type,
    required this.description,
    this.defaultValue,
    this.required = false,
  });

  /// Key under `params: { ... }` in the wizard preview body.
  final String key;
  final String label;

  /// `boolean` | `stringList` | `string`. Drives the rendered control.
  final String type;
  final String description;

  /// Default value when the operator hasn't touched the field.
  /// `bool` for boolean, `List<String>` for stringList, `String` for
  /// string. Untyped because each form variant reads its own type.
  final Object? defaultValue;
  final bool required;
}

class PolicyTemplateInfo {
  const PolicyTemplateInfo({
    required this.id,
    required this.name,
    required this.category,
    required this.description,
    required this.severity,
    required this.targetKinds,
    required this.engines,
    required this.paramFields,
    required this.defaultKyvernoAction,
    required this.defaultGatekeeperAction,
  });

  final String id;
  final String name;
  final String category;
  final String description;

  /// `high` | `medium` | `low`. Drives the severity badge in the
  /// template picker.
  final String severity;
  final List<String> targetKinds;

  /// Subset of `["kyverno", "gatekeeper"]`. Templates that support
  /// only one engine must hide the other when the cluster has both
  /// installed.
  final List<String> engines;

  final List<PolicyParamField> paramFields;
  final String defaultKyvernoAction; // "Enforce" | "Audit"
  final String defaultGatekeeperAction; // "deny" | "dryrun" | "warn"
}

/// Display-order categories. Mirrors the web frontend.
const List<String> kPolicyCategories = [
  'Pod Security',
  'Image Policies',
  'Resource Management',
  'Labeling',
];

const List<PolicyTemplateInfo> kPolicyTemplates = [
  // --- Pod Security ---
  PolicyTemplateInfo(
    id: 'disallow-privileged',
    name: 'Disallow Privileged Containers',
    category: 'Pod Security',
    description:
        'Privileged mode disables most security mechanisms and must not be allowed.',
    severity: 'high',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [],
    defaultKyvernoAction: 'Enforce',
    defaultGatekeeperAction: 'deny',
  ),
  PolicyTemplateInfo(
    id: 'disallow-root',
    name: 'Disallow Root User',
    category: 'Pod Security',
    description: 'Containers must run as a non-root user.',
    severity: 'high',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [],
    defaultKyvernoAction: 'Enforce',
    defaultGatekeeperAction: 'deny',
  ),
  PolicyTemplateInfo(
    id: 'disallow-privilege-escalation',
    name: 'Disallow Privilege Escalation',
    category: 'Pod Security',
    description:
        'Containers must not allow privilege escalation via setuid or setgid.',
    severity: 'high',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [],
    defaultKyvernoAction: 'Enforce',
    defaultGatekeeperAction: 'deny',
  ),
  PolicyTemplateInfo(
    id: 'restrict-capabilities',
    name: 'Restrict Capabilities',
    category: 'Pod Security',
    description: 'Drop all capabilities and allow only specific additions.',
    severity: 'medium',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [
      PolicyParamField(
        key: 'dropAll',
        label: 'Drop ALL capabilities',
        type: 'boolean',
        description: 'Require all containers to drop ALL capabilities.',
        defaultValue: true,
      ),
      PolicyParamField(
        key: 'allowedAdd',
        label: 'Allowed capabilities',
        type: 'stringList',
        description: 'Capabilities that may be added (e.g. NET_BIND_SERVICE).',
        defaultValue: <String>[],
      ),
    ],
    defaultKyvernoAction: 'Audit',
    defaultGatekeeperAction: 'dryrun',
  ),
  // --- Image Policies ---
  PolicyTemplateInfo(
    id: 'allowed-registries',
    name: 'Restrict Image Registries',
    category: 'Image Policies',
    description: 'Images must come from approved container registries.',
    severity: 'high',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [
      PolicyParamField(
        key: 'registries',
        label: 'Allowed registries',
        type: 'stringList',
        description:
            'Registry prefixes that are allowed (e.g. "ghcr.io/", "registry.k8s.io/").',
        defaultValue: <String>[],
        required: true,
      ),
    ],
    defaultKyvernoAction: 'Enforce',
    defaultGatekeeperAction: 'deny',
  ),
  PolicyTemplateInfo(
    id: 'disallow-latest-tag',
    name: 'Disallow Latest Tag',
    category: 'Image Policies',
    description: "Images must have an explicit tag; ':latest' is not allowed.",
    severity: 'medium',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [],
    defaultKyvernoAction: 'Audit',
    defaultGatekeeperAction: 'dryrun',
  ),
  // --- Resource Management ---
  PolicyTemplateInfo(
    id: 'require-resource-limits',
    name: 'Require Resource Limits',
    category: 'Resource Management',
    description:
        'All containers must define CPU and memory resource limits.',
    severity: 'medium',
    targetKinds: ['Pod'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [
      PolicyParamField(
        key: 'requireCpu',
        label: 'Require CPU limits',
        type: 'boolean',
        description: 'Require CPU limits on all containers.',
        defaultValue: true,
      ),
      PolicyParamField(
        key: 'requireMemory',
        label: 'Require memory limits',
        type: 'boolean',
        description: 'Require memory limits on all containers.',
        defaultValue: true,
      ),
    ],
    defaultKyvernoAction: 'Audit',
    defaultGatekeeperAction: 'dryrun',
  ),
  // --- Labeling ---
  PolicyTemplateInfo(
    id: 'require-labels',
    name: 'Require Labels',
    category: 'Labeling',
    description:
        'Resources must have specified labels for ownership and lifecycle tracking.',
    severity: 'medium',
    targetKinds: ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet'],
    engines: ['kyverno', 'gatekeeper'],
    paramFields: [
      PolicyParamField(
        key: 'labels',
        label: 'Required labels',
        type: 'stringList',
        description:
            'Label keys that must be present (e.g. "app.kubernetes.io/name").',
        defaultValue: <String>[],
        required: true,
      ),
    ],
    defaultKyvernoAction: 'Audit',
    defaultGatekeeperAction: 'dryrun',
  ),
];

PolicyTemplateInfo? findPolicyTemplate(String id) {
  for (final t in kPolicyTemplates) {
    if (t.id == id) return t;
  }
  return null;
}

/// Engine-aware default action. Falls through to Audit/dryrun when
/// the template doesn't define the engine.
String defaultActionFor(PolicyTemplateInfo template, String engine) {
  switch (engine) {
    case 'kyverno':
      return template.defaultKyvernoAction;
    case 'gatekeeper':
      return template.defaultGatekeeperAction;
    default:
      return '';
  }
}

/// Default `params` map for a freshly-picked template. Picks each
/// field's `defaultValue` so the form starts populated and the
/// preview body has stable defaults.
Map<String, dynamic> defaultParamsFor(PolicyTemplateInfo template) {
  final out = <String, dynamic>{};
  for (final p in template.paramFields) {
    if (p.defaultValue != null) out[p.key] = p.defaultValue;
  }
  return out;
}
