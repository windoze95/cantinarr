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
  unknown('', 'Unknown');

  const AgentActionStatus(this.value, this.label);

  final String value;
  final String label;

  /// True only while the action can still be approved or denied. Every other
  /// state freezes the card (the server's CAS rejects a second decision anyway).
  bool get isPending => this == AgentActionStatus.proposed;

  /// True for a state reached by an admin decision (drives the "Approved · just
  /// now" / "Denied" frozen footer).
  bool get isDecided =>
      this == AgentActionStatus.approved ||
      this == AgentActionStatus.executing ||
      this == AgentActionStatus.executed ||
      this == AgentActionStatus.denied ||
      this == AgentActionStatus.failed;

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

  const AgentAction({
    required this.id,
    required this.issueId,
    required this.runId,
    required this.kind,
    required this.kindRaw,
    required this.params,
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
  });

  factory AgentAction.fromJson(Map<String, dynamic> json) {
    // `params` arrives as a JSON object; tolerate a stringified object or a
    // missing/non-object value so a malformed proposal never crashes the queue.
    final rawParams = json['params'];
    Map<String, dynamic> paramsMap = const {};
    if (rawParams is Map) {
      paramsMap = rawParams.map((k, v) => MapEntry(k.toString(), v));
    } else if (rawParams is String && rawParams.isNotEmpty) {
      try {
        final decoded = jsonDecode(rawParams);
        if (decoded is Map) {
          paramsMap = decoded.map((k, v) => MapEntry(k.toString(), v));
        }
      } catch (_) {
        // Leave params empty; the card still renders kind + rationale.
      }
    }

    return AgentAction(
      id: (json['id'] as num?)?.toInt() ?? 0,
      issueId: (json['issue_id'] as num?)?.toInt() ?? 0,
      runId: (json['run_id'] as num?)?.toInt(),
      kind: AgentActionKind.fromValue(json['kind'] as String?),
      kindRaw: json['kind'] as String? ?? '',
      params: AgentActionParams(paramsMap),
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
    );
  }
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

  String? get mediaType => _str('media_type');

  /// grab_release: the release GUID (an opaque indexer id, shown truncated).
  String? get guid => _str('guid');
  int? get indexerId => _int('indexer_id');
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

  /// trigger_search / rescan: the media id and optional season.
  int? get tmdbId => _int('tmdb_id');
  int? get season {
    final v = _int('season');
    return (v != null && v > 0) ? v : null;
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
        run: AgentRun.fromJson(json['run'] as Map<String, dynamic>? ?? const {}),
        steps: ((json['steps'] as List?) ?? const [])
            .map((e) => AgentStep.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}
