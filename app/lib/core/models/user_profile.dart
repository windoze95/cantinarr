/// Represents the currently authenticated user.
class UserProfile {
  final int id;
  final String username;
  final String role;
  final List<String> permissions;

  const UserProfile({
    required this.id,
    required this.username,
    required this.role,
    this.permissions = const [],
  });

  bool get isAdmin => role == 'admin';

  bool hasPermission(String permission) =>
      isAdmin || permissions.contains(permission);

  factory UserProfile.fromJson(Map<String, dynamic> json) => UserProfile(
        id: json['id'] as int,
        username: json['username'] as String,
        role: json['role'] as String? ?? 'user',
        permissions: (json['permissions'] as List<dynamic>?)
                ?.map((p) => p as String)
                .toList() ??
            const [],
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'username': username,
        'role': role,
        'permissions': permissions,
      };
}
