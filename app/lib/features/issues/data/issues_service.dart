import 'package:dio/dio.dart';

import 'issue_models.dart';

/// REST client for the issue-reporting / AI-remediation feature.
///
/// Talks to the Wave-1 contract (snake_case). The server may not be merged
/// yet: a 404 from any endpoint is expected pre-merge and surfaces as a thrown
/// [DioException] that callers handle like any other transient failure.
class IssuesService {
  final Dio _dio;

  IssuesService({required Dio backendDio}) : _dio = backendDio;

  // ---- Reporter-facing -----------------------------------------------------

  /// Submit a problem report. Returns the new issue id (the server also echoes
  /// the initial status, which the caller doesn't currently need).
  Future<int> reportProblem({
    required String mediaType, // 'movie' | 'tv'
    required int tmdbId,
    int? tvdbId,
    int? seasonNumber,
    int? episodeNumber,
    required IssueCategory category,
    String? reason,
    String? title,
  }) async {
    final body = <String, dynamic>{
      'media_type': mediaType,
      'tmdb_id': tmdbId,
      'category': category.value,
    };
    if (tvdbId != null && tvdbId != 0) body['tvdb_id'] = tvdbId;
    if (seasonNumber != null && seasonNumber > 0) {
      body['season_number'] = seasonNumber;
    }
    if (episodeNumber != null && episodeNumber > 0) {
      body['episode_number'] = episodeNumber;
    }
    final trimmedReason = reason?.trim();
    if (trimmedReason != null && trimmedReason.isNotEmpty) {
      body['reason'] = trimmedReason;
    }
    if (title != null && title.isNotEmpty) body['title'] = title;

    final resp = await _dio.post('/api/issues', data: body);
    final data = resp.data as Map<String, dynamic>?;
    return (data?['issue_id'] as num?)?.toInt() ?? 0;
  }

  /// Fetch one issue plus its full message thread (reporter or admin).
  Future<IssueThread> getThread(int id) async {
    final resp = await _dio.get('/api/issues/$id');
    return IssueThread.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Append a reply to an issue thread (reporter or admin note).
  Future<void> reply(int id, String body) async {
    await _dio.post('/api/issues/$id/reply', data: {'body': body});
  }

  // ---- Admin ---------------------------------------------------------------

  /// List issues for the admin queue, optionally filtered by [status].
  Future<List<Issue>> listIssues({String? status}) async {
    final resp = await _dio.get(
      '/api/admin/issues',
      queryParameters: {
        if (status != null && status.isNotEmpty) 'status': status,
      },
    );
    final data = resp.data as Map<String, dynamic>?;
    return ((data?['issues'] as List?) ?? const [])
        .map((e) => Issue.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Dismiss an issue (admin).
  Future<void> dismiss(int id) async {
    await _dio.post('/api/admin/issues/$id/dismiss');
  }

  /// Read the admin-tunable remediation settings.
  Future<RemediationSettings> getSettings() async {
    final resp = await _dio.get('/api/admin/remediation-settings');
    return RemediationSettings.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Persist the admin-tunable remediation settings, returning the stored
  /// (normalized) values.
  Future<RemediationSettings> updateSettings(
      RemediationSettings settings) async {
    final resp = await _dio.put(
      '/api/admin/remediation-settings',
      data: settings.toJson(),
    );
    return RemediationSettings.fromJson(resp.data as Map<String, dynamic>);
  }
}
