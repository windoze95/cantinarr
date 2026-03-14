/// Represents the currently authenticated user.
class UserProfile {
  final int id;
  final String username;
  final String role;

  const UserProfile({
    required this.id,
    required this.username,
    required this.role,
  });

  bool get isAdmin => role == 'admin';

  factory UserProfile.fromJson(Map<String, dynamic> json) => UserProfile(
        id: json['id'] as int,
        username: json['username'] as String,
        role: json['role'] as String? ?? 'user',
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'username': username,
        'role': role,
      };
}
