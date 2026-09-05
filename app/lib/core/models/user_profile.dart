/// What a kids account may see, as the account's own profile carries it:
/// enough for the Account line in Settings. Mirrors the server's
/// `content_limits`; null on an unrestricted account.
class ContentLimits {
  final String maxMovieRating;
  final String maxTvRating;
  final String ratingRegion;

  const ContentLimits({
    required this.maxMovieRating,
    required this.maxTvRating,
    required this.ratingRegion,
  });

  factory ContentLimits.fromJson(Map<String, dynamic> json) => ContentLimits(
        maxMovieRating: json['max_movie_rating'] as String? ?? '',
        maxTvRating: json['max_tv_rating'] as String? ?? '',
        ratingRegion: json['rating_region'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'max_movie_rating': maxMovieRating,
        'max_tv_rating': maxTvRating,
        'rating_region': ratingRegion,
      };
}

/// Represents the currently authenticated user.
class UserProfile {
  final bool ssoLinked;
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
  /// they submit one (from the access guide). [plexInvitedAt] is when their
  /// invite went out, derived by the server from their live Plex share.
  final String plexEmail;
  final String? plexInvitedAt;

  /// A kids account: the server filters every title this account is shown.
  /// [contentLimits] is the summary it may render; the app filters nothing.
  final bool child;
  final ContentLimits? contentLimits;

  const UserProfile({
    required this.id,
    this.ssoLinked = false,
    required this.username,
    required this.role,
    this.permissions = const [],
    this.hasPassword,
    this.passwordEnabled = false,
    this.passkeyEnabled = false,
    this.plexEmail = '',
    this.plexInvitedAt,
    this.child = false,
    this.contentLimits,
  });

  bool get isAdmin => role == 'admin';

  bool hasPermission(String permission) =>
      isAdmin || permissions.contains(permission);

  /// Admins always retain both methods; otherwise the policy flags govern.
  bool get canUsePassword => isAdmin || passwordEnabled;
  bool get canUsePasskey => isAdmin || passkeyEnabled;

  factory UserProfile.fromJson(Map<String, dynamic> json) => UserProfile(
        id: json['id'] as int,
        ssoLinked: json['sso_linked'] as bool? ?? false,
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
        child: json['child'] as bool? ?? false,
        contentLimits: json['content_limits'] is Map<String, dynamic>
            ? ContentLimits.fromJson(json['content_limits'] as Map<String, dynamic>)
            : null,
      );

  UserProfile copyWith({
    bool? hasPassword,
    String? plexEmail,
    bool clearPlexInvitedAt = false,
  }) =>
      UserProfile(
        id: id,
        ssoLinked: ssoLinked,
        username: username,
        role: role,
        permissions: permissions,
        hasPassword: hasPassword ?? this.hasPassword,
        passwordEnabled: passwordEnabled,
        passkeyEnabled: passkeyEnabled,
        plexEmail: plexEmail ?? this.plexEmail,
        plexInvitedAt: clearPlexInvitedAt ? null : plexInvitedAt,
        child: child,
        contentLimits: contentLimits,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'sso_linked': ssoLinked,
        'username': username,
        'role': role,
        'permissions': permissions,
        if (hasPassword != null) 'has_password': hasPassword,
        'password_enabled': passwordEnabled,
        'passkey_enabled': passkeyEnabled,
        'plex_email': plexEmail,
        if (plexInvitedAt != null) 'plex_invited_at': plexInvitedAt,
        'child': child,
        if (contentLimits != null) 'content_limits': contentLimits!.toJson(),
      };
}
