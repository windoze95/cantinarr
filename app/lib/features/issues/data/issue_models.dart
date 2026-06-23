// Data models for the AI-remediation issue-reporting feature.
//
// These mirror the Wave-1 REST contract (snake_case JSON) exactly. All
// free-text fields the server marks UNTRUSTED (an issue's `detail`, a
// message `body`) are carried verbatim and rendered as passive text only.

/// A category a reporter may pick for a problem. The string [value] is the
/// wire value sent to the server; [label] is the user-facing choice text.
enum IssueCategory {
  wrongContent('wrong_content', 'Wrong episode/movie'),
  badCopy('bad_copy', 'Bad / corrupt copy'),
  wrongAudio('wrong_audio', 'Wrong or missing audio language'),
  other('other', 'Something else');

  const IssueCategory(this.value, this.label);

  /// The snake_case value sent to / received from the server.
  final String value;

  /// The user-facing label shown on the report sheet and issue chips.
  final String label;

  /// True for the catch-all category, which requires a free-text reason.
  bool get requiresReason => this == IssueCategory.other;

  /// Resolves a wire value back to a category, defaulting to [other] for an
  /// unknown string so the UI never crashes on a future server category.
  static IssueCategory fromValue(String? value) => values.firstWhere(
        (c) => c.value == value,
        orElse: () => IssueCategory.other,
      );
}

/// The lifecycle state of an issue. Terminal states are
/// resolved/wont_fix/failed/dismissed. The enum is tolerant of unknown
/// server values (mapped to [unknown]) so a future status can't break parsing.
enum IssueStatus {
  open('open', 'Open'),
  investigating('investigating', 'Investigating'),
  awaitingUser('awaiting_user', 'Needs your reply'),
  awaitingApproval('awaiting_approval', 'Awaiting approval'),
  resolved('resolved', 'Resolved'),
  wontFix('wont_fix', "Won't fix"),
  failed('failed', 'Failed'),
  dismissed('dismissed', 'Dismissed'),
  unknown('', 'Unknown');

  const IssueStatus(this.value, this.label);

  final String value;
  final String label;

  /// True once the issue has reached a state that accepts no further work.
  bool get isTerminal =>
      this == resolved ||
      this == wontFix ||
      this == failed ||
      this == dismissed;

  /// True while the issue is actively being worked (drives a typing/poll hint).
  bool get isActive => this == open || this == investigating;

  static IssueStatus fromValue(String? value) => values.firstWhere(
        (s) => s.value == value,
        orElse: () => IssueStatus.unknown,
      );
}

/// One reported (or auto-detected) problem. Media-scoped like a media request
/// (tmdb_id + media_type), optionally narrowed to a TV season/episode (0 means
/// "whole series" / not applicable).
class Issue {
  final int id;
  final String source; // 'auto' | 'user'
  final IssueStatus status;
  final IssueCategory? category; // null for auto-detected
  final int? reporterId;
  final String reporterName;
  final int tmdbId;
  final String mediaType; // 'movie' | 'tv'
  final String title;
  final int seasonNumber; // 0 = whole series / movie
  final int episodeNumber; // 0 = whole season / movie
  final String detail; // UNTRUSTED free text — render passively
  final int occurrences;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const Issue({
    required this.id,
    required this.source,
    required this.status,
    required this.category,
    required this.reporterId,
    required this.reporterName,
    required this.tmdbId,
    required this.mediaType,
    required this.title,
    required this.seasonNumber,
    required this.episodeNumber,
    required this.detail,
    required this.occurrences,
    required this.createdAt,
    required this.updatedAt,
  });

  bool get isTv => mediaType == 'tv';

  /// A short scope label for list/thread subtitles:
  /// "S2·E4" / "Season 2" / "Movie".
  String get scopeLabel {
    if (!isTv) return 'Movie';
    if (seasonNumber <= 0) return 'Series';
    if (episodeNumber <= 0) return 'Season $seasonNumber';
    return 'S$seasonNumber·E$episodeNumber';
  }

  factory Issue.fromJson(Map<String, dynamic> json) => Issue(
        id: json['id'] as int? ?? 0,
        source: json['source'] as String? ?? 'user',
        status: IssueStatus.fromValue(json['status'] as String?),
        category: json['category'] == null
            ? null
            : IssueCategory.fromValue(json['category'] as String?),
        reporterId: (json['reporter_id'] as num?)?.toInt(),
        reporterName: json['reporter_name'] as String? ?? '',
        tmdbId: json['tmdb_id'] as int? ?? 0,
        mediaType: json['media_type'] as String? ?? 'movie',
        title: json['title'] as String? ?? '',
        seasonNumber: json['season_number'] as int? ?? 0,
        episodeNumber: json['episode_number'] as int? ?? 0,
        detail: json['detail'] as String? ?? '',
        occurrences: json['occurrences'] as int? ?? 1,
        createdAt:
            DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal(),
        updatedAt:
            DateTime.tryParse(json['updated_at'] as String? ?? '')?.toLocal(),
      );
}

/// Who authored a thread message. Provenance is load-bearing: the UI renders
/// a `user`/`admin` report on the right, an `agent`/`system` message on the
/// left/centered, and never treats any body as a control.
enum IssueAuthorKind {
  user('user'),
  agent('agent'),
  admin('admin'),
  system('system'),
  unknown('');

  const IssueAuthorKind(this.value);
  final String value;

  static IssueAuthorKind fromValue(String? value) => values.firstWhere(
        (k) => k.value == value,
        orElse: () => IssueAuthorKind.unknown,
      );
}

/// One append-only message in an issue thread. [body] is UNTRUSTED when
/// [authorKind] is `user` — it is always rendered as passive text.
class IssueMessage {
  final int id;
  final IssueAuthorKind authorKind;
  final String authorName;
  final String body;
  final DateTime? createdAt;

  const IssueMessage({
    required this.id,
    required this.authorKind,
    required this.authorName,
    required this.body,
    required this.createdAt,
  });

  /// True for messages that originate from the reporter/admin side of the
  /// conversation (rendered as a right-aligned bubble).
  bool get isFromHuman =>
      authorKind == IssueAuthorKind.user ||
      authorKind == IssueAuthorKind.admin;

  factory IssueMessage.fromJson(Map<String, dynamic> json) => IssueMessage(
        id: json['id'] as int? ?? 0,
        authorKind: IssueAuthorKind.fromValue(json['author_kind'] as String?),
        authorName: json['author_name'] as String? ?? '',
        body: json['body'] as String? ?? '',
        createdAt:
            DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal(),
      );
}

/// An issue plus its full thread, as returned by `GET /api/issues/{id}`.
class IssueThread {
  final Issue issue;
  final List<IssueMessage> messages;

  const IssueThread({required this.issue, required this.messages});

  factory IssueThread.fromJson(Map<String, dynamic> json) => IssueThread(
        issue:
            Issue.fromJson(json['issue'] as Map<String, dynamic>? ?? const {}),
        messages: ((json['thread'] as List?) ?? const [])
            .map((e) => IssueMessage.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// The agent's autonomy tier, shown as a dropdown in admin settings.
enum RemediationAutonomy {
  investigateOnly('investigate_only', 'Investigate only'),
  propose('propose', 'Propose fixes'),
  autoSafe('auto_safe', 'Auto-fix low-risk');

  const RemediationAutonomy(this.value, this.label);
  final String value;
  final String label;

  static RemediationAutonomy fromValue(String? value) => values.firstWhere(
        (a) => a.value == value,
        orElse: () => RemediationAutonomy.propose,
      );
}

/// The admin-tunable remediation settings, GET/PUT
/// `/api/admin/remediation-settings`. Field names mirror the server's
/// `Settings` struct exactly.
class RemediationSettings {
  final bool enabled;
  final bool autoDispatch;
  final bool allowReporting;
  final RemediationAutonomy autonomy;

  /// AI provider override (e.g. "anthropic", "openai"). Empty means "use the
  /// server's configured provider". The agent is provider-agnostic, so this is
  /// a free-text field, never a fixed list.
  final String provider;

  /// Model override. Empty means "use the server's configured model".
  final String model;
  final int maxSteps;
  final int maxTurnTokens;
  final int maxWallClockSecs;
  final int maxCostMicros;
  final int dailyRunCap;
  final int dailyCostCeilingMicros;
  final int circuitBreakerGiveups;

  const RemediationSettings({
    required this.enabled,
    required this.autoDispatch,
    required this.allowReporting,
    required this.autonomy,
    required this.provider,
    required this.model,
    required this.maxSteps,
    required this.maxTurnTokens,
    required this.maxWallClockSecs,
    required this.maxCostMicros,
    required this.dailyRunCap,
    required this.dailyCostCeilingMicros,
    required this.circuitBreakerGiveups,
  });

  factory RemediationSettings.fromJson(Map<String, dynamic> json) =>
      RemediationSettings(
        enabled: json['enabled'] as bool? ?? false,
        autoDispatch: json['auto_dispatch'] as bool? ?? false,
        allowReporting: json['allow_reporting'] as bool? ?? false,
        autonomy: RemediationAutonomy.fromValue(json['autonomy'] as String?),
        provider: json['provider'] as String? ?? '',
        model: json['model'] as String? ?? '',
        maxSteps: json['max_steps'] as int? ?? 0,
        maxTurnTokens: json['max_turn_tokens'] as int? ?? 0,
        maxWallClockSecs: json['max_wall_clock_secs'] as int? ?? 0,
        maxCostMicros: json['max_cost_micros'] as int? ?? 0,
        dailyRunCap: json['daily_run_cap'] as int? ?? 0,
        dailyCostCeilingMicros: json['daily_cost_ceiling_micros'] as int? ?? 0,
        circuitBreakerGiveups: json['circuit_breaker_giveups'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'enabled': enabled,
        'auto_dispatch': autoDispatch,
        'allow_reporting': allowReporting,
        'autonomy': autonomy.value,
        'provider': provider,
        'model': model,
        'max_steps': maxSteps,
        'max_turn_tokens': maxTurnTokens,
        'max_wall_clock_secs': maxWallClockSecs,
        'max_cost_micros': maxCostMicros,
        'daily_run_cap': dailyRunCap,
        'daily_cost_ceiling_micros': dailyCostCeilingMicros,
        'circuit_breaker_giveups': circuitBreakerGiveups,
      };

  RemediationSettings copyWith({
    bool? enabled,
    bool? autoDispatch,
    bool? allowReporting,
    RemediationAutonomy? autonomy,
    String? provider,
    String? model,
    int? maxSteps,
    int? maxTurnTokens,
    int? maxWallClockSecs,
    int? maxCostMicros,
    int? dailyRunCap,
    int? dailyCostCeilingMicros,
    int? circuitBreakerGiveups,
  }) =>
      RemediationSettings(
        enabled: enabled ?? this.enabled,
        autoDispatch: autoDispatch ?? this.autoDispatch,
        allowReporting: allowReporting ?? this.allowReporting,
        autonomy: autonomy ?? this.autonomy,
        provider: provider ?? this.provider,
        model: model ?? this.model,
        maxSteps: maxSteps ?? this.maxSteps,
        maxTurnTokens: maxTurnTokens ?? this.maxTurnTokens,
        maxWallClockSecs: maxWallClockSecs ?? this.maxWallClockSecs,
        maxCostMicros: maxCostMicros ?? this.maxCostMicros,
        dailyRunCap: dailyRunCap ?? this.dailyRunCap,
        dailyCostCeilingMicros:
            dailyCostCeilingMicros ?? this.dailyCostCeilingMicros,
        circuitBreakerGiveups:
            circuitBreakerGiveups ?? this.circuitBreakerGiveups,
      );
}
