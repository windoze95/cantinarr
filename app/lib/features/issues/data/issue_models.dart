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
  open('open', 'Reported'),
  observing('observing', 'Watching the download'),
  recovering('recovering', 'Download recovery in progress'),
  investigating('investigating', 'Checking the problem'),
  awaitingUser('awaiting_user', 'Needs your reply'),
  awaitingApproval('awaiting_approval', 'Fix ready for review'),
  needsAdmin('needs_admin', 'Needs a closer look'),
  resolved('resolved', 'Resolved'),
  wontFix('wont_fix', 'Closed without a fix'),
  failed('failed', 'Could not resolve'),
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

  /// True while Cantinarr is passively tracking the arr's own recovery work.
  /// These issues remain open for audit/history, but must not be presented as
  /// agent or admin work that needs attention.
  bool get isTracking => this == observing || this == recovering;

  /// True when an open issue belongs in the admin's attention queue.
  bool get needsAttention => !isTerminal && !isTracking;

  /// True while the issue is actively being worked (drives a typing/poll hint).
  bool get isActive => this == open || this == investigating;

  static IssueStatus fromValue(String? value) => values.firstWhere(
        (s) => s.value == value,
        orElse: () => IssueStatus.unknown,
      );
}

/// Provenance for a terminal issue's resolution. The free-text `resolution`
/// remains passive detail; this enum provides fixed, app-authored copy that
/// makes it clear whether the agent acted or the arr state changed elsewhere.
enum IssueResolutionKind {
  agentConcluded('agent_concluded', 'Review completed'),
  arrStateCleared('arr_state_cleared', 'Media became available'),
  reporterTimeout('reporter_timeout', 'Closed after no reply'),
  adminDismissed('admin_dismissed', 'Closed after review'),
  adminCompleted('admin_completed', 'Completed after review'),
  aiHealthRestored('ai_health_restored', 'Shared AI recovered'),
  legacyUnknown('legacy_unknown', 'How it closed is unknown'),
  unknown('', 'How it closed is unknown');

  const IssueResolutionKind(this.value, this.label);

  final String value;
  final String label;

  static IssueResolutionKind fromValue(String? value) => values.firstWhere(
        (k) => k.value == value,
        orElse: () => IssueResolutionKind.unknown,
      );
}

/// Explicit human judgment for the admin completion endpoint. Dismissal is not
/// a disposition here; it remains a separate action and audit provenance.
enum AdminIssueDisposition {
  resolved('resolved'),
  wontFix('wont_fix');

  const AdminIssueDisposition(this.value);
  final String value;
}

/// One reported (or auto-detected) problem. Media-scoped like a media request
/// (tmdb_id + media_type), optionally narrowed to a TV season/episode (0 means
/// "whole series" / not applicable).
class Issue {
  final int id;
  final String source; // 'auto' | 'user' | 'system'
  final IssueStatus status;
  final IssueCategory? category; // null for auto-detected
  final int? reporterId;
  final String reporterName;
  final String instanceId; // exact owning Radarr/Sonarr instance
  final int tmdbId;
  final String mediaType; // 'movie' | 'tv' | 'system'
  final String title;
  final int seasonNumber; // 0 = whole series / movie
  final int episodeNumber; // 0 = whole season / movie
  final String detail; // UNTRUSTED free text — render passively
  final int occurrences;

  /// Whether an admin has seen the issue's current state. Any non-admin status
  /// change re-flags it unread; an admin opening the thread marks it read.
  final bool read;
  final String resolution;
  final IssueResolutionKind resolutionKind;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final DateTime? closedAt;

  const Issue({
    required this.id,
    required this.source,
    required this.status,
    required this.category,
    required this.reporterId,
    required this.reporterName,
    required this.instanceId,
    required this.tmdbId,
    required this.mediaType,
    required this.title,
    required this.seasonNumber,
    required this.episodeNumber,
    required this.detail,
    required this.occurrences,
    required this.read,
    required this.resolution,
    required this.resolutionKind,
    required this.createdAt,
    required this.updatedAt,
    required this.closedAt,
  });

  bool get isTv => mediaType == 'tv';

  /// Human-facing resolution detail. Older/server-internal sentinel values are
  /// translated so requesters never see implementation vocabulary.
  String get resolutionLabel {
    if (resolutionKind == IssueResolutionKind.reporterTimeout ||
        resolution == 'user_unresponsive') {
      return 'No reply was received before the waiting period ended.';
    }
    return resolution;
  }

  /// A short scope label for list/thread subtitles:
  /// "S2·E4" / "Season 2" / "Movie".
  String get scopeLabel {
    if (isTv) {
      // A positive episode disambiguates Sonarr Specials (season zero) from
      // the season=0/episode=0 whole-series sentinel.
      if (episodeNumber > 0) return 'S$seasonNumber·E$episodeNumber';
      if (seasonNumber <= 0) return 'Series';
      return 'Season $seasonNumber';
    }
    if (mediaType.isNotEmpty && mediaType != 'movie') {
      // 'book', or an off-contract value from an older server (auto issues
      // once stored the *arr service type here) — never claim "Movie".
      return '${mediaType[0].toUpperCase()}${mediaType.substring(1)}';
    }
    return 'Movie';
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
        instanceId: json['instance_id'] as String? ?? '',
        tmdbId: json['tmdb_id'] as int? ?? 0,
        mediaType: json['media_type'] as String? ?? 'movie',
        title: json['title'] as String? ?? '',
        seasonNumber: json['season_number'] as int? ?? 0,
        episodeNumber: json['episode_number'] as int? ?? 0,
        detail: json['detail'] as String? ?? '',
        occurrences: json['occurrences'] as int? ?? 1,
        // Default true so an older server that omits the field doesn't paint the
        // whole list unread.
        read: json['read'] as bool? ?? true,
        resolution: json['resolution'] as String? ?? '',
        resolutionKind: IssueResolutionKind.fromValue(
          json['resolution_kind'] as String?,
        ),
        createdAt:
            DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal(),
        updatedAt:
            DateTime.tryParse(json['updated_at'] as String? ?? '')?.toLocal(),
        closedAt:
            DateTime.tryParse(json['closed_at'] as String? ?? '')?.toLocal(),
      );
}

/// Minimal acknowledgement returned when a reporter creates or adds to an
/// issue. The initial server status lets the app explain that an arr recovery
/// is already being watched instead of implying agent work has begun.
class IssueReportResult {
  final int issueId;
  final IssueStatus status;

  const IssueReportResult({required this.issueId, required this.status});

  factory IssueReportResult.fromJson(Map<String, dynamic> json) =>
      IssueReportResult(
        issueId: (json['issue_id'] as num?)?.toInt() ?? 0,
        status: IssueStatus.fromValue(json['status'] as String?),
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
      authorKind == IssueAuthorKind.user || authorKind == IssueAuthorKind.admin;

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

/// Whether the remediation agent stops after investigation or may prepare
/// changes for an administrator to review. There is intentionally no
/// auto-execution mode.
enum RemediationMode {
  investigateOnly('investigate_only', 'Investigate only'),
  supervised('supervised', 'Propose fixes for review');

  const RemediationMode(this.value, this.label);
  final String value;
  final String label;

  static RemediationMode fromValue(String? value) => values.firstWhere(
        (a) => a.value == value,
        orElse: () => RemediationMode.supervised,
      );
}

/// The admin-tunable remediation settings, GET/PUT
/// `/api/admin/remediation-settings`. Field names mirror the server's
/// `Settings` struct exactly.
class RemediationSettings {
  final bool enabled;
  final bool autoDispatch;
  final bool allowReporting;

  /// When on, an issue that transitions to resolved is marked read (overriding
  /// the flip-to-unread that any non-admin status change otherwise triggers).
  final bool markResolvedAsRead;
  final RemediationMode mode;

  /// Deprecated server compatibility field. The remediation settings screen
  /// always sends this empty so the shared provider is authoritative.
  final String provider;

  /// Deprecated server compatibility field. The remediation settings screen
  /// always sends this empty so the shared model is authoritative.
  final String model;
  final int maxSteps;
  final int maxTurnTokens;
  final int maxWallClockSecs;
  final int dailyRunCap;
  final int circuitBreakerGiveups;
  final int maxUserWaitHours;
  final int observationMinMinutes;
  final int observationQuietMinutes;
  final int observationSettleMinutes;

  const RemediationSettings({
    required this.enabled,
    required this.autoDispatch,
    required this.allowReporting,
    required this.markResolvedAsRead,
    required this.mode,
    required this.provider,
    required this.model,
    required this.maxSteps,
    required this.maxTurnTokens,
    required this.maxWallClockSecs,
    required this.dailyRunCap,
    required this.circuitBreakerGiveups,
    required this.maxUserWaitHours,
    required this.observationMinMinutes,
    required this.observationQuietMinutes,
    required this.observationSettleMinutes,
  });

  factory RemediationSettings.fromJson(Map<String, dynamic> json) =>
      RemediationSettings(
        enabled: json['enabled'] as bool? ?? false,
        autoDispatch: json['auto_dispatch'] as bool? ?? false,
        allowReporting: json['allow_reporting'] as bool? ?? false,
        // Default true to match the server default when the field is absent.
        markResolvedAsRead: json['mark_resolved_as_read'] as bool? ?? true,
        mode: RemediationMode.fromValue(json['mode'] as String?),
        provider: json['provider'] as String? ?? '',
        model: json['model'] as String? ?? '',
        maxSteps: json['max_steps'] as int? ?? 0,
        maxTurnTokens: json['max_turn_tokens'] as int? ?? 0,
        maxWallClockSecs: json['max_wall_clock_secs'] as int? ?? 0,
        dailyRunCap: json['daily_run_cap'] as int? ?? 0,
        circuitBreakerGiveups: json['circuit_breaker_giveups'] as int? ?? 0,
        // Match the server default for older/partial responses; serializing 0
        // back would otherwise look like a deliberate timeout change.
        maxUserWaitHours: json['max_user_wait_hours'] as int? ?? 72,
        observationMinMinutes: json['observation_min_minutes'] as int? ?? 10,
        observationQuietMinutes: json['observation_quiet_minutes'] as int? ?? 5,
        observationSettleMinutes:
            json['observation_settle_minutes'] as int? ?? 2,
      );

  Map<String, dynamic> toJson() => {
        'enabled': enabled,
        'auto_dispatch': autoDispatch,
        'allow_reporting': allowReporting,
        'mark_resolved_as_read': markResolvedAsRead,
        'mode': mode.value,
        'provider': provider,
        'model': model,
        'max_steps': maxSteps,
        'max_turn_tokens': maxTurnTokens,
        'max_wall_clock_secs': maxWallClockSecs,
        'daily_run_cap': dailyRunCap,
        'circuit_breaker_giveups': circuitBreakerGiveups,
        'max_user_wait_hours': maxUserWaitHours,
        'observation_min_minutes': observationMinMinutes,
        'observation_quiet_minutes': observationQuietMinutes,
        'observation_settle_minutes': observationSettleMinutes,
      };

  RemediationSettings copyWith({
    bool? enabled,
    bool? autoDispatch,
    bool? allowReporting,
    bool? markResolvedAsRead,
    RemediationMode? mode,
    String? provider,
    String? model,
    int? maxSteps,
    int? maxTurnTokens,
    int? maxWallClockSecs,
    int? dailyRunCap,
    int? circuitBreakerGiveups,
    int? maxUserWaitHours,
    int? observationMinMinutes,
    int? observationQuietMinutes,
    int? observationSettleMinutes,
  }) =>
      RemediationSettings(
        enabled: enabled ?? this.enabled,
        autoDispatch: autoDispatch ?? this.autoDispatch,
        allowReporting: allowReporting ?? this.allowReporting,
        markResolvedAsRead: markResolvedAsRead ?? this.markResolvedAsRead,
        mode: mode ?? this.mode,
        provider: provider ?? this.provider,
        model: model ?? this.model,
        maxSteps: maxSteps ?? this.maxSteps,
        maxTurnTokens: maxTurnTokens ?? this.maxTurnTokens,
        maxWallClockSecs: maxWallClockSecs ?? this.maxWallClockSecs,
        dailyRunCap: dailyRunCap ?? this.dailyRunCap,
        circuitBreakerGiveups:
            circuitBreakerGiveups ?? this.circuitBreakerGiveups,
        maxUserWaitHours: maxUserWaitHours ?? this.maxUserWaitHours,
        observationMinMinutes:
            observationMinMinutes ?? this.observationMinMinutes,
        observationQuietMinutes:
            observationQuietMinutes ?? this.observationQuietMinutes,
        observationSettleMinutes:
            observationSettleMinutes ?? this.observationSettleMinutes,
      );
}
