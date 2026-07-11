// Data models for the AI-remediation *approval* surface (Wave 3, app side).
//
// These mirror the merged server JSON (snake_case) returned by the
// `agent-actions` and `agent-runs` admin routes. The agent's `params`,
// `rationale`, and `result_text` are UNTRUSTED — they are carried verbatim and
// rendered as PASSIVE, non-editable text only. No field here ever becomes a
// control, command, or button label.
//
// All enums are tolerant of unknown server values (mapped to an `unknown`
// member) so a future action kind / status can never break parsing.

import 'dart:convert';

import 'issue_models.dart';

/// The kind of arr mutation the agent proposed. Drives the plain-language
/// description on the ProposedActionCard. Unknown server kinds fall back to
/// [unknown], which renders a generic "Apply a fix" description.
enum AgentActionKind {
  grabRelease('grab_release'),
  remediateQueue('remediate_queue'),
  manualImport('manual_import'),
  triggerSearch('trigger_search'),
  rescan('rescan'),
  unknown('');

  const AgentActionKind(this.value);

  /// The snake_case value sent by the server.
  final String value;

  static AgentActionKind fromValue(String? value) => values.firstWhere(
        (k) => k.value == value,
        orElse: () => AgentActionKind.unknown,
      );
}

/// The lifecycle state of a proposed action. Only `proposed` is actionable;
/// every other state means the card is frozen (a decision was already made, or
/// it is mid-execution). Tolerant of unknown server values.
enum AgentActionStatus {
  proposed('proposed', 'Awaiting approval'),
  approved('approved', 'Approved'),
  executing('executing', 'Applying…'),
  executed('executed', 'Done'),
  denied('denied', 'Denied'),
  failed('failed', 'Failed'),
  superseded('superseded', 'Superseded'),
  outcomeUnknown('outcome_unknown', 'Outcome unknown'),
  unknown('', 'Unknown');

  const AgentActionStatus(this.value, this.label);

  final String value;
  final String label;

  /// True only while the action can still be approved or denied. Every other
  /// state freezes the card (the server's CAS rejects a second decision anyway).
  bool get isPending => this == AgentActionStatus.proposed;

  /// True after a proposal leaves the reviewable state, whether by an admin
  /// decision, execution outcome, or supersession.
  bool get isDecided =>
      this == AgentActionStatus.approved ||
      this == AgentActionStatus.executing ||
      this == AgentActionStatus.executed ||
      this == AgentActionStatus.denied ||
      this == AgentActionStatus.failed ||
      this == AgentActionStatus.superseded ||
      this == AgentActionStatus.outcomeUnknown;

  static AgentActionStatus fromValue(String? value) => values.firstWhere(
        (s) => s.value == value,
        orElse: () => AgentActionStatus.unknown,
      );
}

/// One admin-approvable proposed mutation, as returned by
/// `GET /api/admin/agent-actions`. The reified [params] are parsed into a
/// small read-only [AgentActionParams] view for plain-language rendering; the
/// raw map is retained so an unknown kind still shows its data.
class AgentAction {
  final int id;
  final int issueId;

  /// The run that produced this proposal, used to open the audit timeline.
  /// Null if the server didn't link one.
  final int? runId;

  final AgentActionKind kind;

  /// The raw server `kind` string, retained so an unknown kind can still be
  /// shown verbatim (never executed).
  final String kindRaw;

  /// Parsed, typed view over the proposal's `params` JSON object (UNTRUSTED;
  /// rendered as quoted, non-editable data).
  final AgentActionParams params;

  /// False when `params` was absent, not an object, or invalid JSON. The card
  /// remains readable but can never expose decision controls in that case.
  final bool paramsWellFormed;

  /// Canonical params the server actually approved. Null until approval. This
  /// can differ from [params] when an admin override was accepted.
  final AgentActionParams? approvedParams;

  /// The agent's plain-language justification (UNTRUSTED — quoted text only).
  final String rationale;

  /// 'mutating' (always gated) | 'safe'. Carried for display; the app never
  /// uses it to skip the approval gate.
  final String risk;

  final AgentActionStatus status;
  final String statusRaw;

  final int? decidedBy;
  final DateTime? decidedAt;

  /// The admin's deny note, if denied (UNTRUSTED — passive text).
  final String? denyReason;

  final DateTime? executedAt;

  /// The execution outcome text, once executed/failed (UNTRUSTED — passive).
  final String? resultText;

  final DateTime? createdAt;

  // Joined from the issue for the queue list view.
  final String issueTitle;
  final String issueMediaType;
  final String? issueCategory;
  final IssueStatus issueStatus;
  final DateTime? issueClosedAt;

  /// Immutable arr target copied from the issue. The display name/service are
  /// informational; [instanceId] is the authoritative execution scope.
  final String instanceId;
  final String instanceName;
  final String instanceServiceType;

  /// Authoritative server guard. The app applies additional local safety
  /// checks, but never enables controls when this is false.
  final bool canDecide;
  final String blockedReason;

  const AgentAction({
    required this.id,
    required this.issueId,
    required this.runId,
    required this.kind,
    required this.kindRaw,
    required this.params,
    required this.paramsWellFormed,
    required this.approvedParams,
    required this.rationale,
    required this.risk,
    required this.status,
    required this.statusRaw,
    required this.decidedBy,
    required this.decidedAt,
    required this.denyReason,
    required this.executedAt,
    required this.resultText,
    required this.createdAt,
    required this.issueTitle,
    required this.issueMediaType,
    required this.issueCategory,
    required this.issueStatus,
    required this.issueClosedAt,
    required this.instanceId,
    required this.instanceName,
    required this.instanceServiceType,
    required this.canDecide,
    required this.blockedReason,
  });

  factory AgentAction.fromJson(Map<String, dynamic> json) {
    // `params` arrives as a JSON object; tolerate a stringified object or a
    // missing/non-object value so a malformed proposal never crashes the queue.
    final parsedParams = _parseParams(json['params'], nullable: false);
    final parsedApproved = _parseParams(json['approved_params']);

    return AgentAction(
      id: (json['id'] as num?)?.toInt() ?? 0,
      issueId: (json['issue_id'] as num?)?.toInt() ?? 0,
      runId: (json['run_id'] as num?)?.toInt(),
      kind: AgentActionKind.fromValue(json['kind'] as String?),
      kindRaw: json['kind'] as String? ?? '',
      params: AgentActionParams(parsedParams.map ?? const {}),
      paramsWellFormed: parsedParams.wellFormed,
      approvedParams: parsedApproved.map == null
          ? null
          : AgentActionParams(parsedApproved.map!),
      rationale: json['rationale'] as String? ?? '',
      risk: json['risk'] as String? ?? 'mutating',
      status: AgentActionStatus.fromValue(json['status'] as String?),
      statusRaw: json['status'] as String? ?? '',
      decidedBy: (json['decided_by'] as num?)?.toInt(),
      decidedAt:
          DateTime.tryParse(json['decided_at'] as String? ?? '')?.toLocal(),
      denyReason: json['deny_reason'] as String?,
      executedAt:
          DateTime.tryParse(json['executed_at'] as String? ?? '')?.toLocal(),
      resultText: json['result_text'] as String?,
      createdAt:
          DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal(),
      issueTitle: json['issue_title'] as String? ?? '',
      issueMediaType: json['issue_media_type'] as String? ?? '',
      issueCategory: json['issue_category'] as String?,
      issueStatus: IssueStatus.fromValue(json['issue_status'] as String?),
      issueClosedAt: DateTime.tryParse(json['issue_closed_at'] as String? ?? '')
          ?.toLocal(),
      instanceId: json['instance_id'] as String? ?? '',
      instanceName: json['instance_name'] as String? ?? '',
      instanceServiceType: json['instance_service_type'] as String? ?? '',
      canDecide: json['can_decide'] as bool? ?? false,
      blockedReason: json['blocked_reason'] as String? ?? '',
    );
  }

  /// The fixed, local reason this action must stay read-only. A null result is
  /// required before approval/deny controls may render.
  String? get decisionBlockedReason {
    if (status != AgentActionStatus.proposed) {
      return 'This fix is no longer awaiting a decision.';
    }
    if (id <= 0 || issueId <= 0) {
      return 'The proposed fix has an invalid identifier.';
    }
    if (issueClosedAt != null || issueStatus.isTerminal) {
      return 'The issue is already closed, so this fix will not run.';
    }
    if (issueStatus == IssueStatus.unknown) {
      return 'The issue state could not be verified. Refresh before deciding.';
    }
    if (issueStatus != IssueStatus.awaitingApproval) {
      return 'The issue is no longer waiting for this fix to be reviewed.';
    }
    if (kind == AgentActionKind.unknown) {
      return 'This app does not recognize the proposed fix type.';
    }
    if (!paramsWellFormed) {
      return 'The proposed fix data is malformed.';
    }
    final validationProblem = params.validationProblem(kind);
    if (validationProblem != null) return validationProblem;
    final targetService = instanceServiceType.trim();
    if (instanceId.trim().isEmpty ||
        instanceName.trim().isEmpty ||
        targetService.isEmpty) {
      return 'The target instance could not be verified. Refresh before deciding.';
    }
    final expectedService = switch (issueMediaType) {
      'movie' => 'radarr',
      'tv' => 'sonarr',
      'book' => 'chaptarr',
      _ => '',
    };
    if (expectedService.isNotEmpty && targetService != expectedService) {
      return 'The target service does not match this issue. Refresh before deciding.';
    }
    final serverReason = blockedReason.trim();
    if (serverReason.isNotEmpty) return serverReason;
    if (!canDecide) {
      return 'The server says this fix can no longer be approved or denied.';
    }
    return null;
  }

  bool get canTakeAction => decisionBlockedReason == null;

  String get instanceServiceLabel => switch (instanceServiceType) {
        'radarr' => 'Radarr',
        'sonarr' => 'Sonarr',
        'chaptarr' => 'Chaptarr',
        final value when value.trim().isNotEmpty => value.trim(),
        _ => 'Unknown service',
      };

  String get instanceDisplayName {
    final name = instanceName.trim();
    if (name.isNotEmpty) return name;
    final id = instanceId.trim();
    return id.isNotEmpty ? id : 'Unknown instance';
  }

  static _ParsedParams _parseParams(Object? raw, {bool nullable = true}) {
    if (raw == null) {
      return _ParsedParams(null, nullable);
    }
    if (raw is Map) {
      return _ParsedParams(
        raw.map((k, v) => MapEntry(k.toString(), v)),
        true,
      );
    }
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) {
          return _ParsedParams(
            decoded.map((k, v) => MapEntry(k.toString(), v)),
            true,
          );
        }
      } catch (_) {
        // Fall through to a malformed result.
      }
    }
    return const _ParsedParams(null, false);
  }
}

class _ParsedParams {
  final Map<String, dynamic>? map;
  final bool wellFormed;

  const _ParsedParams(this.map, this.wellFormed);
}

/// A small read-only view over a proposal's `params` JSON object. Every getter
/// returns UNTRUSTED data that callers render quoted and non-editable; the view
/// never interprets a value as a command. Unknown keys are simply absent.
class AgentActionParams {
  final Map<String, dynamic> _raw;

  const AgentActionParams(this._raw);

  /// The raw decoded params map (UNTRUSTED). Exposed so an unknown kind can
  /// list its fields generically.
  Map<String, dynamic> get raw => _raw;

  bool get isEmpty => _raw.isEmpty;

  String? _str(String key) {
    final v = _raw[key];
    if (v == null) return null;
    final s = v.toString().trim();
    return s.isEmpty ? null : s;
  }

  int? _int(String key) {
    final v = _raw[key];
    if (v is num) return v.toInt();
    if (v is String) return int.tryParse(v);
    return null;
  }

  bool _bool(String key) => _raw[key] == true;

  bool _isInt(String key, {bool optional = false}) {
    if (!_raw.containsKey(key)) return optional;
    final value = _raw[key];
    return value is num && value.isFinite && value == value.toInt();
  }

  String? get mediaType => _str('media_type');

  /// grab_release: the release GUID (an opaque indexer id, shown truncated).
  String? get guid => _str('guid');
  int? get indexerId => _int('indexer_id');
  String? get releaseTitle => _str('release_title');
  String? get quality => _str('quality');
  int? get size => _int('size');
  String? get protocol => _str('protocol');
  String? get indexer => _str('indexer');
  bool get rejected => _bool('rejected');
  List<String> get rejections {
    final value = _raw['rejections'];
    if (value is! List) return const [];
    return value.whereType<String>().toList(growable: false);
  }

  int? get queueIdToReplace {
    final v = _int('queue_id_to_replace');
    return (v != null && v > 0) ? v : null;
  }

  /// remediate_queue / manual_import: the target queue item id.
  int? get queueId => _int('queue_id');

  /// remediate_queue: remove | blocklist_search | change_category.
  String? get queueAction => _str('action');

  /// manual_import: whether to force past arr's safety checks.
  bool get force => _bool('force');

  /// trigger_search / rescan: the media id and optional TV episode scope.
  int? get tmdbId => _int('tmdb_id');
  int? get season {
    final v = _int('season');
    return (v != null && v > 0) ? v : null;
  }

  int? get episode {
    final v = _int('episode');
    return (v != null && v > 0) ? v : null;
  }

  int? get authorId => _int('author_id');
  int? get bookId => _int('book_id');

  /// Mirrors the server's strict action schemas. This is defense in depth: a
  /// future or malformed payload remains visible as history but cannot become
  /// an approval control merely because its status says `proposed`.
  String? validationProblem(AgentActionKind kind) {
    final allowed = switch (kind) {
      AgentActionKind.grabRelease => const {
          'media_type',
          'guid',
          'indexer_id',
          'queue_id_to_replace',
          'release_title',
          'quality',
          'size',
          'protocol',
          'indexer',
          'rejected',
          'rejections',
        },
      AgentActionKind.remediateQueue => const {
          'media_type',
          'queue_id',
          'action',
        },
      AgentActionKind.manualImport => const {
          'media_type',
          'queue_id',
          'force',
        },
      AgentActionKind.triggerSearch => const {
          'media_type',
          'tmdb_id',
          'season',
          'episode',
          'author_id',
          'book_id',
        },
      AgentActionKind.rescan => const {
          'media_type',
          'tmdb_id',
          'author_id',
        },
      AgentActionKind.unknown => const <String>{},
    };
    if (_raw.keys.any((key) => !allowed.contains(key))) {
      return 'The proposed fix contains fields this app does not recognize.';
    }

    final media = mediaType;
    if (_raw['media_type'] is! String ||
        (media != 'movie' && media != 'tv' && media != 'book')) {
      return 'The proposed fix has an invalid media type.';
    }
    switch (kind) {
      case AgentActionKind.grabRelease:
        if (_raw['guid'] is! String ||
            guid == null ||
            !_isInt('indexer_id') ||
            indexerId == null ||
            indexerId! <= 0) {
          return 'The release details needed to apply this fix are missing.';
        }
        if (_raw['release_title'] is! String ||
            releaseTitle == null ||
            !_isInt('size') ||
            (size ?? -1) < 0 ||
            _raw['protocol'] is! String ||
            protocol == null ||
            _raw['indexer'] is! String ||
            indexer == null ||
            (_raw.containsKey('quality') && _raw['quality'] is! String) ||
            (_raw.containsKey('rejected') && _raw['rejected'] is! bool) ||
            (_raw.containsKey('rejections') &&
                (_raw['rejections'] is! List ||
                    (_raw['rejections'] as List)
                        .any((value) => value is! String)))) {
          return 'The server-observed release details are missing or malformed.';
        }
        if (!_isInt('queue_id_to_replace', optional: true) ||
            (_int('queue_id_to_replace') ?? 0) < 0) {
          return 'The proposed queue item is invalid.';
        }
      case AgentActionKind.remediateQueue:
        if (!_isInt('queue_id') || queueId == null || queueId! <= 0) {
          return 'The proposed queue item is invalid.';
        }
        if (_raw['action'] is! String ||
            !const {
              'remove',
              'blocklist_search',
              'change_category',
            }.contains(queueAction)) {
          return 'The proposed queue change is not recognized.';
        }
      case AgentActionKind.manualImport:
        if (!_isInt('queue_id') || queueId == null || queueId! <= 0) {
          return 'The proposed queue item is invalid.';
        }
        if (_raw.containsKey('force') && _raw['force'] is! bool) {
          return 'The proposed import options are malformed.';
        }
      case AgentActionKind.triggerSearch:
        if (!_isInt('tmdb_id', optional: true) ||
            !_isInt('season', optional: true) ||
            !_isInt('episode', optional: true) ||
            !_isInt('author_id', optional: true) ||
            !_isInt('book_id', optional: true)) {
          return 'The proposed search details are malformed.';
        }
        if (media == 'book') {
          if ((authorId ?? 0) <= 0 && (bookId ?? 0) <= 0) {
            return 'The book or author needed for this search is missing.';
          }
          if ((tmdbId ?? 0) != 0 ||
              _raw.containsKey('season') ||
              _raw.containsKey('episode')) {
            return 'The proposed book search contains invalid media details.';
          }
        } else {
          if ((tmdbId ?? 0) <= 0) {
            return 'The title needed for this search is missing.';
          }
          if (media == 'movie' &&
              (_raw.containsKey('season') || _raw.containsKey('episode'))) {
            return 'The proposed movie search contains TV episode details.';
          }
          if (_raw.containsKey('episode') &&
              ((episode ?? 0) <= 0 || (season ?? 0) <= 0)) {
            return 'The proposed episode search is missing its season or episode.';
          }
        }
      case AgentActionKind.rescan:
        if (!_isInt('tmdb_id', optional: true) ||
            !_isInt('author_id', optional: true)) {
          return 'The proposed rescan details are malformed.';
        }
        if (media == 'book') {
          if ((authorId ?? 0) <= 0 || (tmdbId ?? 0) != 0) {
            return 'The author needed for this rescan is missing.';
          }
        } else if ((tmdbId ?? 0) <= 0) {
          return 'The title needed for this rescan is missing.';
        }
      case AgentActionKind.unknown:
        return 'This app does not recognize the proposed fix type.';
    }
    return null;
  }
}

/// One run of the agent's investigation, for the read-only audit timeline
/// (`GET /api/admin/agent-runs/{id}` → `run`).
class AgentRun {
  final int id;
  final int issueId;
  final String trigger;
  final String status;
  final String model;
  final int stepCount;
  final int inputTokens;
  final int outputTokens;
  final int cacheCreationTokens;
  final int cacheReadTokens;
  final int costMicros;
  final String? stopReason;
  final DateTime? startedAt;
  final DateTime? finishedAt;

  const AgentRun({
    required this.id,
    required this.issueId,
    required this.trigger,
    required this.status,
    required this.model,
    required this.stepCount,
    required this.inputTokens,
    required this.outputTokens,
    required this.cacheCreationTokens,
    required this.cacheReadTokens,
    required this.costMicros,
    required this.stopReason,
    required this.startedAt,
    required this.finishedAt,
  });

  /// The accumulated run cost as a short "$0.0123" string (micros → USD).
  String get costLabel {
    final usd = costMicros / 1000000.0;
    return '\$${usd.toStringAsFixed(usd < 0.01 ? 4 : 2)}';
  }

  String get statusLabel => switch (status) {
        'running' => 'Investigation in progress',
        'succeeded' || 'completed' => 'Investigation completed',
        'failed' => 'Investigation failed',
        'gave_up' => 'Investigation stopped without a fix',
        'waiting_user' => 'Waiting for a reply',
        'waiting_approval' => 'Waiting for fix review',
        'resume_pending' => 'Ready to continue after a reply or decision',
        'aborted' => 'Investigation stopped when the issue closed',
        _ => 'Investigation status unknown',
      };

  String? get stopReasonLabel => switch (stopReason) {
        null || '' => null,
        'resolved' => 'Resolution verified',
        'max_steps' => 'Reached the investigation step limit',
        'timeout' => 'Reached the investigation time limit',
        'max_cost' => 'Reached the investigation cost limit',
        'model_error' => 'The AI provider returned an error',
        'no_diagnosis' => 'No reliable diagnosis was found',
        'awaiting_approval' => 'Waiting for an admin to review a fix',
        'awaiting_user' => 'Waiting for the reporter to reply',
        'user_unresponsive' => 'Closed after no reply',
        'external_resolution' => 'The issue changed outside this run',
        'admin_dismissed' => 'Dismissed by an admin',
        'admin_completed' => 'Completed after admin review',
        'issue_closed' => 'Stopped because the issue closed',
        'server_restarted' => 'Interrupted by a server restart',
        'action_outcome_unknown' =>
          'Stopped because an approved action needs manual verification',
        _ => 'Stopped for an unrecognized reason',
      };

  factory AgentRun.fromJson(Map<String, dynamic> json) => AgentRun(
        id: (json['id'] as num?)?.toInt() ?? 0,
        issueId: (json['issue_id'] as num?)?.toInt() ?? 0,
        trigger: json['trigger'] as String? ?? '',
        status: json['status'] as String? ?? '',
        model: json['model'] as String? ?? '',
        stepCount: (json['step_count'] as num?)?.toInt() ?? 0,
        inputTokens: (json['input_tokens'] as num?)?.toInt() ?? 0,
        outputTokens: (json['output_tokens'] as num?)?.toInt() ?? 0,
        cacheCreationTokens:
            (json['cache_creation_tokens'] as num?)?.toInt() ?? 0,
        cacheReadTokens: (json['cache_read_tokens'] as num?)?.toInt() ?? 0,
        costMicros: (json['cost_micros'] as num?)?.toInt() ?? 0,
        stopReason: json['stop_reason'] as String?,
        startedAt:
            DateTime.tryParse(json['started_at'] as String? ?? '')?.toLocal(),
        finishedAt:
            DateTime.tryParse(json['finished_at'] as String? ?? '')?.toLocal(),
      );
}

/// One step of the agent's audit ledger (`agent-runs/{id}` → `steps[]`). All
/// text fields (`text`, `toolInput`, `toolOutput`) are UNTRUSTED — rendered as
/// passive, truncated text in the timeline.
class AgentStep {
  final int id;
  final int seq;

  /// 'assistant' | 'tool_call' | 'tool_result' | 'system' | 'giveup'.
  final String kind;
  final String? toolName;
  final String? toolInput;
  final String? toolOutput;
  final String? text;
  final bool isError;
  final DateTime? createdAt;

  const AgentStep({
    required this.id,
    required this.seq,
    required this.kind,
    required this.toolName,
    required this.toolInput,
    required this.toolOutput,
    required this.text,
    required this.isError,
    required this.createdAt,
  });

  factory AgentStep.fromJson(Map<String, dynamic> json) => AgentStep(
        id: (json['id'] as num?)?.toInt() ?? 0,
        seq: (json['seq'] as num?)?.toInt() ?? 0,
        kind: json['kind'] as String? ?? '',
        toolName: json['tool_name'] as String?,
        toolInput: json['tool_input'] as String?,
        toolOutput: json['tool_output'] as String?,
        text: json['text'] as String?,
        isError: json['is_error'] as bool? ?? false,
        createdAt:
            DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal(),
      );
}

/// The `GET /api/admin/agent-runs/{id}` payload: a run plus its ordered steps.
class AgentRunDetail {
  final AgentRun run;
  final List<AgentStep> steps;

  const AgentRunDetail({required this.run, required this.steps});

  factory AgentRunDetail.fromJson(Map<String, dynamic> json) => AgentRunDetail(
        run:
            AgentRun.fromJson(json['run'] as Map<String, dynamic>? ?? const {}),
        steps: ((json['steps'] as List?) ?? const [])
            .map((e) => AgentStep.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// Durable activity for one issue. It includes every action status and compact
/// run summaries, unlike the transient approval queue.
class IssueAgentActivity {
  final List<AgentAction> actions;
  final List<AgentRun> runs;

  const IssueAgentActivity({required this.actions, required this.runs});

  factory IssueAgentActivity.fromJson(Map<String, dynamic> json) =>
      IssueAgentActivity(
        actions: ((json['actions'] as List?) ?? const [])
            .whereType<Map>()
            .map((e) => AgentAction.fromJson(
                  e.map((k, v) => MapEntry(k.toString(), v)),
                ))
            .toList(),
        runs: ((json['runs'] as List?) ?? const [])
            .whereType<Map>()
            .map((e) => AgentRun.fromJson(
                  e.map((k, v) => MapEntry(k.toString(), v)),
                ))
            .toList(),
      );
}
