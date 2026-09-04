import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';

/// One library album that recently gained files.
class RecentAlbum {
  final int albumId;
  final String foreignAlbumId;
  final String title;
  final String artist;
  final String cover;
  final DateTime? importedAt;

  const RecentAlbum({
    required this.albumId,
    required this.foreignAlbumId,
    required this.title,
    required this.artist,
    required this.cover,
    this.importedAt,
  });

  factory RecentAlbum.fromJson(Map<String, dynamic> json) => RecentAlbum(
        albumId: (json['album_id'] as num?)?.toInt() ?? 0,
        foreignAlbumId: json['foreign_album_id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        artist: json['artist'] as String? ?? '',
        cover: json['cover'] as String? ?? '',
        importedAt: DateTime.tryParse(json['imported_at'] as String? ?? ''),
      );
}

/// Fetches the newest music imports for a Lidarr instance, so the Music tab
/// can show what recently landed.
class RecentAlbumsService {
  final Dio _dio;

  RecentAlbumsService({required Dio backendDio}) : _dio = backendDio;

  Future<List<RecentAlbum>> fetchRecent(
      {String? instanceId, int limit = 20}) async {
    final resp = await _dio.get(
      '/api/requests/music-recent',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
        'limit': limit,
      },
    );
    final data = resp.data;
    final items = data is Map ? data['items'] : null;
    if (items is! List) {
      throw const FormatException('Recent albums response is invalid');
    }
    return items
        .whereType<Map<String, dynamic>>()
        .map(RecentAlbum.fromJson)
        .toList();
  }
}

/// Recently added albums for one Lidarr instance. Keyed on the instance id so
/// switching libraries can never show the previous library's albums.
final recentAlbumsForInstanceProvider = FutureProvider.autoDispose
    .family<List<RecentAlbum>, String?>((ref, instanceId) async {
  final dio = ref.read(backendClientProvider);
  return RecentAlbumsService(backendDio: dio)
      .fetchRecent(instanceId: instanceId);
});

/// The row follows the drawer's active Lidarr instance, like search does.
final recentAlbumsProvider =
    FutureProvider.autoDispose<List<RecentAlbum>>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
  return ref.watch(recentAlbumsForInstanceProvider(instanceId).future);
});
