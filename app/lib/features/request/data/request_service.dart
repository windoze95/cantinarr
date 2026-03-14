import 'package:dio/dio.dart';
import '../../discover/data/tmdb_models.dart';

/// Status of a media request from the user's perspective.
enum RequestStatus {
  /// Not on the server, can be requested.
  unavailable('Not Available', 'Request'),

  /// Request has been submitted, waiting for processing.
  requested('Requested', 'Requested'),

  /// Actively downloading.
  downloading('Downloading', 'Downloading'),

  /// Fully available on the media server.
  available('Available on Plex', 'Watch Now'),

  /// Partially available (some seasons/episodes).
  partial('Partially Available', 'Request More');

  const RequestStatus(this.label, this.buttonLabel);
  final String label;
  final String buttonLabel;
}

/// Routes media requests through the Cantinarr backend.
///
/// The backend handles all TMDB-to-TVDB bridging and Radarr/Sonarr
/// communication transparently.
class RequestService {
  final Dio _backendDio;

  RequestService({required Dio backendDio}) : _backendDio = backendDio;

  /// Check the current status of a media item.
  Future<RequestStatus> checkStatus(int tmdbId, MediaType mediaType) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/$tmdbId/status',
        queryParameters: {'media_type': mediaType.name},
      );
      final data = resp.data as Map<String, dynamic>;
      final statusName = data['status'] as String? ?? 'unavailable';
      return RequestStatus.values.firstWhere(
        (s) => s.name == statusName,
        orElse: () => RequestStatus.unavailable,
      );
    } catch (_) {
      return RequestStatus.unavailable;
    }
  }

  /// Submit a request for a media item.
  Future<bool> request({
    required int tmdbId,
    required MediaType mediaType,
  }) async {
    try {
      final resp = await _backendDio.post('/api/requests', data: {
        'tmdb_id': tmdbId,
        'media_type': mediaType.name,
      });
      return resp.statusCode == 200 || resp.statusCode == 201;
    } catch (_) {
      return false;
    }
  }
}
