import 'package:dio/dio.dart';
import 'radarr_models.dart';

/// Networking layer for Radarr, proxied through the Cantinarr backend.
class RadarrApiService {
  final Dio _dio;
  final String _instanceId;

  RadarrApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  /// Returns the base path prefix for API calls.
  String get _basePath => '/api/instances/$_instanceId/api/v3';

  Future<RadarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('$_basePath/system/status');
    return RadarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<RadarrMovie>> getMovies({String? searchTerm}) async {
    final params = <String, dynamic>{};
    if (searchTerm != null && searchTerm.isNotEmpty) {
      params['searchTerm'] = searchTerm;
    }
    final resp = await _dio.get('$_basePath/movie', queryParameters: params);
    return (resp.data as List<dynamic>)
        .map((m) => RadarrMovie.fromJson(m as Map<String, dynamic>))
        .toList();
  }

  Future<RadarrMovie> getMovie(int id) async {
    final resp = await _dio.get('$_basePath/movie/$id');
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Alias for [getMovie] used by the movie detail screen for naming parity
  /// with the Sonarr drill-down (getSeriesById).
  Future<RadarrMovie> getMovieById(int id) => getMovie(id);

  Future<List<RadarrMovie>> lookupMovies(String term) async {
    final resp = await _dio
        .get('$_basePath/movie/lookup', queryParameters: {'term': term});
    return (resp.data as List<dynamic>)
        .map((m) => RadarrMovie.fromJson(m as Map<String, dynamic>))
        .toList();
  }

  Future<List<RadarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('$_basePath/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) => RadarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<RadarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('$_basePath/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => RadarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<RadarrMovie> addMovie(Map<String, dynamic> movieData) async {
    final resp = await _dio.post('$_basePath/movie', data: movieData);
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<RadarrMovie> updateMovie(
      int id, Map<String, dynamic> movieData) async {
    final resp = await _dio.put('$_basePath/movie/$id', data: movieData);
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteMovie(int id,
      {bool deleteFiles = false, bool addExclusion = false}) async {
    await _dio.delete('$_basePath/movie/$id', queryParameters: {
      'deleteFiles': deleteFiles,
      'addImportListExclusion': addExclusion,
    });
  }

  Future<void> searchMovie(int movieId) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'MoviesSearch',
      'movieIds': [movieId],
    });
  }

  Future<List<Map<String, dynamic>>> getQueue() async {
    final resp = await _dio.get('$_basePath/queue',
        queryParameters: {'includeMovie': true, 'pageSize': 50});
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>?;
    return records?.cast<Map<String, dynamic>>() ?? [];
  }

  Future<List<Map<String, dynamic>>> getCalendar({
    required String start,
    required String end,
  }) async {
    final resp = await _dio.get('$_basePath/calendar',
        queryParameters: {'start': start, 'end': end});
    return (resp.data as List<dynamic>).cast<Map<String, dynamic>>();
  }

  /// Fetches the queue with full movie details, typed.
  Future<List<RadarrQueueItem>> getQueueDetailed() async {
    final resp = await _dio.get('$_basePath/queue', queryParameters: {
      'page': 1,
      'pageSize': 100,
      'includeMovie': true,
    });
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>? ?? [];
    return records
        .map((r) => RadarrQueueItem.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// Removes a queue item, optionally from the download client / blocklist.
  /// [changeCategory] hands the download to the post-import category instead of
  /// deleting it (e.g. for Unpackerr); [skipRedownload] suppresses the
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
  Future<RadarrHistoryPage> getHistory({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/history', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'date',
      'sortDirection': 'descending',
    });
    return RadarrHistoryPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// History for a single movie, newest first. Uses the non-paged
  /// /history/movie endpoint.
  Future<List<RadarrHistoryRecord>> getMovieHistory(int movieId) async {
    final resp = await _dio.get('$_basePath/history/movie',
        queryParameters: {'movieId': movieId});
    final records = (resp.data as List<dynamic>)
        .map((r) => RadarrHistoryRecord.fromJson(r as Map<String, dynamic>))
        .toList();
    records.sort(
        (a, b) => (b.date ?? DateTime(0)).compareTo(a.date ?? DateTime(0)));
    return records;
  }

  /// Fetches a page of monitored movies that have no file, newest in
  /// cinemas first.
  Future<RadarrWantedPage> getWantedMissing({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/missing', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'movieMetadata.inCinemas',
      'sortDirection': 'descending',
      'monitored': true,
    });
    return RadarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored movies whose file is below the quality
  /// profile cutoff, newest in cinemas first.
  Future<RadarrWantedPage> getWantedCutoff({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/cutoff', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'movieMetadata.inCinemas',
      'sortDirection': 'descending',
      'monitored': true,
    });
    return RadarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Interactive release search. Slow (10-60s): indexers are queried live.
  Future<List<RadarrRelease>> getReleases(int movieId) async {
    final resp = await _dio.get(
      '$_basePath/release',
      queryParameters: {'movieId': movieId},
      options: Options(receiveTimeout: const Duration(seconds: 120)),
    );
    return (resp.data as List<dynamic>)
        .map((r) => RadarrRelease.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// Alias for [getReleases] (naming parity with the Sonarr drill-down).
  Future<List<RadarrRelease>> getMovieReleases(int movieId) =>
      getReleases(movieId);

  /// Sends a release from interactive search to the download client.
  Future<void> grabRelease({
    required String guid,
    required int indexerId,
  }) async {
    await _dio.post(
      '$_basePath/release',
      data: {'guid': guid, 'indexerId': indexerId},
      options: Options(receiveTimeout: const Duration(seconds: 60)),
    );
  }

  // --- Import Doctor (admin; proxy requires instances:manage) ---

  /// Lists the importable files Radarr found for a finished download, with any
  /// rejection reasons. Backs the manual-import recovery flow.
  Future<List<RadarrManualImportCandidate>> getManualImportCandidates(
    String downloadId,
  ) async {
    final resp = await _dio.get(
      '$_basePath/manualimport',
      queryParameters: {
        'downloadId': downloadId,
        'filterExistingFiles': false,
      },
      options: Options(receiveTimeout: const Duration(seconds: 60)),
    );
    return (resp.data as List<dynamic>)
        .map((c) =>
            RadarrManualImportCandidate.fromJson(c as Map<String, dynamic>))
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

  /// Nudges Radarr to run its completed-download import pass now (clears items
  /// stuck "waiting to import").
  Future<void> processMonitoredDownloads() async {
    await _dio.post('$_basePath/command',
        data: {'name': 'ProcessMonitoredDownloads'});
  }

  /// Rescans a movie's files on disk (retries imports blocked by a transient
  /// path/permissions problem).
  Future<void> rescanMovie(int movieId) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'RescanMovie',
      'movieIds': [movieId],
    });
  }
}
