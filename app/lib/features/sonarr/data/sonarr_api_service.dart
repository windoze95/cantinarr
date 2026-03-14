import 'package:dio/dio.dart';
import 'sonarr_models.dart';

/// Networking layer for Sonarr, proxied through the Cantinarr backend.
///
/// All requests go to /api/sonarr/... which the backend forwards to
/// the configured Sonarr instance.
class SonarrApiService {
  final Dio _dio;

  SonarrApiService({required Dio backendDio}) : _dio = backendDio;

  Future<SonarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('/api/sonarr/api/v3/system/status');
    return SonarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<SonarrSeries>> getSeries() async {
    final resp = await _dio.get('/api/sonarr/api/v3/series');
    return (resp.data as List<dynamic>)
        .map((s) => SonarrSeries.fromJson(s as Map<String, dynamic>))
        .toList();
  }

  Future<SonarrSeries> getSeriesById(int id) async {
    final resp = await _dio.get('/api/sonarr/api/v3/series/$id');
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<SonarrSeries>> lookupSeries(String term) async {
    final resp = await _dio.get('/api/sonarr/api/v3/series/lookup',
        queryParameters: {'term': term});
    return (resp.data as List<dynamic>)
        .map((s) => SonarrSeries.fromJson(s as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrEpisode>> getEpisodes(int seriesId) async {
    final resp = await _dio.get('/api/sonarr/api/v3/episode',
        queryParameters: {'seriesId': seriesId});
    return (resp.data as List<dynamic>)
        .map((e) => SonarrEpisode.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('/api/sonarr/api/v3/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) =>
            SonarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<SonarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('/api/sonarr/api/v3/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => SonarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<SonarrSeries> addSeries(Map<String, dynamic> seriesData) async {
    final resp =
        await _dio.post('/api/sonarr/api/v3/series', data: seriesData);
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<SonarrSeries> updateSeries(
      int id, Map<String, dynamic> data) async {
    final resp =
        await _dio.put('/api/sonarr/api/v3/series/$id', data: data);
    return SonarrSeries.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteSeries(int id, {bool deleteFiles = false}) async {
    await _dio.delete('/api/sonarr/api/v3/series/$id',
        queryParameters: {'deleteFiles': deleteFiles});
  }

  Future<void> searchSeries(int seriesId) async {
    await _dio.post('/api/sonarr/api/v3/command', data: {
      'name': 'SeriesSearch',
      'seriesId': seriesId,
    });
  }

  Future<void> searchSeason(int seriesId, int seasonNumber) async {
    await _dio.post('/api/sonarr/api/v3/command', data: {
      'name': 'SeasonSearch',
      'seriesId': seriesId,
      'seasonNumber': seasonNumber,
    });
  }
}
