import 'package:dio/dio.dart';
import '../../../core/network/long_request_options.dart';

/// Progress for one Audiobookshelf book library during the separation bridge.
class ABSSeparationLibrary {
  final String libraryId;
  final String state;
  final int inventoryCount;
  final int sharedCount;
  final int requestedCount;
  final int unresolvedCount;
  final int pendingItems;
  final int failedItems;
  final String lastError;

  const ABSSeparationLibrary({
    required this.libraryId,
    required this.state,
    required this.inventoryCount,
    required this.sharedCount,
    required this.requestedCount,
    required this.unresolvedCount,
    required this.pendingItems,
    required this.failedItems,
    required this.lastError,
  });

  factory ABSSeparationLibrary.fromJson(Map<String, dynamic> json) =>
      ABSSeparationLibrary(
        libraryId: json['library_id'] as String? ?? '',
        state: json['state'] as String? ?? '',
        inventoryCount: json['inventory_count'] as int? ?? 0,
        sharedCount: json['shared_count'] as int? ?? 0,
        requestedCount: json['requested_count'] as int? ?? 0,
        unresolvedCount: json['unresolved_count'] as int? ?? 0,
        pendingItems: json['pending_items'] as int? ?? 0,
        failedItems: json['failed_items'] as int? ?? 0,
        lastError: json['last_error'] as String? ?? '',
      );
}

/// Admin-only separation status. The plan hash is opaque and must be echoed
/// unchanged when applying the exact preview the administrator reviewed.
class ABSSeparationStatus {
  final String instanceId;
  final String state;
  final String planHash;
  final bool canApply;
  final int unmatchedRequestCount;
  final List<ABSSeparationLibrary> libraries;

  const ABSSeparationStatus({
    required this.instanceId,
    required this.state,
    required this.planHash,
    required this.canApply,
    required this.unmatchedRequestCount,
    required this.libraries,
  });

  factory ABSSeparationStatus.fromJson(Map<String, dynamic> json) {
    final unmatched = json['unmatched_requests'];
    final libraries = json['libraries'];
    return ABSSeparationStatus(
      instanceId: json['instance_id'] as String? ?? '',
      state: json['state'] as String? ?? '',
      planHash: json['plan_hash'] as String? ?? '',
      canApply: json['can_apply'] as bool? ?? false,
      unmatchedRequestCount: unmatched is List ? unmatched.length : 0,
      libraries: libraries is List
          ? libraries
                .whereType<Map>()
                .map(
                  (value) => ABSSeparationLibrary.fromJson(
                    Map<String, dynamic>.from(value),
                  ),
                )
                .toList(growable: false)
          : const [],
    );
  }
}

class ABSLibrarySeparationService {
  final Dio _dio;

  ABSLibrarySeparationService({required Dio backendDio}) : _dio = backendDio;

  Future<ABSSeparationStatus> status(String instanceId) async {
    final response = await _dio.get<Map<String, dynamic>>(
      '/api/admin/instances/$instanceId/library-separation',
    );
    return ABSSeparationStatus.fromJson(response.data ?? const {});
  }

  Future<ABSSeparationStatus> preview(
    String instanceId,
    List<String> libraryIds,
  ) async {
    final response = await _dio.post<Map<String, dynamic>>(
      '/api/admin/instances/$instanceId/library-separation/preview',
      data: {'library_ids': libraryIds},
      options: longRequestOptions(
        timeout: const Duration(minutes: 2, seconds: 30),
      ),
    );
    return ABSSeparationStatus.fromJson(response.data ?? const {});
  }

  Future<ABSSeparationStatus> apply(
    String instanceId, {
    required String planHash,
    required bool confirmUnresolved,
  }) async {
    final response = await _dio.post<Map<String, dynamic>>(
      '/api/admin/instances/$instanceId/library-separation/apply',
      data: {'plan_hash': planHash, 'confirm_unresolved': confirmUnresolved},
      options: longRequestOptions(timeout: const Duration(minutes: 10)),
    );
    return ABSSeparationStatus.fromJson(response.data ?? const {});
  }
}
