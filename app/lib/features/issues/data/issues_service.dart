import 'package:dio/dio.dart';

import 'agent_action_models.dart';
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

  // ---- Agent actions (admin approval queue) --------------------------------

  /// List proposed agent actions awaiting an admin decision — the approval
  /// queue. Defaults to `proposed`; pass another [status] to inspect a
  /// different bucket (e.g. `executed`). The server has no `issue_id` filter,
  /// so a per-issue view fetches `proposed` and filters client-side
  /// ([pendingActionsForIssue]).
  Future<List<AgentAction>> listPendingActions({String status = 'proposed'}) async {
    final resp = await _dio.get(
      '/api/admin/agent-actions',
      queryParameters: {if (status.isNotEmpty) 'status': status},
    );
    final data = resp.data as Map<String, dynamic>?;
    return ((data?['actions'] as List?) ?? const [])
        .map((e) => AgentAction.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// The proposed actions for one issue, filtered client-side from the queue
  /// (there is no server-side `issue_id` filter). Used to surface the
  /// ProposedActionCard inline in the issue thread.
  Future<List<AgentAction>> pendingActionsForIssue(int issueId) async {
    final all = await listPendingActions();
    return all.where((a) => a.issueId == issueId).toList();
  }

  /// Approve a proposed action, optionally replacing its params with an admin
  /// [override] (a JSON object for the action's kind). Returns the updated
  /// action (now `executing`/`executed`/`failed`) so the UI can freeze the card
  /// from the authoritative server state.
  ///
  /// The server tolerates an empty body, so when there's no override we send
  /// none rather than an empty object.
  Future<AgentAction> approveAction(int id, {Object? override}) async {
    final resp = await _dio.post(
      '/api/admin/agent-actions/$id/approve',
      data: override == null ? null : {'override': override},
    );
    return AgentAction.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Deny a proposed action with an optional [note]. A denial returns the
  /// investigation to `investigating` server-side (not a terminal failure).
  /// Returns the updated (now `denied`) action.
  Future<AgentAction> denyAction(int id, {String? note}) async {
    final trimmed = note?.trim();
    final resp = await _dio.post(
      '/api/admin/agent-actions/$id/deny',
      data: {'note': trimmed ?? ''},
    );
    return AgentAction.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetch one agent run plus its ordered audit steps, for the read-only
  /// "agent activity" timeline.
  Future<AgentRunDetail> getRun(int id) async {
    final resp = await _dio.get('/api/admin/agent-runs/$id');
    return AgentRunDetail.fromJson(resp.data as Map<String, dynamic>);
  }
}
