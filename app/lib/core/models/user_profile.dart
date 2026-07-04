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

  /// The email this user shared for their Plex server invite. Empty until
  /// they submit one (from the Watch on Plex guide). [plexInvitedAt] is set
  /// once Cantinarr sent their invite (one-tap or auto).
  final String plexEmail;
  final String? plexInvitedAt;

  const UserProfile({
    required this.id,
    required this.username,
    required this.role,
    this.permissions = const [],
    this.hasPassword,
    this.passwordEnabled = false,
    this.passkeyEnabled = false,
    this.plexEmail = '',
    this.plexInvitedAt,
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
        plexEmail: json['plex_email'] as String? ?? '',
        plexInvitedAt: json['plex_invited_at'] as String?,
      );

  UserProfile copyWith({
    bool? hasPassword,
    String? plexEmail,
    bool clearPlexInvitedAt = false,
  }) =>
      UserProfile(
        id: id,
        username: username,
        role: role,
        permissions: permissions,
        hasPassword: hasPassword ?? this.hasPassword,
        passwordEnabled: passwordEnabled,
        passkeyEnabled: passkeyEnabled,
        plexEmail: plexEmail ?? this.plexEmail,
        plexInvitedAt: clearPlexInvitedAt ? null : plexInvitedAt,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'username': username,
        'role': role,
        'permissions': permissions,
        if (hasPassword != null) 'has_password': hasPassword,
        'password_enabled': passwordEnabled,
        'passkey_enabled': passkeyEnabled,
        'plex_email': plexEmail,
        if (plexInvitedAt != null) 'plex_invited_at': plexInvitedAt,
      };
}
