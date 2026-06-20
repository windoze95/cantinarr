/// Represents the currently authenticated user.
class UserProfile {
  final int id;
  final String username;
  final String role;
  final List<String> permissions;

  /// Whether the account has a password set. Only populated by the `/me`
  /// endpoint; login/connect responses leave this null (unknown).
  final bool? hasPassword;

  /// Admin-controlled policy: whether this account may create a password /
  /// register a passkey. Both default off — a new user just gets a session.
  final bool passwordEnabled;
  final bool passkeyEnabled;

  const UserProfile({
    required this.id,
    required this.username,
    required this.role,
    this.permissions = const [],
    this.hasPassword,
    this.passwordEnabled = false,
    this.passkeyEnabled = false,
  });

  bool get isAdmin => role == 'admin';

  bool hasPermission(String permission) =>
      isAdmin || permissions.contains(permission);

  /// Admins always retain both methods; otherwise the policy flags govern.
  bool get canUsePassword => isAdmin || passwordEnabled;
  bool get canUsePasskey => isAdmin || passkeyEnabled;

  factory UserProfile.fromJson(Map<String, dynamic> json) => UserProfile(
        id: json['id'] as int,
        username: json['username'] as String,
        role: json['role'] as String? ?? 'user',
        permissions: (json['permissions'] as List<dynamic>?)
                ?.map((p) => p as String)
                .toList() ??
            const [],
        hasPassword: json['has_password'] as bool?,
        passwordEnabled: json['password_enabled'] as bool? ?? false,
        passkeyEnabled: json['passkey_enabled'] as bool? ?? false,
      );

  UserProfile copyWith({bool? hasPassword}) => UserProfile(
        id: id,
        username: username,
        role: role,
        permissions: permissions,
        hasPassword: hasPassword ?? this.hasPassword,
        passwordEnabled: passwordEnabled,
        passkeyEnabled: passkeyEnabled,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'username': username,
        'role': role,
        'permissions': permissions,
        if (hasPassword != null) 'has_password': hasPassword,
        'password_enabled': passwordEnabled,
        'passkey_enabled': passkeyEnabled,
      };
}
