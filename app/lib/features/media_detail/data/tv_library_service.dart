import 'package:dio/dio.dart';

import '../../discover/data/tmdb_models.dart';
import '../../request/data/request_service.dart';
import '../../request/data/tv_match_service.dart';
import '../../request/data/request_quota.dart';

/// One native series, with every season and any verified catalog mappings.
/// A positive catalogId means an ordinary show: use its unchanged title page.
class TVLibraryDetail {
  final int catalogId;
  final TVDetail? detail;
  final RequestStatusDetail status;
  final List<TVMatch> matches;
  final String revision;

  TVLibraryDetail.fromJson(Map<String, dynamic> json)
      : catalogId = json['tmdb_id'] as int? ?? 0,
        detail = json['detail'] is Map<String, dynamic>
            ? TVDetail.fromJson(json['detail'] as Map<String, dynamic>) : null,
        status = RequestStatusDetail.fromJson(
            json['status'] as Map<String, dynamic>? ?? {'status_known': false}),
        matches = [for (final value in (json['matches'] as List?) ?? [])
          TVMatch.fromJson(value as Map<String, dynamic>)],
        revision = json['revision'] as String? ?? '';

  TVMatch? sourceForSeason(int number) {
    for (final match in matches) {
      if (match.seasonMap.containsValue(number)) return match;
    }
    return null;
  }
}

/// Adapts the existing request notifier/table to library-numbered seasons.
/// Translation, permissions, approval, and quotas remain server-owned.
class TVLibraryService extends RequestService {
  final Dio dio;
  final String libraryId;
  final int seriesId;
  TVLibraryDetail? current;

  TVLibraryService({required this.dio, required this.libraryId,
    required this.seriesId}) : super(backendDio: dio);

  Future<TVLibraryDetail> load({CancelToken? cancelToken}) async {
    final response = await dio.get('/api/requests/tv-library',
      queryParameters: {'instance_id': libraryId, 'series_id': seriesId},
      cancelToken: cancelToken);
    final data = response.data as Map<String, dynamic>;
    if (data['instance_id'] != libraryId || data['series_id'] != seriesId) {
      throw const FormatException('The library series changed. Reopen the title.');
    }
    final result = TVLibraryDetail.fromJson(data);
    if (result.catalogId <= 0 && (result.detail == null ||
        result.revision.isEmpty ||
        result.matches.any((m) => m.tmdbId <= 0 || m.seriesId != seriesId || !m.isResolved))) {
      throw const FormatException('Could not verify this library series.');
    }
    if (result.catalogId <= 0) {
      final seasons = result.detail!.seasons.where((s) => s.seasonNumber > 0)
          .map((s) => s.seasonNumber).toSet();
      final statuses = result.status.seasons.map((s) => s.seasonNumber).toSet();
      if (seasons.length != statuses.length || !seasons.containsAll(statuses) ||
          result.status.seasons.any((s) => !s.hasRequestIssue &&
              result.matches.where((m) => m.seasonMap.containsValue(s.seasonNumber)).length != 1)) {
        throw const FormatException('Could not verify this series\' season status.');
      }
    }
    return current = result;
  }

  @override
  Future<RequestStatusDetail> checkStatusDetail(int tmdbId, MediaType mediaType,
      {String? instanceId, bool includeInstanceStatuses = true,
      bool include4K = false, CancelToken? cancelToken}) async {
    if (instanceId != libraryId) throw const FormatException('Library changed');
    final result = await load(cancelToken: cancelToken);
    if (result.catalogId > 0) throw const FormatException('Reopen this title');
    return result.status;
  }

  @override
  bool get refreshStatusAfterFailure => true;

  @override
  Future<RequestStatus?> request({required int tmdbId,
      required MediaType mediaType, String? title, int? tvdbId,
      String? seasonScope, List<int>? seasons, int? qualityProfileId,
      String? instanceId}) async {
    lastRequestError = null;
    lastRequestQuotaExceeded = false;
    if (instanceId != libraryId || current == null) return null;
    try {
      final response = await dio.post('/api/requests/tv-library', data: {
        'instance_id': libraryId, 'series_id': seriesId,
        'revision': current!.revision,
        if (seasons?.isNotEmpty ?? false) 'seasons': seasons,
        if (seasonScope != null) 'season_scope': seasonScope,
        if (qualityProfileId != null) 'quality_profile_id': qualityProfileId,
      });
      final data = response.data as Map<String, dynamic>;
      if (data['success'] != true) {
        lastRequestError = data['error'] as String? ?? 'Could not request these seasons.';
        return null;
      }
      return RequestStatus.requested;
    } catch (error) {
      final quota = requestQuotaError(error);
      lastRequestQuotaExceeded = quota != null;
      lastRequestError = quota ?? tvMatchError(error);
      return null;
    }
  }
}
