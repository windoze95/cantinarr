import 'package:dio/dio.dart';
import '../../../core/config/app_config.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';
import 'server_status.dart';

/// Handles authentication requests against the Cantinarr backend.
///
/// Uses a plain Dio instance (no auth interceptor) since these endpoints
/// are called before/during authentication.
class AuthService {
  Dio _createDio(String serverUrl) => Dio(BaseOptions(
        baseUrl: serverUrl,
        connectTimeout: AppConfig.requestTimeout,
        receiveTimeout: AppConfig.requestTimeout,
        headers: {'Content-Type': 'application/json'},
      ));

  /// Check server status (needs setup, webauthn available).
  Future<ServerStatus> getServerStatus(String serverUrl) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get('/api/auth/status');
    return ServerStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Create admin account during first-run setup.
  Future<AuthResponse> setup(
    String serverUrl,
    String username,
    String password,
    String deviceName,
    String hardwareId,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/setup', data: {
      'username': username,
      'password': password,
      'device_name': deviceName,
      if (hardwareId.isNotEmpty) 'hardware_id': hardwareId,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Log in with username and password.
  Future<AuthResponse> login(
    String serverUrl,
    String username,
    String password,
    String deviceName,
    String hardwareId,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/login', data: {
      'username': username,
      'password': password,
      'device_name': deviceName,
      if (hardwareId.isNotEmpty) 'hardware_id': hardwareId,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Refresh an expired access token.
  Future<AuthResponse> refreshToken(
    String serverUrl,
    String refreshToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/refresh', data: {
      'refresh_token': refreshToken,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Redeem a connect token to authenticate a new device.
  Future<AuthResponse> redeemConnectToken(
    String serverUrl,
    String token,
    String deviceName,
    String hardwareId,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/connect', data: {
      'token': token,
      'device_name': deviceName,
      if (hardwareId.isNotEmpty) 'hardware_id': hardwareId,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetch the authenticated user's profile, including whether a password is
  /// set (`has_password`).
  Future<UserProfile> fetchMe(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get(
      '/api/auth/me',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return UserProfile.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Create or replace the authenticated user's password.
  Future<void> setPassword(
    String serverUrl,
    String accessToken,
    String newPassword,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.post(
      '/api/auth/password',
      data: {'password': newPassword},
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }

  /// Share or update the email the user wants their Plex invite sent to.
  /// The server notifies admins when the address is new or changed.
  Future<void> setPlexEmail(
    String serverUrl,
    String accessToken,
    String email,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.post(
      '/api/auth/plex-email',
      data: {'email': email},
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }

  /// Fetch server configuration (TMDB key, available services, etc.).
  Future<ServerConfig> fetchConfig(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get(
      '/api/config',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return ServerConfig.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Generate a connect link for a user (admin only).
  Future<ConnectTokenResponse> generateConnectToken(
    String serverUrl,
    String accessToken,
    String name,
    String serverUrlForLink,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/admin/connect-token',
      data: {'name': name, 'server_url': serverUrlForLink},
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return ConnectTokenResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// List all connected devices (admin only).
  Future<List<DeviceInfo>> listDevices(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get(
      '/api/admin/devices',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return (resp.data as List<dynamic>)
        .map((d) => DeviceInfo.fromJson(d as Map<String, dynamic>))
        .toList();
  }

  /// Revoke a device (admin only).
  Future<void> revokeDevice(
    String serverUrl,
    String accessToken,
    String deviceId,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.delete(
      '/api/admin/devices/$deviceId',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }

  /// List all user accounts (admin only).
  Future<List<UserSummary>> listUsers(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get(
      '/api/admin/users',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return (resp.data as List<dynamic>)
        .map((u) => UserSummary.fromJson(u as Map<String, dynamic>))
        .toList();
  }

  /// Change a user's role (admin only).
  Future<UserSummary> updateUserRole(
    String serverUrl,
    String accessToken,
    int userId,
    String role,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.patch(
      '/api/admin/users/$userId',
      data: {'role': role},
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return UserSummary.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Delete a user account (admin only).
  Future<void> deleteUser(
    String serverUrl,
    String accessToken,
    int userId,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.delete(
      '/api/admin/users/$userId',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }

  /// Enable or disable a user's password / passkey sign-in (admin only).
  /// Omitted fields are left unchanged; disabling is a real revoke server-side.
  Future<UserSummary> updateUserAuthMethods(
    String serverUrl,
    String accessToken,
    int userId, {
    bool? passwordEnabled,
    bool? passkeyEnabled,
  }) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.patch(
      '/api/admin/users/$userId/auth-methods',
      data: {
        if (passwordEnabled != null) 'password_enabled': passwordEnabled,
        if (passkeyEnabled != null) 'passkey_enabled': passkeyEnabled,
      },
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return UserSummary.fromJson(resp.data as Map<String, dynamic>);
  }

  // ─── Passkey API Methods ─────────────────────────────

  /// Begin passkey registration (authenticated).
  Future<BeginRegistrationResponse> beginPasskeyRegistration(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/auth/passkey/register/begin',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return BeginRegistrationResponse.fromJson(
        resp.data as Map<String, dynamic>);
  }

  /// Finish passkey registration (authenticated).
  /// The body is the raw WebAuthn credential creation response.
  Future<void> finishPasskeyRegistration(
    String serverUrl,
    String accessToken,
    String sessionId,
    String credentialName,
    Map<String, dynamic> credentialResponse,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.post(
      '/api/auth/passkey/register/finish',
      queryParameters: {
        'session_id': sessionId,
        'credential_name': credentialName,
      },
      data: credentialResponse,
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }

  /// Create a short-lived browser link for passkey setup.
  Future<PasskeySetupLinkResponse> createPasskeySetupLink(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/auth/passkey/setup-link',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return PasskeySetupLinkResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Begin passkey login (public).
  Future<BeginLoginResponse> beginPasskeyLogin(String serverUrl) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/passkey/login/begin');
    return BeginLoginResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Finish passkey login (public).
  Future<AuthResponse> finishPasskeyLogin(
    String serverUrl,
    String sessionId,
    Map<String, dynamic> assertionResponse,
    String deviceName,
    String hardwareId,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/auth/passkey/login/finish',
      queryParameters: {
        'session_id': sessionId,
        'device_name': deviceName,
        if (hardwareId.isNotEmpty) 'hardware_id': hardwareId,
      },
      data: assertionResponse,
    );
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// List user's passkeys (authenticated).
  Future<List<PasskeyInfoResponse>> listPasskeys(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.get(
      '/api/auth/passkeys',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return (resp.data as List<dynamic>)
        .map((p) => PasskeyInfoResponse.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  /// Delete a passkey (authenticated).
  Future<void> deletePasskey(
    String serverUrl,
    String accessToken,
    String credentialId,
  ) async {
    final dio = _createDio(serverUrl);
    await dio.delete(
      '/api/auth/passkeys/$credentialId',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
  }
}

/// Response from login/register/refresh/connect endpoints.
class AuthResponse {
  final String accessToken;
  final String refreshToken;
  final UserProfile user;
  final String? deviceId;

  const AuthResponse({
    required this.accessToken,
    required this.refreshToken,
    required this.user,
    this.deviceId,
  });

  factory AuthResponse.fromJson(Map<String, dynamic> json) => AuthResponse(
        accessToken: json['access_token'] as String,
        refreshToken: json['refresh_token'] as String,
        user: UserProfile.fromJson(json['user'] as Map<String, dynamic>),
        deviceId: json['device_id'] as String?,
      );
}

/// Response from connect token generation.
class ConnectTokenResponse {
  final String link;
  final String expiresAt;

  const ConnectTokenResponse({required this.link, required this.expiresAt});

  factory ConnectTokenResponse.fromJson(Map<String, dynamic> json) =>
      ConnectTokenResponse(
        link: json['link'] as String,
        expiresAt: json['expires_at'] as String,
      );
}

/// Device info returned by the admin devices endpoint.
class DeviceInfo {
  final String id;
  final int userId;
  final String username;
  final String deviceName;
  final String createdAt;
  final String lastSeenAt;

  const DeviceInfo({
    required this.id,
    required this.userId,
    required this.username,
    required this.deviceName,
    required this.createdAt,
    required this.lastSeenAt,
  });

  factory DeviceInfo.fromJson(Map<String, dynamic> json) => DeviceInfo(
        id: json['id'] as String,
        userId: json['user_id'] as int,
        username: json['username'] as String,
        deviceName: json['device_name'] as String,
        createdAt: json['created_at'] as String,
        lastSeenAt: json['last_seen_at'] as String,
      );
}

/// Enriched user account returned by the admin users endpoint.
class UserSummary {
  final int id;
  final String username;
  final String role;
  final List<String> permissions;
  final String createdAt;
  final int deviceCount;
  final bool hasPassword;
  final bool passwordEnabled;
  final bool passkeyEnabled;
  final bool hasPendingInvite;

  /// The email this user shared for their Plex server invite ('' = none yet),
  /// and when Cantinarr last sent their invite (null = never).
  final String plexEmail;
  final String? plexInvitedAt;

  const UserSummary({
    required this.id,
    required this.username,
    required this.role,
    required this.permissions,
    required this.createdAt,
    required this.deviceCount,
    required this.hasPassword,
    required this.passwordEnabled,
    required this.passkeyEnabled,
    required this.hasPendingInvite,
    this.plexEmail = '',
    this.plexInvitedAt,
  });

  bool get isAdmin => role == 'admin';

  factory UserSummary.fromJson(Map<String, dynamic> json) => UserSummary(
        id: json['id'] as int,
        username: json['username'] as String,
        role: json['role'] as String,
        permissions: (json['permissions'] as List<dynamic>?)
                ?.map((p) => p as String)
                .toList() ??
            const [],
        createdAt: json['created_at'] as String? ?? '',
        deviceCount: json['device_count'] as int? ?? 0,
        hasPassword: json['has_password'] as bool? ?? false,
        passwordEnabled: json['password_enabled'] as bool? ?? false,
        passkeyEnabled: json['passkey_enabled'] as bool? ?? false,
        hasPendingInvite: json['has_pending_invite'] as bool? ?? false,
        plexEmail: json['plex_email'] as String? ?? '',
        plexInvitedAt: json['plex_invited_at'] as String?,
      );
}

/// Server-provided configuration returned after authentication.
class ServerConfig {
  final String serverName;
  final AvailableServices services;
  final List<ServiceInstance> instances;

  /// Whether the AI-remediation feature is enabled server-side.
  final bool issuesEnabled;

  /// Whether users may see the "Report a problem" affordance.
  final bool allowReporting;

  const ServerConfig({
    required this.serverName,
    required this.services,
    this.instances = const [],
    this.issuesEnabled = false,
    this.allowReporting = false,
  });

  factory ServerConfig.fromJson(Map<String, dynamic> json) {
    final instancesList = (json['instances'] as List<dynamic>?)
            ?.map((i) => ServiceInstance.fromJson(i as Map<String, dynamic>))
            .toList() ??
        [];

    return ServerConfig(
      serverName: json['server_name'] as String? ?? 'Cantinarr',
      services: AvailableServices.fromJson(
          json['services'] as Map<String, dynamic>? ?? {}),
      instances: instancesList,
      issuesEnabled: json['issues_enabled'] as bool? ?? false,
      allowReporting: json['allow_reporting'] as bool? ?? false,
    );
  }
}

/// Response from begin passkey registration.
class BeginRegistrationResponse {
  final Map<String, dynamic> options;
  final String sessionId;

  const BeginRegistrationResponse({
    required this.options,
    required this.sessionId,
  });

  factory BeginRegistrationResponse.fromJson(Map<String, dynamic> json) =>
      BeginRegistrationResponse(
        options: json['options'] as Map<String, dynamic>,
        sessionId: json['session_id'] as String,
      );
}

/// Response from begin passkey login.
class BeginLoginResponse {
  final Map<String, dynamic> options;
  final String sessionId;

  const BeginLoginResponse({
    required this.options,
    required this.sessionId,
  });

  factory BeginLoginResponse.fromJson(Map<String, dynamic> json) =>
      BeginLoginResponse(
        options: json['options'] as Map<String, dynamic>,
        sessionId: json['session_id'] as String,
      );
}

/// Response from passkey setup-link generation.
class PasskeySetupLinkResponse {
  final String link;
  final String expiresAt;

  const PasskeySetupLinkResponse({
    required this.link,
    required this.expiresAt,
  });

  factory PasskeySetupLinkResponse.fromJson(Map<String, dynamic> json) =>
      PasskeySetupLinkResponse(
        link: json['link'] as String,
        expiresAt: json['expires_at'] as String,
      );
}

/// Passkey info returned by list passkeys endpoint.
class PasskeyInfoResponse {
  final String id;
  final String name;
  final String createdAt;
  final String? lastUsedAt;

  const PasskeyInfoResponse({
    required this.id,
    required this.name,
    required this.createdAt,
    this.lastUsedAt,
  });

  factory PasskeyInfoResponse.fromJson(Map<String, dynamic> json) =>
      PasskeyInfoResponse(
        id: json['id'] as String,
        name: json['name'] as String,
        createdAt: json['created_at'] as String,
        lastUsedAt: json['last_used_at'] as String?,
      );
}
