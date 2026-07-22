import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import 'media_download_models.dart';

class MediaDownloadService {
  final Dio _dio;

  MediaDownloadService({required Dio backendDio}) : _dio = backendDio;

  Future<MediaDownloadTicket> createTicket({
    required String instanceId,
    required int fileId,
  }) async {
    if (instanceId.trim().isEmpty || fileId <= 0) {
      throw const MediaDownloadException(
        'This file is no longer available.',
      );
    }

    try {
      final response = await _dio.post(
        '/api/media-files/tickets',
        data: {
          'instance_id': instanceId,
          'file_id': fileId,
        },
      );
      final data = response.data;
      if (data is! Map<String, dynamic>) {
        throw const MediaDownloadException(
          'Could not prepare the download. Try again.',
        );
      }

      final filename = (data['filename'] as String? ?? '').trim();
      final sizeBytes = (data['size_bytes'] as num?)?.toInt();
      final expiresAt =
          DateTime.tryParse(data['expires_at'] as String? ?? '');
      if (filename.isEmpty ||
          sizeBytes == null ||
          sizeBytes < 0 ||
          expiresAt == null) {
        throw const MediaDownloadException(
          'Could not prepare the download. Try again.',
        );
      }

      return MediaDownloadTicket(
        url: _resolveDownloadUrl(data['url']),
        filename: filename,
        sizeBytes: sizeBytes,
        expiresAt: expiresAt,
      );
    } on MediaDownloadException {
      rethrow;
    } on DioException catch (error) {
      switch (error.response?.statusCode) {
        case 403:
          throw const MediaDownloadException(
            'You do not have access to this file.',
          );
        case 404:
        case 410:
          throw const MediaDownloadException(
            'This file is no longer available.',
          );
        default:
          throw const MediaDownloadException(
            'Could not prepare the download. Try again.',
          );
      }
    } catch (_) {
      throw const MediaDownloadException(
        'Could not prepare the download. Try again.',
      );
    }
  }

  Uri _resolveDownloadUrl(dynamic value) {
    final raw = value is String ? value.trim() : '';
    final base = Uri.tryParse(_dio.options.baseUrl);
    final candidate = Uri.tryParse(raw);
    if (raw.isEmpty ||
        base == null ||
        !_isHttpOrigin(base) ||
        candidate == null ||
        candidate.userInfo.isNotEmpty ||
        candidate.fragment.isNotEmpty) {
      throw const MediaDownloadException(
        'Could not prepare the download. Try again.',
      );
    }

    final resolved = base.resolveUri(candidate);
    if (!_sameOrigin(base, resolved)) {
      throw const MediaDownloadException(
        'Could not prepare the download. Try again.',
      );
    }
    return resolved;
  }

  bool _isHttpOrigin(Uri uri) =>
      (uri.scheme == 'http' || uri.scheme == 'https') &&
      uri.host.isNotEmpty &&
      uri.userInfo.isEmpty;

  bool _sameOrigin(Uri left, Uri right) =>
      _isHttpOrigin(right) &&
      left.scheme.toLowerCase() == right.scheme.toLowerCase() &&
      left.host.toLowerCase() == right.host.toLowerCase() &&
      _effectivePort(left) == _effectivePort(right);

  int _effectivePort(Uri uri) {
    if (uri.hasPort) return uri.port;
    return uri.scheme.toLowerCase() == 'https' ? 443 : 80;
  }
}

final mediaDownloadServiceProvider = Provider<MediaDownloadService>((ref) {
  return MediaDownloadService(backendDio: ref.watch(backendClientProvider));
});
