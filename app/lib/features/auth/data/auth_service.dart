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
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/setup', data: {
      'username': username,
      'password': password,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Log in with username and password.
  Future<AuthResponse> login(
    String serverUrl,
    String username,
    String password,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/login', data: {
      'username': username,
      'password': password,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Register a new account using an invite code.
  Future<AuthResponse> register(
    String serverUrl,
    String username,
    String password,
    String inviteCode,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/register', data: {
      'username': username,
      'password': password,
      'invite_code': inviteCode,
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
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post('/api/auth/connect', data: {
      'token': token,
      'device_name': deviceName,
    });
    return AuthResponse.fromJson(resp.data as Map<String, dynamic>);
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
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/auth/passkey/login/finish',
      queryParameters: {'session_id': sessionId},
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

/// Server-provided configuration returned after authentication.
class ServerConfig {
  final String serverName;
  final AvailableServices services;
  final List<ServiceInstance> instances;

  const ServerConfig({
    required this.serverName,
    required this.services,
    this.instances = const [],
  });

  factory ServerConfig.fromJson(Map<String, dynamic> json) {
    final instancesList = (json['instances'] as List<dynamic>?)
            ?.map((i) =>
                ServiceInstance.fromJson(i as Map<String, dynamic>))
            .toList() ??
        [];

    return ServerConfig(
      serverName: json['server_name'] as String? ?? 'Cantinarr',
      services: AvailableServices.fromJson(
          json['services'] as Map<String, dynamic>? ?? {}),
      instances: instancesList,
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
