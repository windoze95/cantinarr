import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';

class RemoteAccessSecuritySettings {
  final bool requireHTTPSOutsideLocalNetwork;
  final List<String> trustedProxyCIDRs;
  final bool trustCGNATAsLocal;
  final bool requireHTTPSManaged;
  final bool trustedProxyCIDRsManaged;
  final bool trustCGNATManaged;
  final String connectionSecurity;
  final bool httpsRequired;
  final bool wouldCurrentConnectionBeAllowed;

  const RemoteAccessSecuritySettings({
    required this.requireHTTPSOutsideLocalNetwork,
    required this.trustedProxyCIDRs,
    required this.trustCGNATAsLocal,
    required this.requireHTTPSManaged,
    required this.trustedProxyCIDRsManaged,
    required this.trustCGNATManaged,
    required this.connectionSecurity,
    required this.httpsRequired,
    required this.wouldCurrentConnectionBeAllowed,
  });

  factory RemoteAccessSecuritySettings.fromJson(Map<String, dynamic> json) {
    final managed = json['managed_by_environment'];
    final managedMap =
        managed is Map ? Map<String, dynamic>.from(managed) : const <String, dynamic>{};
    return RemoteAccessSecuritySettings(
      requireHTTPSOutsideLocalNetwork:
          json['require_https_outside_local_network'] as bool? ?? false,
      trustedProxyCIDRs: (json['trusted_proxy_cidrs'] as List<dynamic>?)
              ?.whereType<String>()
              .toList() ??
          const [],
      trustCGNATAsLocal: json['trust_cgnat_as_local'] as bool? ?? false,
      requireHTTPSManaged:
          managedMap['require_https_outside_local_network'] as bool? ?? false,
      trustedProxyCIDRsManaged:
          managedMap['trusted_proxy_cidrs'] as bool? ?? false,
      trustCGNATManaged:
          managedMap['trust_cgnat_as_local'] as bool? ?? false,
      connectionSecurity: json['connection_security'] as String? ?? 'unknown_http',
      httpsRequired: json['https_required'] as bool? ?? false,
      wouldCurrentConnectionBeAllowed:
          json['would_current_connection_be_allowed'] as bool? ?? true,
    );
  }
}

class RemoteAccessSecurityService {
  final Dio _dio;

  RemoteAccessSecurityService({required Dio backendDio}) : _dio = backendDio;

  Future<RemoteAccessSecuritySettings> fetch() async {
    final response = await _dio.get('/api/admin/remote-access-security');
    return RemoteAccessSecuritySettings.fromJson(
      response.data as Map<String, dynamic>,
    );
  }

  Future<RemoteAccessSecuritySettings> update({
    required bool requireHTTPSOutsideLocalNetwork,
    required List<String> trustedProxyCIDRs,
    required bool trustCGNATAsLocal,
  }) async {
    final response = await _dio.put(
      '/api/admin/remote-access-security',
      data: {
        'require_https_outside_local_network':
            requireHTTPSOutsideLocalNetwork,
        'trusted_proxy_cidrs': trustedProxyCIDRs,
        'trust_cgnat_as_local': trustCGNATAsLocal,
      },
    );
    return RemoteAccessSecuritySettings.fromJson(
      response.data as Map<String, dynamic>,
    );
  }
}

final remoteAccessSecurityServiceProvider =
    Provider<RemoteAccessSecurityService>(
  (ref) => RemoteAccessSecurityService(
    backendDio: ref.watch(backendClientProvider),
  ),
);
