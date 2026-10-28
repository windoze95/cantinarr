import 'package:dio/dio.dart';
import '../../settings/data/instance_api_service.dart';

class MediaScanStatus {
  final String state;
  final DateTime? lastSuccessAt;
  final DateTime? lastAttemptAt;
  final int itemCount;
  final String error;
  const MediaScanStatus(
      {required this.state,
      this.lastSuccessAt,
      this.lastAttemptAt,
      this.itemCount = 0,
      this.error = ''});
  factory MediaScanStatus.fromJson(Map<String, dynamic> json) =>
      MediaScanStatus(
        state: json['state'] as String? ?? 'unknown',
        lastSuccessAt:
            DateTime.tryParse(json['last_success_at']?.toString() ?? ''),
        lastAttemptAt:
            DateTime.tryParse(json['last_attempt_at']?.toString() ?? ''),
        itemCount: (json['item_count'] as num?)?.toInt() ?? 0,
        error: json['error'] as String? ?? '',
      );
  bool get running =>
      state == 'running' || state == 'scanning' || state == 'queued';
}

class AvailabilityHouseholdServer {
  final String instanceId, name, serviceType, error;
  final bool enabled, linked;
  final List<String> libraryIds;
  final List<MediaServerLibrary> libraries;
  const AvailabilityHouseholdServer(
      {required this.instanceId,
      required this.name,
      required this.serviceType,
      required this.enabled,
      required this.linked,
      this.error = '',
      this.libraryIds = const [],
      this.libraries = const []});
  bool get eligible =>
      enabled && !linked && error.isEmpty && libraryIds.isNotEmpty;
  factory AvailabilityHouseholdServer.fromJson(Map<String, dynamic> json) =>
      AvailabilityHouseholdServer(
          instanceId: json['instance_id'] as String? ?? '',
          name: json['name'] as String? ?? '',
          serviceType: json['service_type'] as String? ?? '',
          error: json['error'] as String? ?? '',
          enabled: json['enabled'] == true,
          linked: json['linked'] == true,
          libraryIds: (json['library_ids'] as List? ?? const [])
              .map((id) => id.toString())
              .toList(),
          libraries: [
            for (final row in (json['libraries'] as List?) ?? const [])
              MediaServerLibrary.fromJson(Map<String, dynamic>.from(row as Map))
          ]);
}

class AvailabilityHouseholds {
  final Map<String, List<String>> associations;
  final List<AvailabilityHouseholdServer> servers;
  const AvailabilityHouseholds(
      {this.associations = const {}, this.servers = const []});
  factory AvailabilityHouseholds.fromJson(Map<String, dynamic> json) =>
      AvailabilityHouseholds(associations: {
        for (final row in (json['associations'] as List?) ?? const [])
          row['instance_id'].toString():
              (row['library_ids'] as List? ?? const [])
                  .map((id) => id.toString())
                  .toList()
      }, servers: [
        for (final row in (json['servers'] as List?) ?? const [])
          AvailabilityHouseholdServer.fromJson(
              Map<String, dynamic>.from(row as Map))
      ]);
}

class MediaAvailabilityService {
  final Dio dio;
  const MediaAvailabilityService(this.dio);
  String _base(String id) =>
      '/api/admin/media-availability/${Uri.encodeComponent(id)}';
  Future<List<MediaServerLibrary>> libraries(String id) async {
    final data = (await dio.get('${_base(id)}/libraries')).data as Map;
    return [
      for (final row in (data['libraries'] as List?) ?? const [])
        MediaServerLibrary.fromJson(Map<String, dynamic>.from(row as Map))
    ];
  }

  Future<MediaScanStatus> status(String id) async =>
      MediaScanStatus.fromJson(Map<String, dynamic>.from(
          (await dio.get('${_base(id)}/status')).data as Map));
  Future<MediaScanStatus> scan(String id) async =>
      MediaScanStatus.fromJson(Map<String, dynamic>.from(
          (await dio.post('${_base(id)}/scan')).data as Map));
  Future<AvailabilityHouseholds> households(int userId) async =>
      AvailabilityHouseholds.fromJson(Map<String, dynamic>.from(
          (await dio.get('/api/admin/users/$userId/media-availability')).data
              as Map));
  Future<void> saveHouseholds(
      int userId, Map<String, List<String>> associations) async {
    await dio.put('/api/admin/users/$userId/media-availability', data: {
      'associations': [
        for (final entry in associations.entries)
          {'instance_id': entry.key, 'library_ids': entry.value}
      ]
    });
  }
}
