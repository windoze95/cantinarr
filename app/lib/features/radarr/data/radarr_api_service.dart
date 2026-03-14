import 'package:dio/dio.dart';
import 'radarr_models.dart';

/// Networking layer for Radarr, proxied through the Cantinarr backend.
///
/// All requests go to /api/radarr/... which the backend forwards to
/// the configured Radarr instance.
class RadarrApiService {
  final Dio _dio;

  RadarrApiService({required Dio backendDio}) : _dio = backendDio;

  Future<RadarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('/api/radarr/api/v3/system/status');
    return RadarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<RadarrMovie>> getMovies({String? searchTerm}) async {
    final params = <String, dynamic>{};
    if (searchTerm != null && searchTerm.isNotEmpty) {
      params['searchTerm'] = searchTerm;
    }
    final resp =
        await _dio.get('/api/radarr/api/v3/movie', queryParameters: params);
    return (resp.data as List<dynamic>)
        .map((m) => RadarrMovie.fromJson(m as Map<String, dynamic>))
        .toList();
  }

  Future<RadarrMovie> getMovie(int id) async {
    final resp = await _dio.get('/api/radarr/api/v3/movie/$id');
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<RadarrMovie>> lookupMovies(String term) async {
    final resp = await _dio
        .get('/api/radarr/api/v3/movie/lookup', queryParameters: {'term': term});
    return (resp.data as List<dynamic>)
        .map((m) => RadarrMovie.fromJson(m as Map<String, dynamic>))
        .toList();
  }

  Future<List<RadarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('/api/radarr/api/v3/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) =>
            RadarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<RadarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('/api/radarr/api/v3/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => RadarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<RadarrMovie> addMovie(Map<String, dynamic> movieData) async {
    final resp = await _dio.post('/api/radarr/api/v3/movie', data: movieData);
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<RadarrMovie> updateMovie(
      int id, Map<String, dynamic> movieData) async {
    final resp =
        await _dio.put('/api/radarr/api/v3/movie/$id', data: movieData);
    return RadarrMovie.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteMovie(int id,
      {bool deleteFiles = false, bool addExclusion = false}) async {
    await _dio.delete('/api/radarr/api/v3/movie/$id', queryParameters: {
      'deleteFiles': deleteFiles,
      'addImportListExclusion': addExclusion,
    });
  }

  Future<void> searchMovie(int movieId) async {
    await _dio.post('/api/radarr/api/v3/command', data: {
      'name': 'MoviesSearch',
      'movieIds': [movieId],
    });
  }
}
