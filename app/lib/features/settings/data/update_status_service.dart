import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

/// The latest-release comparison the server computed for the running version.
class UpdateInfo {
  final String current;
  final String latest;
  final bool available;
  final String url;

  const UpdateInfo({
    required this.current,
    required this.latest,
    required this.available,
    required this.url,
  });

  factory UpdateInfo.fromJson(Map<String, dynamic> json) => UpdateInfo(
        current: json['current'] as String? ?? '',
        latest: json['latest'] as String? ?? '',
        available: json['available'] as bool? ?? false,
        url: json['url'] as String? ?? '',
      );
}

/// Admin-only update state: the release comparison plus the optional
/// management-portal URL the banner's action button links to.
class UpdateStatus {
  final UpdateInfo update;
  final String managementUrl;

  const UpdateStatus({required this.update, required this.managementUrl});

  factory UpdateStatus.fromJson(Map<String, dynamic> json) => UpdateStatus(
        update: UpdateInfo.fromJson(
            json['update'] as Map<String, dynamic>? ?? const {}),
        managementUrl: json['management_url'] as String? ?? '',
      );
}

class UpdateStatusService {
  final Dio _dio;

  UpdateStatusService({required Dio backendDio}) : _dio = backendDio;

  Future<UpdateStatus> fetch() async {
    final resp = await _dio.get('/api/admin/update-status');
    return UpdateStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Sets the management-portal URL (empty clears it) and returns the updated
  /// status. Throws on a non-2xx response so callers can surface the error.
  Future<UpdateStatus> setManagementUrl(String url) async {
    final resp = await _dio.put(
      '/api/admin/update-status',
      data: {'management_url': url},
    );
    return UpdateStatus.fromJson(resp.data as Map<String, dynamic>);
  }
}

final updateStatusServiceProvider = Provider<UpdateStatusService>(
  (ref) => UpdateStatusService(backendDio: ref.watch(backendClientProvider)),
);
