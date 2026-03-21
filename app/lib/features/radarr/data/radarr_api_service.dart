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
    final resp =
        await _dio.get('$_basePath/movie', queryParameters: params);
    return (resp.data as List<dynamic>)
        .map((m) => RadarrMovie.fromJson(m as Map<String, dynamic>))
        .toList();
  }

  Future<RadarrMovie> getMovie(int id) async {
    final resp = await _dio.get('$_basePath/movie/$id');
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

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
        .map((p) =>
            RadarrQualityProfile.fromJson(p as Map<String, dynamic>))
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
    final resp =
        await _dio.put('$_basePath/movie/$id', data: movieData);
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
}
