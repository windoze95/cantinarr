import 'dart:convert';

import 'package:dio/dio.dart';
import '../../../core/network/long_request_options.dart';
import 'lidarr_models.dart';

/// Coerces a Dio response body into a JSON list. Servarr lookup endpoints
/// don't reliably send an `application/json` content-type, so Dio can hand
/// back the raw String instead of a decoded List — decode it here rather than
/// blindly casting.
List<dynamic> _jsonList(dynamic data) {
  if (data is List) return data;
  if (data is String && data.trim().isNotEmpty) {
    final decoded = jsonDecode(data);
    if (decoded is List) return decoded;
  }
  return const [];
}

/// Networking layer for Lidarr (the Servarr music service), proxied through
/// the Cantinarr backend. Note the Lidarr API is v1 (not v3).
class LidarrApiService {
  final Dio _dio;
  final String _instanceId;

  LidarrApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  /// Returns the base path prefix for API calls.
  String get _basePath => '/api/instances/$_instanceId/api/v1';

  Future<LidarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('$_basePath/system/status');
    return LidarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches the entire artist library in one unpaginated response — slow for
  /// large libraries.
  Future<List<LidarrArtist>> getArtists() async {
    final resp =
        await _dio.get('$_basePath/artist', options: longRequestOptions());
    return (resp.data as List<dynamic>)
        .map((a) => LidarrArtist.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  Future<LidarrArtist> getArtistById(int id) async {
    final resp = await _dio.get('$_basePath/artist/$id');
    return LidarrArtist.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<LidarrArtist>> lookupArtist(String term) async {
    final resp = await _dio
        .get('$_basePath/artist/lookup', queryParameters: {'term': term});
    return _jsonList(resp.data)
        .map((a) => LidarrArtist.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  /// Lists albums, optionally narrowed to one artist. A bare call returns the
  /// entire album library in one unpaginated response — slow for large
  /// libraries.
  Future<List<LidarrAlbum>> getAlbums({int? artistId}) async {
    final resp = await _dio.get(
      '$_basePath/album',
      queryParameters: {
        if (artistId != null) 'artistId': artistId,
      },
      options: longRequestOptions(),
    );
    return (resp.data as List<dynamic>)
        .map((a) => LidarrAlbum.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  Future<LidarrAlbum> getAlbumById(int id) async {
    final resp = await _dio.get('$_basePath/album/$id');
    return LidarrAlbum.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<LidarrAlbum>> lookupAlbum(String term) async {
    final resp = await _dio
        .get('$_basePath/album/lookup', queryParameters: {'term': term});
    return _jsonList(resp.data)
        .map((a) => LidarrAlbum.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  Future<List<LidarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('$_basePath/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) => LidarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<LidarrMetadataProfile>> getMetadataProfiles() async {
    final resp = await _dio.get('$_basePath/metadataprofile');
    return (resp.data as List<dynamic>)
        .map((p) => LidarrMetadataProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  /// Toggles monitoring for a set of albums in one call (the endpoint is
  /// PUT-only; a POST answers 405). Admin only (proxy requires
  /// instances:manage).
  Future<void> setAlbumMonitored(List<int> albumIds, bool monitored) async {
    await _dio.put('$_basePath/album/monitor',
        data: {'albumIds': albumIds, 'monitored': monitored});
  }

  /// Triggers an indexer search for every monitored album of an artist.
  Future<void> searchArtist(int artistId) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'ArtistSearch',
      'artistId': artistId,
    });
  }

  /// Triggers an automatic indexer search for the given albums.
  Future<void> searchAlbums(List<int> albumIds) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'AlbumSearch',
      'albumIds': albumIds,
    });
  }

  /// Fetches the queue with full artist + album details, typed.
  Future<List<LidarrQueueItem>> getQueueDetailed({
    int page = 1,
    int pageSize = 100,
  }) async {
    final resp = await _dio.get('$_basePath/queue', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'includeArtist': true,
      'includeAlbum': true,
    });
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>? ?? [];
    return records
        .map((r) => LidarrQueueItem.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// Removes a queue item, optionally from the download client / blocklist.
  /// [changeCategory] hands the download to the post-import category instead
  /// of deleting it (e.g. for Unpackerr); [skipRedownload] suppresses the
  /// automatic re-grab on a blocklist removal.
  Future<void> deleteQueueItem(
    int id, {
    bool removeFromClient = true,
    bool blocklist = false,
    bool skipRedownload = false,
    bool changeCategory = false,
  }) async {
    await _dio.delete('$_basePath/queue/$id', queryParameters: {
      'removeFromClient': removeFromClient,
      'blocklist': blocklist,
      'skipRedownload': skipRedownload,
      'changeCategory': changeCategory,
    });
  }

  /// Fetches a page of history events, newest first.
  Future<LidarrHistoryPage> getHistory({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/history', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'date',
      'sortDirection': 'descending',
    });
    return LidarrHistoryPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored albums that are missing files, newest
  /// release date first. Records include artist context.
  Future<LidarrWantedPage> getWantedMissing({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/missing', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'releaseDate',
      'sortDirection': 'descending',
      'monitored': true,
      'includeArtist': true,
    });
    return LidarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored albums whose files are below the quality
  /// profile cutoff, newest release date first.
  Future<LidarrWantedPage> getWantedCutoff({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/cutoff', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'releaseDate',
      'sortDirection': 'descending',
      'monitored': true,
      'includeArtist': true,
    });
    return LidarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches the album calendar for a date window, with artist context so
  /// rows can be labelled without a fan-out. Album release dates are calendar
  /// dates (no meaningful time-of-day).
  Future<List<LidarrAlbum>> getCalendar({
    required String start,
    required String end,
  }) async {
    final resp = await _dio.get('$_basePath/calendar', queryParameters: {
      'start': start,
      'end': end,
      'unmonitored': 'false',
      'includeArtist': 'true',
    });
    return _jsonList(resp.data)
        .whereType<Map<String, dynamic>>()
        .map(LidarrAlbum.fromJson)
        .toList();
  }

  /// Lists the music files on disk for one album. `trackfile` is in the
  /// requester read allowlist for exactly this: album downloads need the
  /// file ids and reported paths.
  Future<List<LidarrTrackFile>> getTrackFiles({required int albumId}) async {
    final resp = await _dio.get('$_basePath/trackfile', queryParameters: {
      'albumId': albumId,
    });
    return _jsonList(resp.data)
        .whereType<Map<String, dynamic>>()
        .map(LidarrTrackFile.fromJson)
        .where((f) => f.albumId == albumId || f.albumId == 0)
        .toList();
  }

  /// Lists one album's tracks — the naming join for downloads (a track file
  /// carries only a path; the track knows its number and title).
  Future<List<LidarrTrack>> getTracks({required int albumId}) async {
    final resp = await _dio.get('$_basePath/track', queryParameters: {
      'albumId': albumId,
    });
    return _jsonList(resp.data)
        .whereType<Map<String, dynamic>>()
        .map(LidarrTrack.fromJson)
        .toList();
  }

  /// Nudges Lidarr to run its completed-download import pass now (clears
  /// items stuck "waiting to import").
  Future<void> processMonitoredDownloads() async {
    await _dio.post('$_basePath/command',
        data: {'name': 'ProcessMonitoredDownloads'});
  }

  /// Rescans an artist's files on disk. Lidarr has no per-artist rescan
  /// command; RescanFolders scoped by artistId is its equivalent.
  Future<void> rescanArtist(int artistId) async {
    await _dio.post('$_basePath/command',
        data: {'name': 'RescanFolders', 'artistId': artistId});
  }

  /// Interactive release search for one album. Queries every indexer live,
  /// so this is slow (up to a minute) by design.
  Future<List<LidarrRelease>> getReleases(int albumId) async {
    final resp = await _dio.get(
      '$_basePath/release',
      queryParameters: {'albumId': albumId},
      options: longRequestOptions(),
    );
    return _jsonList(resp.data)
        .whereType<Map<String, dynamic>>()
        .map(LidarrRelease.fromJson)
        .toList();
  }

  /// Sends a release from interactive search to the download client.
  Future<void> grabRelease(String guid, int indexerId) async {
    await _dio.post(
      '$_basePath/release',
      data: {'guid': guid, 'indexerId': indexerId},
      options: longRequestOptions(timeout: const Duration(seconds: 60)),
    );
  }

  // --- Import Doctor (admin; proxy requires instances:manage) ---

  /// Lists the importable files Lidarr found for a finished download, with
  /// any rejection reasons. Backs the manual-import recovery flow.
  Future<List<LidarrManualImportCandidate>> getManualImportCandidates(
    String downloadId,
  ) async {
    final resp = await _dio.get(
      '$_basePath/manualimport',
      queryParameters: {
        'downloadId': downloadId,
        'filterExistingFiles': false,
      },
      options: longRequestOptions(timeout: const Duration(seconds: 60)),
    );
    return (resp.data as List<dynamic>)
        .map((c) =>
            LidarrManualImportCandidate.fromJson(c as Map<String, dynamic>))
        .toList();
  }

  /// Imports the given candidate files. [importMode] must be lowercase
  /// (`move`/`copy`/`auto`); `copy` preserves seeding for torrents.
  Future<void> executeManualImport(
    List<Map<String, dynamic>> files, {
    String importMode = 'move',
  }) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'ManualImport',
      'importMode': importMode,
      'files': files,
    });
  }
}
