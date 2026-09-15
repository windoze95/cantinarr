import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../request/data/album_ownership.dart';
import '../../discover/logic/discovery_access.dart';
import '../../../core/providers/library_refresh_provider.dart';

/// Fetches the backend's lean owned-albums digest — what the user's Lidarr
/// library already tracks, one entry per album — so the Music search can mark
/// results as owned and stop re-requesting an album the library has.
class MusicLibraryService {
  final Dio _dio;

  MusicLibraryService({required Dio backendDio}) : _dio = backendDio;

  Future<List<OwnedAlbum>> fetchOwnedAlbums(
      {String? instanceId, CancelToken? cancelToken}) async {
    final resp = await _dio.get(
      '/api/requests/music-library',
      cancelToken: cancelToken,
      options: Options(receiveTimeout: const Duration(seconds: 10)),
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    final data = resp.data;
    final titles = data is Map ? data['titles'] : null;
    if (titles is! List) {
      throw const FormatException('Music library response is invalid');
    }
    return titles
        .whereType<Map<String, dynamic>>()
        .map(OwnedAlbum.fromJson)
        .toList();
  }
}

/// The user's owned-album digest for the actively selected Lidarr instance.
/// Failures remain AsyncError so callers never confuse an unreachable library
/// with a genuinely empty one.
final ownedAlbumsForInstanceProvider = FutureProvider.autoDispose
    .family<List<OwnedAlbum>, String?>((ref, instanceId) async {
  ref.watch(catalogDiscoveryScopeProvider);
  ref.watch(libraryRefreshTickProvider);
  if (!ref.watch(discoveryAccessProvider).hasInstance('lidarr', instanceId)) {
    throw StateError('Music is not available for this account.');
  }
  final token = CancelToken();
  ref.onDispose(() => token.cancel());
  final dio = ref.read(backendClientProvider);
  return MusicLibraryService(backendDio: dio)
      .fetchOwnedAlbums(instanceId: instanceId, cancelToken: token);
});

/// Convenience projection for search, which always follows the drawer's active
/// Lidarr instance. Pinned detail routes use [ownedAlbumsForInstanceProvider]
/// directly so another library can never bleed ownership into their state.
final ownedAlbumsProvider =
    FutureProvider.autoDispose<List<OwnedAlbum>>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
  return ref.watch(ownedAlbumsForInstanceProvider(instanceId).future);
});
