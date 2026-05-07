// Mirrors `backend/internal/store/clusters.go::ClusterRecord` for the
// fields mobile actually renders. Sensitive fields (CAData, AuthData) are
// stripped server-side before serialization.

class Cluster {
  const Cluster({
    required this.id,
    required this.name,
    required this.isLocal,
    required this.status,
    this.displayName,
    this.apiServerUrl,
    this.k8sVersion,
    this.nodeCount = 0,
    this.statusMessage,
  });

  factory Cluster.fromJson(Map<String, dynamic> json) {
    return Cluster(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      displayName: json['displayName'] as String?,
      apiServerUrl: json['apiServerUrl'] as String?,
      isLocal: json['isLocal'] as bool? ?? false,
      status: json['status'] as String? ?? 'unknown',
      statusMessage: json['statusMessage'] as String?,
      k8sVersion: json['k8sVersion'] as String?,
      nodeCount: json['nodeCount'] as int? ?? 0,
    );
  }

  final String id;
  final String name;
  final String? displayName;
  final String? apiServerUrl;
  final bool isLocal;
  final String status;
  final String? statusMessage;
  final String? k8sVersion;
  final int nodeCount;

  String get label => displayName ?? name;
}
