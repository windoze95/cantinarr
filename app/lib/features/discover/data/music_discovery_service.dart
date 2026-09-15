import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/widgets/cached_image.dart';
import '../../auth/logic/auth_provider.dart';
import '../logic/music_browse_query.dart';
import '../logic/discovery_access.dart';
import 'music_models.dart';

const musicUpdateMessage =
    'Music discovery requires a server update. You can still browse your library and search.';

class MusicDiscoveryUnsupported implements Exception {
  const MusicDiscoveryUnsupported();
}

class MusicDiscoveryService {
  final Dio dio;
  MusicDiscoveryService(this.dio);

  Future<Map<String, dynamic>> _get(String path, Map<String, dynamic> params,
      {CancelToken? cancelToken}) async {
    try {
      final response = await dio.get(path,
          queryParameters: params,
          cancelToken: cancelToken,
          options: Options(
              sendTimeout: const Duration(seconds: 10),
              receiveTimeout: const Duration(seconds: 10)));
      if (response.data is String &&
          (response.data as String).trimLeft().startsWith('<')) {
        throw const MusicDiscoveryUnsupported();
      }
      return response.data as Map<String, dynamic>;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) {
        throw const MusicDiscoveryUnsupported();
      }
      rethrow;
    }
  }

  Future<MusicPage> feed(MusicBrowseQuery query, int page) async =>
      MusicPage.fromJson(await _get('/api/discover/music/${query.feed}',
          {...query.parameters, 'page': page}));

  Future<MusicPage> search(String query, String? instanceId,
          {int page = 1,
          bool includeSingles = false,
          CancelToken? cancelToken}) async =>
      MusicPage.fromJson(await _get(
          '/api/discover/music/search',
          {
            'query': query,
            'page': page,
            if (includeSingles) 'include_singles': true,
            if (instanceId != null) 'instance_id': instanceId
          },
          cancelToken: cancelToken));

  Future<MusicArtistPage> searchArtists(String query, String? instanceId,
          {int page = 1, CancelToken? cancelToken}) async =>
      MusicArtistPage.fromJson(await _get(
          '/api/discover/music/artists',
          {
            'query': query,
            'page': page,
            if (instanceId != null) 'instance_id': instanceId
          },
          cancelToken: cancelToken));
  Future<MusicArtist> artist(String id, String? instanceId,
          {CancelToken? cancelToken}) async =>
      MusicArtist.fromJson(await _get(
          '/api/media/music/artists/${Uri.encodeComponent(id)}',
          {if (instanceId != null) 'instance_id': instanceId},
          cancelToken: cancelToken));
  Future<MusicPage> artistAlbums(String id, String? instanceId,
          {int page = 1, CancelToken? cancelToken}) async =>
      MusicPage.fromJson(await _get(
          '/api/media/music/artists/${Uri.encodeComponent(id)}/albums',
          {'page': page, if (instanceId != null) 'instance_id': instanceId},
          cancelToken: cancelToken));

  Future<List<MusicGenre>> genres(String? instanceId) async {
    final data = await _get('/api/genres/music',
        {if (instanceId != null) 'instance_id': instanceId});
    return (data['genres'] as List)
        .map((g) => MusicGenre.fromJson(g as Map<String, dynamic>))
        .toList();
  }

  Future<MusicAlbum> album(String mbid, String? instanceId,
      {CancelToken? cancelToken}) async {
    final album = MusicAlbum.fromJson(await _get(
        '/api/media/music/${Uri.encodeComponent(mbid)}',
        {if (instanceId != null) 'instance_id': instanceId},
        cancelToken: cancelToken));
    // The server verifies exact provider redirects before returning a canonical ID.
    return album;
  }
}

final musicDiscoveryServiceProvider = Provider<MusicDiscoveryService>(
    (ref) => MusicDiscoveryService(ref.watch(backendClientProvider)));

final musicGenresProvider =
    FutureProvider.autoDispose.family<List<MusicGenre>, String?>(
  (ref, id) {
    ref.watch(catalogDiscoveryScopeProvider);
    if (!ref.watch(discoveryAccessProvider).canBrowse('lidarr', id)) {
      throw StateError('Music is not available for this account.');
    }
    return ref.watch(musicDiscoveryServiceProvider).genres(id);
  },
);

/// The path is constructed here from identity, never taken as an arbitrary
/// image URL. Covers always use the authenticated Cantinarr relay on web/native.
ImageSource? musicArtworkSource(
        WidgetRef ref, MusicAlbum album, String? instanceId) =>
    musicArtworkSourceFor(
        ref.watch(authProvider).valueOrNull?.connection, album, instanceId);

ImageSource? musicArtworkSourceFor(
    BackendConnection? connection, MusicAlbum album, String? instanceId) {
  if (album.artwork == null || album.artwork!.isEmpty) return null;
  if (connection == null) return null;
  final base = connection.serverUrl.replaceFirst(RegExp(r'/$'), '');
  final path =
      '/api/discover/music/artwork/${Uri.encodeComponent(album.foreignId)}';
  final relative = Uri(path: path, queryParameters: {
    if (instanceId != null) 'instance_id': instanceId,
  });
  return (
    url: '$base$relative',
    headers: {'Authorization': 'Bearer ${connection.accessToken}'},
  );
}
