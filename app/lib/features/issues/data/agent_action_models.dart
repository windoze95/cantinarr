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
  deleteMediaFiles('delete_media_files'),
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

/// The server's typed invitation to arm a standing auto-approval rule from a
/// decidable proposal. Every field is SERVER-AUTHORED (doctor problem labels +
/// fixed action vocabulary) — the client renders it verbatim and never derives
/// eligibility itself. Absent on old servers and on ineligible proposals.
class AutoApprovalOffer {
  final String problemKind;
  final String actionKind;
  final String actionFacet;

  /// Fixed display label, e.g. "Manual import · Waiting to import".
  final String label;

  /// True when checking the box re-arms an existing paused rule instead of
  /// creating a new one.
  final bool reactivatesPausedRule;

  const AutoApprovalOffer({
    required this.problemKind,
    required this.actionKind,
    required this.actionFacet,
    required this.label,
    required this.reactivatesPausedRule,
  });

  static AutoApprovalOffer? fromJson(Object? json) {
    if (json is! Map<String, dynamic>) return null;
    final label = json['label'] as String? ?? '';
    if (label.isEmpty) return null;
    return AutoApprovalOffer(
      problemKind: json['problem_kind'] as String? ?? '',
      actionKind: json['action_kind'] as String? ?? '',
      actionFacet: json['action_facet'] as String? ?? '',
      label: label,
      reactivatesPausedRule: json['reactivates_paused_rule'] as bool? ?? false,
    );
  }
}

/// One admin-approvable proposed mutation, as returned by
/// `GET /api/admin/agent-actions`. The reified [params] are parsed into a
/// small read-only [AgentActionParams] view for plain-language rendering; the
/// raw map is retained so an unknown kind still shows its data.
/// One executed, download-attributed fix on the same issue — the server's own
/// remediation memory, rendered for the approving human so they never decide
/// with less evidence than the model. [recurred] means the arr re-added the
/// SAME download after the fix ran: the fix did not hold.
class PriorAttempt {
  final String kind;
  final String facet;
  final DateTime? executedAt;
  final bool recurred;

  const PriorAttempt({
    required this.kind,
    this.facet = '',
    this.executedAt,
    this.recurred = false,
  });

  factory PriorAttempt.fromJson(Map<String, dynamic> json) => PriorAttempt(
        kind: json['kind'] as String? ?? '',
        facet: json['facet'] as String? ?? '',
        executedAt: DateTime.tryParse(json['executed_at'] as String? ?? ''),
        recurred: json['recurred'] as bool? ?? false,
      );
}

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
  final int issueTmdbId;
  final int issueSeason;
  final int issueEpisode;
  final int issueOccurrences;
  final List<PriorAttempt> priorAttempts;
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

  /// Standing-rule attribution: non-null [autoRuleId] (and [autoApproved])
  /// mean a rule — not an admin — made this decision; [decidedBy] is null
  /// then. [autoRuleLabel] is null when that rule was later deleted. All
  /// absent on old servers, parsed leniently to false/null.
  final int? autoRuleId;
  final bool autoApproved;
  final String? autoRuleLabel;

  /// Present only when the server computed that approving this proposal could
  /// arm (or re-arm) a standing rule. Display data only: it never loosens or
  /// tightens the decision gate.
  final AutoApprovalOffer? autoApprovalOffer;

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
    this.issueTmdbId = 0,
    this.issueSeason = 0,
    this.issueEpisode = 0,
    this.issueOccurrences = 0,
    this.priorAttempts = const [],
    required this.issueCategory,
    required this.issueStatus,
    required this.issueClosedAt,
    required this.instanceId,
    required this.instanceName,
    required this.instanceServiceType,
    required this.canDecide,
    required this.blockedReason,
    this.autoRuleId,
    this.autoApproved = false,
    this.autoRuleLabel,
    this.autoApprovalOffer,
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
      issueTmdbId: (json['issue_tmdb_id'] as num?)?.toInt() ?? 0,
      issueSeason: (json['issue_season'] as num?)?.toInt() ?? 0,
      issueEpisode: (json['issue_episode'] as num?)?.toInt() ?? 0,
      issueOccurrences: (json['issue_occurrences'] as num?)?.toInt() ?? 0,
      priorAttempts: ((json['prior_attempts'] as List?) ?? const [])
          .whereType<Map<String, dynamic>>()
          .map(PriorAttempt.fromJson)
          .toList(),
      issueCategory: json['issue_category'] as String?,
      issueStatus: IssueStatus.fromValue(json['issue_status'] as String?),
      issueClosedAt: DateTime.tryParse(json['issue_closed_at'] as String? ?? '')
          ?.toLocal(),
      instanceId: json['instance_id'] as String? ?? '',
      instanceName: json['instance_name'] as String? ?? '',
      instanceServiceType: json['instance_service_type'] as String? ?? '',
      canDecide: json['can_decide'] as bool? ?? false,
      blockedReason: json['blocked_reason'] as String? ?? '',
      autoRuleId: (json['auto_rule_id'] as num?)?.toInt(),
      autoApproved: json['auto_approved'] as bool? ?? false,
      autoRuleLabel: json['auto_rule_label'] as String?,
      autoApprovalOffer: AutoApprovalOffer.fromJson(json['auto_approval_offer']),
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
      'music' => 'lidarr',
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
        'lidarr' => 'Lidarr',
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

/// The most episodes one delete_media_files proposal may cover. Mirrors
/// `maxDeleteEpisodes` in `server/internal/remediation/actions.go`: a wrong-file
/// report is one season-shaped incident, and past this the approval card stops
/// being something an admin can actually read before authorising a deletion.
const int _maxDeleteEpisodes = 60;

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

  /// True when [key] holds a JSON array whose every member is a whole number.
  /// A numeric-looking string is rejected, matching the server's strict decode:
  /// an episode list is the target of an irreversible deletion, so it is never
  /// coerced.
  bool _isIntList(String key, {bool optional = false}) {
    if (!_raw.containsKey(key)) return optional;
    final value = _raw[key];
    if (value is! List) return false;
    return value.every((v) => v is num && v.isFinite && v == v.toInt());
  }

  String? get mediaType => _str('media_type');

  /// grab_release: the server-issued one-way release reference.
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

  /// remediate_queue: remove | blocklist_search | blocklist_only | change_category.
  String? get queueAction => _str('action');

  /// manual_import: whether to force past arr's safety checks.
  bool get force => _bool('force');

  /// trigger_search / rescan: the media id and optional TV episode scope.
  int? get tmdbId => _int('tmdb_id');
  int? get season {
    final v = _int('season');
    // An explicitly supplied zero is Sonarr's Specials season. Missing and
    // negative values remain invalid, but S00 must survive parsing so an exact
    // special does not become an undecidable proposal in the app.
    return (v != null && v >= 0) ? v : null;
  }

  int? get episode {
    final v = _int('episode');
    return (v != null && v > 0) ? v : null;
  }

  /// delete_media_files: the exact episode numbers whose already-imported files
  /// would be deleted. Only whole numbers survive parsing; [validationProblem]
  /// refuses the whole proposal when the list holds anything else, so a
  /// partially parsed list can never become the target of a deletion.
  List<int> get episodes {
    final value = _raw['episodes'];
    if (value is! List) return const [];
    return value
        .whereType<num>()
        .where((n) => n.isFinite && n == n.toInt())
        .map((n) => n.toInt())
        .toList(growable: false);
  }

  /// delete_media_files: whether the releases that delivered the deleted files
  /// are also blocked, so the same ones are not downloaded again.
  bool get blocklist => _bool('blocklist');

  int? get authorId => _int('author_id');
  int? get bookId => _int('book_id');
  int? get artistId => _int('artist_id');
  int? get albumId => _int('album_id');

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
      // No aired-only variant: replacing what a bad import destroyed is part of
      // delete_media_files, not a search of its own. A proposal still carrying
      // `aired_only` is therefore an unrecognized field, and falls out below.
      AgentActionKind.triggerSearch => const {
          'media_type',
          'tmdb_id',
          'season',
          'episode',
          'author_id',
          'book_id',
          'artist_id',
          'album_id',
        },
      AgentActionKind.rescan => const {
          'media_type',
          'tmdb_id',
          'author_id',
          'artist_id',
        },
      AgentActionKind.deleteMediaFiles => const {
          'media_type',
          'tmdb_id',
          'season',
          'episodes',
          'blocklist',
          'book_id',
          'album_id',
        },
      AgentActionKind.unknown => const <String>{},
    };
    if (_raw.keys.any((key) => !allowed.contains(key))) {
      return 'The proposed fix contains fields this app does not recognize.';
    }

    final media = mediaType;
    if (_raw['media_type'] is! String ||
        (media != 'movie' &&
            media != 'tv' &&
            media != 'book' &&
            media != 'music')) {
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
              'blocklist_only',
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
            !_isInt('book_id', optional: true) ||
            !_isInt('artist_id', optional: true) ||
            !_isInt('album_id', optional: true)) {
          return 'The proposed search details are malformed.';
        }
        if (media == 'book') {
          if ((authorId ?? 0) <= 0 && (bookId ?? 0) <= 0) {
            return 'The book or author needed for this search is missing.';
          }
          if ((tmdbId ?? 0) != 0 ||
              (artistId ?? 0) != 0 ||
              (albumId ?? 0) != 0 ||
              _raw.containsKey('season') ||
              _raw.containsKey('episode')) {
            return 'The proposed book search contains invalid media details.';
          }
        } else if (media == 'music') {
          if ((artistId ?? 0) <= 0 && (albumId ?? 0) <= 0) {
            return 'The album or artist needed for this search is missing.';
          }
          if ((tmdbId ?? 0) != 0 ||
              (authorId ?? 0) != 0 ||
              (bookId ?? 0) != 0 ||
              _raw.containsKey('season') ||
              _raw.containsKey('episode')) {
            return 'The proposed music search contains invalid media details.';
          }
        } else {
          if ((artistId ?? 0) != 0 || (albumId ?? 0) != 0) {
            return 'The proposed search contains music details.';
          }
          if ((tmdbId ?? 0) <= 0) {
            return 'The title needed for this search is missing.';
          }
          if (media == 'movie' &&
              (_raw.containsKey('season') || _raw.containsKey('episode'))) {
            return 'The proposed movie search contains TV episode details.';
          }
          if (_raw.containsKey('season') && season == null) {
            return 'The proposed TV season is invalid.';
          }
          if (_raw.containsKey('episode') &&
              ((episode ?? 0) <= 0 || season == null)) {
            return 'The proposed episode search is missing its season or episode.';
          }
        }
      case AgentActionKind.rescan:
        if (!_isInt('tmdb_id', optional: true) ||
            !_isInt('author_id', optional: true) ||
            !_isInt('artist_id', optional: true)) {
          return 'The proposed rescan details are malformed.';
        }
        if (media == 'book') {
          if ((authorId ?? 0) <= 0 ||
              (tmdbId ?? 0) != 0 ||
              (artistId ?? 0) != 0) {
            return 'The author needed for this rescan is missing.';
          }
        } else if (media == 'music') {
          if ((artistId ?? 0) <= 0 ||
              (tmdbId ?? 0) != 0 ||
              (authorId ?? 0) != 0) {
            return 'The artist needed for this rescan is missing.';
          }
        } else if ((tmdbId ?? 0) <= 0 ||
            (authorId ?? 0) != 0 ||
            (artistId ?? 0) != 0) {
          return 'The title needed for this rescan is missing.';
        }
      case AgentActionKind.deleteMediaFiles:
        // Deleting is irreversible, so every part of the target is required and
        // strictly typed here — an under-specified proposal stays readable as
        // history but must never reach an Approve button.
        if (_raw.containsKey('blocklist') && _raw['blocklist'] is! bool) {
          return 'The proposed deletion options are malformed.';
        }
        if (media == 'book') {
          // A book delete addresses the durable Chaptarr record id; book_id
          // and the blocklist choice are the entire target.
          if (!_isInt('book_id') || (bookId ?? 0) <= 0) {
            return 'The book whose files would be deleted is missing.';
          }
          if ((tmdbId ?? 0) != 0 ||
              (albumId ?? 0) != 0 ||
              _raw.containsKey('season') ||
              _raw.containsKey('episodes')) {
            return 'The proposed book deletion contains invalid media details.';
          }
        } else if (media == 'music') {
          // The wrong-album repair mirrors the book one over the durable
          // Lidarr record id; album_id and blocklist are the entire target.
          if (!_isInt('album_id') || (albumId ?? 0) <= 0) {
            return 'The album whose files would be deleted is missing.';
          }
          if ((tmdbId ?? 0) != 0 ||
              (bookId ?? 0) != 0 ||
              _raw.containsKey('season') ||
              _raw.containsKey('episodes')) {
            return 'The proposed music deletion contains invalid media details.';
          }
        } else {
          if (_raw.containsKey('book_id')) {
            return 'The proposed deletion contains book details.';
          }
          if (_raw.containsKey('album_id')) {
            return 'The proposed deletion contains music details.';
          }
          if (!_isInt('tmdb_id') || (tmdbId ?? 0) <= 0) {
            return 'The title whose files would be deleted is missing.';
          }
          if (media == 'movie') {
            if (_raw.containsKey('season') || _raw.containsKey('episodes')) {
              return 'The proposed movie deletion contains TV episode details.';
            }
          } else {
            if (!_isInt('season') || season == null) {
              return 'The proposed TV season is invalid.';
            }
            if (!_isIntList('episodes', optional: true)) {
              return 'The list of episodes to delete is malformed.';
            }
            final numbers = episodes;
            if (numbers.isEmpty) {
              return 'The episodes whose files would be deleted are missing.';
            }
            if (numbers.any((n) => n <= 0)) {
              return 'The proposed episode numbers are invalid.';
            }
            if (numbers.length > _maxDeleteEpisodes) {
              return 'The proposed deletion covers too many episodes to review.';
            }
          }
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
    required this.stopReason,
    required this.startedAt,
    required this.finishedAt,
  });

  String get statusLabel => switch (status) {
        'running' => 'Investigation in progress',
        'succeeded' || 'completed' => 'Investigation completed',
        'failed' => 'Investigation failed',
        'gave_up' => 'Investigation stopped without a fix',
        'waiting_user' => 'Waiting for a reply',
        'waiting_approval' => 'Waiting for fix review',
        'resume_pending' => 'Ready to continue after a reply or decision',
        'aborted' => 'Investigation stopped',
        _ => 'Investigation status unknown',
      };

  String? get stopReasonLabel => switch (stopReason) {
        null || '' => null,
        'resolved' => 'Resolution verified',
        'max_steps' => 'Reached the investigation step limit',
        'timeout' => 'Reached the investigation time limit',
        'max_cost' => 'Reached the investigation limit',
        'model_error' => 'The AI provider returned an error',
        'infrastructure_error' => 'The investigation service returned an error',
        'no_diagnosis' => 'No reliable diagnosis was found',
        'unverified_conclusion' =>
          'The proposed resolution could not be verified',
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
        'arr_recovery_in_flight' =>
          'Stopped because the media service resumed download recovery',
        'media_state_changed' => 'Stopped because the live media state changed',
        'recovery_preflight_failed' =>
          'Stopped because the latest media state could not be verified',
        'unresumable_transcript' =>
          'Stopped because the saved investigation could not be resumed',
        'legacy_release_metadata' =>
          'Stopped because a legacy fix lacked verified release details',
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

/// One item's verdict from the approve-batch endpoint: the durable action
/// status after the attempt (`executed`, `failed`, `outcome_unknown`,
/// `superseded`, ...) or one of two batch-only verdicts — `skipped` (a
/// decision or arr-recovery race owned the proposal first; nothing executed)
/// and `error` (the id could not be decided at all). [detail] carries the
/// server's reason for anything that did not execute cleanly.
class AgentActionBatchResult {
  final int id;
  final String status;
  final String detail;

  const AgentActionBatchResult({
    required this.id,
    required this.status,
    this.detail = '',
  });

  factory AgentActionBatchResult.fromJson(Map<String, dynamic> json) =>
      AgentActionBatchResult(
        id: (json['id'] as num?)?.toInt() ?? 0,
        status: json['status'] as String? ?? '',
        detail: json['detail'] as String? ?? '',
      );

  /// The fix ran and completed cleanly.
  bool get applied => status == 'executed';

  /// Nothing ran and nothing needs an admin: the proposal was owned by a
  /// concurrent decision, the arr's own recovery, or a superseding change.
  bool get skipped =>
      status == 'skipped' || status == 'superseded' || status == 'executing';

  /// Everything else — failed, outcome unknown, undecidable — deserves the
  /// admin's eye on the queue/history view.
  bool get needsAttention => !applied && !skipped;
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
