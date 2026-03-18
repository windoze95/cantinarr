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

  /// Generate an invite code (admin only).
  Future<String> generateInviteCode(
    String serverUrl,
    String accessToken,
  ) async {
    final dio = _createDio(serverUrl);
    final resp = await dio.post(
      '/api/admin/invite',
      options: Options(headers: {'Authorization': 'Bearer $accessToken'}),
    );
    return (resp.data as Map<String, dynamic>)['code'] as String;
  }
}

/// Response from login/register/refresh endpoints.
class AuthResponse {
  final String accessToken;
  final String refreshToken;
  final UserProfile user;

  const AuthResponse({
    required this.accessToken,
    required this.refreshToken,
    required this.user,
  });

  factory AuthResponse.fromJson(Map<String, dynamic> json) => AuthResponse(
        accessToken: json['access_token'] as String,
        refreshToken: json['refresh_token'] as String,
        user: UserProfile.fromJson(json['user'] as Map<String, dynamic>),
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
