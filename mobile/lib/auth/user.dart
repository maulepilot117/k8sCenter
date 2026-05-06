// User + RBAC summary models. Match the backend's /v1/auth/me response
// shape (`backend/internal/auth/provider.go::User` and RBACSummary).
//
// Keeping these as plain Dart classes — Freezed adds compile-time
// codegen, which is heavy for what's effectively a flat DTO. PR-1c+
// can migrate to Freezed when more models accumulate.

class UserInfo {
  const UserInfo({
    required this.id,
    required this.username,
    required this.provider,
    required this.roles,
    this.email,
    this.kubernetesUsername,
    this.kubernetesGroups = const [],
  });

  factory UserInfo.fromJson(Map<String, dynamic> json) {
    return UserInfo(
      id: json['id'] as String? ?? '',
      username: json['username'] as String? ?? '',
      provider: json['provider'] as String? ?? 'local',
      email: json['email'] as String?,
      kubernetesUsername: json['kubernetesUsername'] as String?,
      kubernetesGroups: ((json['kubernetesGroups'] as List?) ?? const [])
          .whereType<String>()
          .toList(),
      roles: ((json['roles'] as List?) ?? const [])
          .whereType<String>()
          .toList(),
    );
  }

  final String id;
  final String username;
  final String provider;
  final String? email;
  final String? kubernetesUsername;
  final List<String> kubernetesGroups;
  final List<String> roles;

  bool get isAdmin => roles.contains('admin');
}

/// RBAC summary as returned by /v1/auth/me. Carried opaquely in PR-1b —
/// PR-1d and onwards parse the per-namespace permissions for action gating.
class RBACSummary {
  const RBACSummary({this.raw = const {}});

  factory RBACSummary.fromJson(Map<String, dynamic>? json) =>
      RBACSummary(raw: json ?? const {});

  final Map<String, dynamic> raw;
}
