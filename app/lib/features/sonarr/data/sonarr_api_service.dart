import 'package:dio/dio.dart';
import 'sonarr_models.dart';

/// Networking layer for Sonarr, proxied through the Cantinarr backend.
///
/// Supports both instance-specific paths (/api/instances/{id}/...) and
/// legacy paths (/api/sonarr/...) for backward compatibility.
class SonarrApiService {
  final Dio _dio;
  final String? _instanceId;

  SonarrApiService({required Dio backendDio, String? instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  /// Returns the base path prefix for API calls.
  String get _basePath => _instanceId != null
      ? '/api/instances/$_instanceId/api/v3'
      : '/api/sonarr/api/v3';

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
    final resp = await _dio.get('$_basePath/series/lookup',
        queryParameters: {'term': term});
    return (resp.data as List<dynamic>)
        .map((s) => SonarrSeries.fromJson(s as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrEpisode>> getEpisodes(int seriesId) async {
    final resp = await _dio.get('$_basePath/episode',
        queryParameters: {'seriesId': seriesId});
    return (resp.data as List<dynamic>)
        .map((e) => SonarrEpisode.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('$_basePath/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) =>
            SonarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('$_basePath/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => SonarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<SonarrSeries> addSeries(Map<String, dynamic> seriesData) async {
    final resp =
        await _dio.post('$_basePath/series', data: seriesData);
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<SonarrSeries> updateSeries(
      int id, Map<String, dynamic> data) async {
    final resp =
        await _dio.put('$_basePath/series/$id', data: data);
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
}
