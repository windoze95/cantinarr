import 'package:dio/dio.dart';

import '../../../core/network/long_request_options.dart';
import 'agent_action_models.dart';
import 'agent_approval_rule_models.dart';
import 'issue_models.dart';

const _aiValidationReceiveTimeout = Duration(seconds: 75);

/// Receive timeout for one approve-batch request. The server decides items
/// sequentially (arr preflights plus one mutation each), so a large wave is
/// legitimately slower than the default request timeout. The server runs the
/// batch to completion even if this client gives up waiting, so a timeout
/// here means "refresh to see what ran", never "nothing happened".
const _batchApproveReceiveTimeout = Duration(minutes: 3);

/// REST client for the issue-reporting / AI-remediation feature.
///
/// Talks to the Wave-1 contract (snake_case). The server may not be merged
/// yet: a 404 from any endpoint is expected pre-merge and surfaces as a thrown
/// [DioException] that callers handle like any other transient failure.
class IssuesService {
  final Dio _dio;

  IssuesService({required Dio backendDio}) : _dio = backendDio;

  // ---- Reporter-facing -----------------------------------------------------

  /// Submit a problem report. Returns the issue id and authoritative initial
  /// status so the caller can distinguish passive arr recovery from agent
  /// investigation.
  Future<IssueReportResult> reportProblem({
    required String instanceId,
    required String mediaType, // 'movie' | 'tv' | 'book'
    required int tmdbId,
    int? tvdbId,
    int? seasonNumber,
    int? episodeNumber,
    String? foreignId, // book: Chaptarr foreignBookId
    String? bookFormat, // book: 'ebook' | 'audiobook'
    required IssueCategory category,
    String? reason,
    String? title,
  }) async {
    final body = <String, dynamic>{
      'instance_id': instanceId,
      'media_type': mediaType,
      'category': category.value,
    };
    // Books have no TMDB identity; their scope is the library foreignBookId
    // (plus format when the title exists as both ebook and audiobook).
    if (mediaType == 'book') {
      if (foreignId != null && foreignId.isNotEmpty) {
        body['foreign_id'] = foreignId;
      }
      if (bookFormat != null && bookFormat.isNotEmpty) {
        body['book_format'] = bookFormat;
      }
    } else {
      body['tmdb_id'] = tmdbId;
    }
    if (tvdbId != null && tvdbId != 0) body['tvdb_id'] = tvdbId;
    // Season zero is a real Sonarr season (Specials). A positive episode makes
    // the zero unambiguous, so preserve it instead of collapsing S00E## into a
    // series-wide report.
    if (seasonNumber != null &&
        (seasonNumber > 0 || (episodeNumber != null && episodeNumber > 0))) {
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
    return IssueReportResult.fromJson(
      resp.data as Map<String, dynamic>? ?? const {},
    );
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

  /// Record the reporter's own verdict that the applied fix worked, closing the
  /// issue. Only the reporter may call it — an admin's verdict goes through
  /// [resolveIssue] so the audit trail never attributes one to the other — and
  /// it is irreversible: the thread stops accepting replies.
  ///
  /// A refusal (the issue closed meanwhile, or a fix started executing) arrives
  /// as a 409 whose body carries the server's explanation; the thrown
  /// [DioException] keeps that response so the caller can show it verbatim
  /// instead of a generic failure.
  Future<void> confirmFixed(int issueId) async {
    await _dio.post('/api/issues/$issueId/confirm-fixed');
  }

  // ---- Admin ---------------------------------------------------------------

  /// List issues for the admin queue, optionally filtered by [status].
  /// Triples the admin has hand-approved and could automate.
  Future<List<Map<String, dynamic>>> listRuleCandidates() async {
    final resp = await _dio.get('/api/admin/agent-approval-rules/candidates');
    return ((resp.data['candidates'] as List?) ?? const [])
        .whereType<Map<String, dynamic>>()
        .toList();
  }

  /// Arm a rule from the catalog (server re-checks the grounding).
  Future<void> armRule(String problemKind, String actionKind, String actionFacet) async {
    await _dio.post('/api/admin/agent-approval-rules', data: {
      'problem_kind': problemKind,
      'action_kind': actionKind,
      'action_facet': actionFacet,
    });
  }

  /// The agent scoreboard: what the pipeline did over the trailing window.
  Future<Map<String, dynamic>> agentDigest({int days = 7}) async {
    final resp = await _dio
        .get('/api/admin/agent-digest', queryParameters: {'days': days});
    return (resp.data as Map).cast<String, dynamic>();
  }

  /// The reporter inbox: the caller's OWN reports, requester copy applied
  /// server-side. Non-admin accessible.
  Future<List<Issue>> listMyIssues() async {
    final resp = await _dio.get('/api/issues');
    final list = (resp.data['issues'] as List?) ?? const [];
    return list
        .map((e) => Issue.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// The admin issue list. Open issues always arrive in full; the closed tail
  /// is bounded server-side, and [IssuePage.closedTotal] is how much history
  /// exists — so the Closed tab can say what it is not showing instead of
  /// presenting a truncated list as the whole story.
  Future<IssuePage> listIssues({String? status}) async {
    final resp = await _dio.get(
      '/api/admin/issues',
      queryParameters: {
        if (status != null && status.isNotEmpty) 'status': status,
      },
    );
    final data = resp.data as Map<String, dynamic>?;
    return IssuePage(
      issues: ((data?['issues'] as List?) ?? const [])
          .map((e) => Issue.fromJson(e as Map<String, dynamic>))
          .toList(),
      closedTotal: (data?['closed_total'] as num?)?.toInt() ?? 0,
    );
  }

  /// Dismiss an issue (admin).
  Future<void> dismiss(int id) async {
    await _dio.post('/api/admin/issues/$id/dismiss');
  }

  /// Complete an issue after human review. The server atomically closes the
  /// aggregate and records the required note/admin provenance. Dismissal is a
  /// separate endpoint and is intentionally not representable here.
  Future<Issue> resolveIssue(
    int id, {
    required AdminIssueDisposition disposition,
    required String note,
  }) async {
    final resp = await _dio.post(
      '/api/admin/issues/$id/resolve',
      data: {
        'disposition': disposition.value,
        'note': note.trim(),
      },
    );
    return Issue.fromJson(resp.data as Map<String, dynamic>);
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
      options: longRequestOptions(timeout: _aiValidationReceiveTimeout),
    );
    return RemediationSettings.fromJson(resp.data as Map<String, dynamic>);
  }

  // ---- Agent actions (admin approval queue) --------------------------------

  /// List proposed agent actions awaiting an admin decision — the approval
  /// queue. Defaults to `proposed`; pass another [status] to inspect a
  /// different bucket (e.g. `executed`) or `all` for permanent history.
  Future<List<AgentAction>> listPendingActions(
      {String status = 'proposed'}) async {
    final resp = await _dio.get(
      '/api/admin/agent-actions',
      queryParameters: {if (status.isNotEmpty) 'status': status},
    );
    final data = resp.data as Map<String, dynamic>?;
    return ((data?['actions'] as List?) ?? const [])
        .map((e) => AgentAction.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// List the complete durable action history. `status=all` is explicit so a
  /// future server default cannot silently turn this back into a pending-only
  /// view.
  Future<List<AgentAction>> listAllActions() =>
      listPendingActions(status: 'all');

  /// Fetch the authoritative current state of one action. Used after an
  /// ambiguous approval response so the client never asks an admin to retry a
  /// change that may already have executed.
  Future<AgentAction> getAction(int id) async {
    final resp = await _dio.get('/api/admin/agent-actions/$id');
    return AgentAction.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetch every action and agent-run summary for one issue, including
  /// terminal/superseded actions that have left the approval queue.
  Future<IssueAgentActivity> getIssueActivity(int issueId) async {
    final resp = await _dio.get('/api/admin/issues/$issueId/activity');
    return IssueAgentActivity.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Approve a proposed action, optionally replacing its params with an admin
  /// [override] (a JSON object for the action's kind). Passing [remember]
  /// additionally arms a standing auto-approval rule for this action's
  /// (problem, fix, facet) triple — only offered when the server sent an
  /// `auto_approval_offer`. Returns the updated action (now
  /// `executing`/`executed`/`failed`) so the UI can freeze the card from the
  /// authoritative server state.
  ///
  /// The server tolerates an empty body, so the plain approve still sends none
  /// rather than an empty object (old-server wire shape unchanged).
  Future<AgentAction> approveAction(
    int id, {
    Object? override,
    bool remember = false,
  }) async {
    final body = <String, dynamic>{
      if (override != null) 'override': override,
      if (remember) 'remember': true,
    };
    final resp = await _dio.post(
      '/api/admin/agent-actions/$id/approve',
      data: body.isEmpty ? null : body,
    );
    return AgentAction.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Approve an explicit list of reviewed proposals in one request. The server
  /// decides each id sequentially through the same at-most-once core as
  /// [approveAction] — one item's conflict never fails the rest — and returns
  /// a per-item verdict. There is deliberately no "approve everything" form:
  /// [ids] must be the proposals the admin was actually shown.
  Future<List<AgentActionBatchResult>> approveActionsBatch(
      List<int> ids) async {
    final resp = await _dio.post(
      '/api/admin/agent-actions/approve-batch',
      data: {'ids': ids},
      options: longRequestOptions(timeout: _batchApproveReceiveTimeout),
    );
    final data = resp.data as Map<String, dynamic>?;
    return ((data?['results'] as List?) ?? const [])
        .map((e) => AgentActionBatchResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---- Standing auto-approval rules (admin) --------------------------------

  /// List every standing auto-approval rule with its status and counters.
  Future<List<AgentApprovalRule>> listApprovalRules() async {
    final resp = await _dio.get('/api/admin/agent-approval-rules');
    final data = resp.data as Map<String, dynamic>?;
    return ((data?['rules'] as List?) ?? const [])
        .map((e) => AgentApprovalRule.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Stop a rule from matching (idempotent). Returns the updated rule.
  Future<AgentApprovalRule> pauseApprovalRule(int id) async {
    final resp = await _dio.post('/api/admin/agent-approval-rules/$id/pause');
    return AgentApprovalRule.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Re-arm a paused rule (idempotent). Returns the updated rule.
  Future<AgentApprovalRule> resumeApprovalRule(int id) async {
    final resp = await _dio.post('/api/admin/agent-approval-rules/$id/resume');
    return AgentApprovalRule.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Remove a rule. Decided-action history keeps its attribution server-side.
  Future<void> deleteApprovalRule(int id) async {
    await _dio.delete('/api/admin/agent-approval-rules/$id');
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
