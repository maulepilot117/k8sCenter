// CPU/memory requests + limits sub-widget. Mirrors the backend's
// `ResourcesInput` (`backend/internal/wizard/container.go:39`):
//
//   { requestCpu, requestMemory, limitCpu, limitMemory }
//
// All fields are optional strings — the operator types Kubernetes
// quantity literals like `100m`, `512Mi`, `1Gi`. Server validates via
// `resource.ParseQuantity` and surfaces a 422 with field path
// `<prefix>.resources.<key>` when the literal is malformed.
//
// Used by Deployment, Job, CronJob, DaemonSet, StatefulSet.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

/// Mirrors backend `ResourcesInput`. All four fields default to empty
/// strings — wizards that want a starting recommendation can override
/// in their `buildInitialForm`.
class ResourcesData {
  const ResourcesData({
    this.requestCpu = '',
    this.requestMemory = '',
    this.limitCpu = '',
    this.limitMemory = '',
  });

  final String requestCpu;
  final String requestMemory;
  final String limitCpu;
  final String limitMemory;

  ResourcesData copyWith({
    String? requestCpu,
    String? requestMemory,
    String? limitCpu,
    String? limitMemory,
  }) =>
      ResourcesData(
        requestCpu: requestCpu ?? this.requestCpu,
        requestMemory: requestMemory ?? this.requestMemory,
        limitCpu: limitCpu ?? this.limitCpu,
        limitMemory: limitMemory ?? this.limitMemory,
      );

  /// Strip blank fields. Empty list / null when nothing was set so
  /// the wizard can omit the `resources` field entirely from the
  /// preview body.
  Map<String, dynamic>? toJson() {
    final out = <String, dynamic>{};
    if (requestCpu.trim().isNotEmpty) out['requestCpu'] = requestCpu.trim();
    if (requestMemory.trim().isNotEmpty) {
      out['requestMemory'] = requestMemory.trim();
    }
    if (limitCpu.trim().isNotEmpty) out['limitCpu'] = limitCpu.trim();
    if (limitMemory.trim().isNotEmpty) {
      out['limitMemory'] = limitMemory.trim();
    }
    return out.isEmpty ? null : out;
  }

  bool get isEmpty =>
      requestCpu.trim().isEmpty &&
      requestMemory.trim().isEmpty &&
      limitCpu.trim().isEmpty &&
      limitMemory.trim().isEmpty;
}

class ResourcesFormSection extends StatelessWidget {
  const ResourcesFormSection({
    super.key,
    required this.resources,
    required this.onChanged,
    this.fieldErrors = const <String, String>{},
    this.fieldPrefix = 'resources',
  });

  final ResourcesData resources;
  final ValueChanged<ResourcesData> onChanged;

  /// Map of full field path → message. Keys looked up:
  ///   `<prefix>.requestCpu`, `<prefix>.requestMemory`,
  ///   `<prefix>.limitCpu`, `<prefix>.limitMemory`.
  final Map<String, String> fieldErrors;

  /// Backend field prefix. e.g. `resources` for Deployment;
  /// `container.resources` for Job/CronJob/DaemonSet/StatefulSet.
  final String fieldPrefix;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Resources',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'Use Kubernetes quantity literals — e.g. 100m for 0.1 CPU, '
            '128Mi or 1Gi for memory.',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: TextFormField(
                  initialValue: resources.requestCpu,
                  onChanged: (v) =>
                      onChanged(resources.copyWith(requestCpu: v)),
                  decoration: InputDecoration(
                    labelText: 'Request CPU',
                    hintText: '100m',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: fieldErrors['$fieldPrefix.requestCpu'],
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: TextFormField(
                  initialValue: resources.requestMemory,
                  onChanged: (v) =>
                      onChanged(resources.copyWith(requestMemory: v)),
                  decoration: InputDecoration(
                    labelText: 'Request memory',
                    hintText: '128Mi',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: fieldErrors['$fieldPrefix.requestMemory'],
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextFormField(
                  initialValue: resources.limitCpu,
                  onChanged: (v) =>
                      onChanged(resources.copyWith(limitCpu: v)),
                  decoration: InputDecoration(
                    labelText: 'Limit CPU',
                    hintText: '500m',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: fieldErrors['$fieldPrefix.limitCpu'],
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: TextFormField(
                  initialValue: resources.limitMemory,
                  onChanged: (v) =>
                      onChanged(resources.copyWith(limitMemory: v)),
                  decoration: InputDecoration(
                    labelText: 'Limit memory',
                    hintText: '512Mi',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: fieldErrors['$fieldPrefix.limitMemory'],
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
