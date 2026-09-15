import 'package:dio/dio.dart';
import 'tdarr_models.dart';

/// Tdarr is read through normalized Cantinarr endpoints. No Tdarr URL or
/// credential is available to the module.
class TdarrApiService {
  final Dio dio;
  final String instanceId;

  const TdarrApiService(this.dio, this.instanceId);

  Future<Map<String, dynamic>> _read(String view, CancelToken cancelToken,
      {String? libraryId}) async {
    final response = await dio.get('/api/tdarr/$instanceId/$view',
        queryParameters: {
          if (libraryId != null) 'library_id': libraryId,
        }, cancelToken: cancelToken);
    return response.data as Map<String, dynamic>;
  }

  Future<TdarrActivity> activity(CancelToken token) async =>
      TdarrActivity.fromJson(await _read('activity', token));
  Future<TdarrLibraries> libraries(CancelToken token) async =>
      TdarrLibraries.fromJson(await _read('libraries', token));
  Future<TdarrStats> stats(CancelToken token, String? libraryId) async =>
      TdarrStats.fromJson(await _read('stats', token, libraryId: libraryId));
}
