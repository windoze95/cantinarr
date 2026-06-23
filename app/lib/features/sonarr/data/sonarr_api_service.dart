import 'package:dio/dio.dart';
import 'sonarr_models.dart';

/// Networking layer for Sonarr, proxied through the Cantinarr backend.
class SonarrApiService {
  final Dio _dio;
  final String _instanceId;

  SonarrApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  /// Returns the base path prefix for API calls.
  String get _basePath => '/api/instances/$_instanceId/api/v3';

  Future<SonarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('$_basePath/system/status');
    return SonarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<SonarrSeries>> getSeries() async {
    final resp = await _dio.get('$_basePath/series');
    return (resp.data as List<dynamic>)
        .map((s) => SonarrSeries.fromJson(s as Map<String, dynamic>))
        .toList();
  }

  Future<SonarrSeries> getSeriesById(int id) async {
    final resp = await _dio.get('$_basePath/series/$id');
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<SonarrSeries>> lookupSeries(String term) async {
    final resp = await _dio
        .get('$_basePath/series/lookup', queryParameters: {'term': term});
    return (resp.data as List<dynamic>)
        .map((s) => SonarrSeries.fromJson(s as Map<String, dynamic>))
        .toList();
  }

  /// Lists episodes for a series, optionally narrowed to one season. When
  /// [includeEpisodeFile] is set, each downloaded episode carries its file
  /// (quality + size) inline — drives the per-episode status line.
  Future<List<SonarrEpisode>> getEpisodes(
    int seriesId, {
    int? seasonNumber,
    bool includeEpisodeFile = false,
  }) async {
    final resp = await _dio.get('$_basePath/episode', queryParameters: {
      'seriesId': seriesId,
      if (seasonNumber != null) 'seasonNumber': seasonNumber,
      if (includeEpisodeFile) 'includeEpisodeFile': true,
    });
    return (resp.data as List<dynamic>)
        .map((e) => SonarrEpisode.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('$_basePath/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) => SonarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('$_basePath/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => SonarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<SonarrSeries> addSeries(Map<String, dynamic> seriesData) async {
    final resp = await _dio.post('$_basePath/series', data: seriesData);
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<SonarrSeries> updateSeries(int id, Map<String, dynamic> data) async {
    final resp = await _dio.put('$_basePath/series/$id', data: data);
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteSeries(int id, {bool deleteFiles = false}) async {
    await _dio.delete('$_basePath/series/$id',
        queryParameters: {'deleteFiles': deleteFiles});
  }

  Future<void> searchSeries(int seriesId) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'SeriesSearch',
      'seriesId': seriesId,
    });
  }

  Future<void> searchSeason(int seriesId, int seasonNumber) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'SeasonSearch',
      'seriesId': seriesId,
      'seasonNumber': seasonNumber,
    });
  }

  /// Triggers an automatic indexer search for the given episodes.
  Future<void> searchEpisodes(List<int> episodeIds) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'EpisodeSearch',
      'episodeIds': episodeIds,
    });
  }

  Future<List<Map<String, dynamic>>> getQueue() async {
    final resp = await _dio.get('$_basePath/queue',
        queryParameters: {'includeSeries': true, 'pageSize': 50});
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

  /// Fetches the queue with full series + episode details, typed.
  Future<List<SonarrQueueItem>> getQueueDetailed() async {
    final resp = await _dio.get('$_basePath/queue', queryParameters: {
      'page': 1,
      'pageSize': 100,
      'includeSeries': true,
      'includeEpisode': true,
    });
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>? ?? [];
    return records
        .map((r) => SonarrQueueItem.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// Removes a queue item, optionally from the download client / blocklist.
  Future<void> deleteQueueItem(
    int id, {
    bool removeFromClient = true,
    bool blocklist = false,
  }) async {
    await _dio.delete('$_basePath/queue/$id', queryParameters: {
      'removeFromClient': removeFromClient,
      'blocklist': blocklist,
    });
  }

  /// Fetches a page of history events, newest first.
  Future<SonarrHistoryPage> getHistory({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/history', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'date',
      'sortDirection': 'descending',
    });
    return SonarrHistoryPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored episodes that are missing a file, newest
  /// air date first. Records include series context.
  Future<SonarrWantedPage> getWantedMissing({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/missing', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'episodes.airDateUtc',
      'sortDirection': 'descending',
      'monitored': true,
      'includeSeries': true,
    });
    return SonarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored episodes whose file is below the quality
  /// profile cutoff, newest air date first. Records include series context
  /// and the current episode file (for its quality).
  Future<SonarrWantedPage> getWantedCutoff({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/cutoff', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'episodes.airDateUtc',
      'sortDirection': 'descending',
      'monitored': true,
      'includeSeries': true,
      'includeEpisodeFile': true,
    });
    return SonarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Interactive release search for one season.
  /// Slow (10-60s): indexers are queried live.
  Future<List<SonarrRelease>> getReleases({
    required int seriesId,
    required int seasonNumber,
  }) async {
    final resp = await _dio.get(
      '$_basePath/release',
      queryParameters: {'seriesId': seriesId, 'seasonNumber': seasonNumber},
      options: Options(receiveTimeout: const Duration(seconds: 120)),
    );
    return (resp.data as List<dynamic>)
        .map((r) => SonarrRelease.fromJson(r as Map<String, dynamic>))
        .toList();
  }

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

  /// Interactive release search for a single episode (per-episode picker).
  /// Slow (10-60s): indexers are queried live.
  Future<List<SonarrRelease>> getEpisodeReleases(int episodeId) async {
    final resp = await _dio.get(
      '$_basePath/release',
      queryParameters: {'episodeId': episodeId},
      options: Options(receiveTimeout: const Duration(seconds: 120)),
    );
    return (resp.data as List<dynamic>)
        .map((r) => SonarrRelease.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// History for a series (optionally one season), newest first. Uses the
  /// non-paged /history/series endpoint and includes episode context.
  Future<List<SonarrHistoryRecord>> getSeriesHistory(
    int seriesId, {
    int? seasonNumber,
  }) async {
    final resp = await _dio.get('$_basePath/history/series', queryParameters: {
      'seriesId': seriesId,
      if (seasonNumber != null) 'seasonNumber': seasonNumber,
      'includeEpisode': true,
    });
    final records = (resp.data as List<dynamic>)
        .map((r) => SonarrHistoryRecord.fromJson(r as Map<String, dynamic>))
        .toList();
    records.sort(
        (a, b) => (b.date ?? DateTime(0)).compareTo(a.date ?? DateTime(0)));
    return records;
  }

  /// Toggles monitoring for a single season. Sonarr requires the whole series
  /// resource on PUT, so we fetch it raw, flip the one season's flag, and send
  /// it back unchanged otherwise. Admin only (proxy requires instances:manage).
  Future<void> setSeasonMonitored(
    int seriesId,
    int seasonNumber, {
    required bool monitored,
  }) async {
    final resp = await _dio.get('$_basePath/series/$seriesId');
    final series = Map<String, dynamic>.from(resp.data as Map);
    for (final s in (series['seasons'] as List<dynamic>? ?? [])) {
      final season = s as Map<String, dynamic>;
      if (season['seasonNumber'] == seasonNumber) {
        season['monitored'] = monitored;
      }
    }
    await _dio.put('$_basePath/series/$seriesId', data: series);
  }
}
