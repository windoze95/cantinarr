import 'package:dio/dio.dart';
import '../../../core/config/app_config.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';

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
