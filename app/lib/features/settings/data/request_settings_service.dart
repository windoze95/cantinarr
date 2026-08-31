import 'package:dio/dio.dart';

import '../../request/data/request_service.dart';

int _positiveRequesterCount(Object? value) =>
    value is int && value > 0 ? value : 1;

/// An arr quality profile (id + name) offered for selection.
class QualityProfile {
  final int id;
  final String name;
  const QualityProfile({required this.id, required this.name});

  factory QualityProfile.fromJson(Map<String, dynamic> json) => QualityProfile(
        id: json['id'] as int? ?? 0,
        name: json['name'] as String? ?? '',
      );
}

/// System-wide request defaults.
class GlobalRequestSettings {
  final bool requireApproval;
  final bool allowSeasonChoice;
  final String defaultSeasonScope;
  final bool allowQualityChoice;
  final int defaultQualityRadarr;
  final int defaultQualitySonarr;

  const GlobalRequestSettings({
    required this.requireApproval,
    required this.allowSeasonChoice,
    required this.defaultSeasonScope,
    required this.allowQualityChoice,
    required this.defaultQualityRadarr,
    required this.defaultQualitySonarr,
  });

  factory GlobalRequestSettings.fromJson(Map<String, dynamic> json) =>
      GlobalRequestSettings(
        requireApproval: json['require_approval'] as bool? ?? false,
        allowSeasonChoice: json['allow_season_choice'] as bool? ?? true,
        defaultSeasonScope: json['default_season_scope'] as String? ?? 'all',
        allowQualityChoice: json['allow_quality_choice'] as bool? ?? false,
        defaultQualityRadarr: json['default_quality_radarr'] as int? ?? 0,
        defaultQualitySonarr: json['default_quality_sonarr'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'require_approval': requireApproval,
        'allow_season_choice': allowSeasonChoice,
        'default_season_scope': defaultSeasonScope,
        'allow_quality_choice': allowQualityChoice,
        'default_quality_radarr': defaultQualityRadarr,
        'default_quality_sonarr': defaultQualitySonarr,
      };

  GlobalRequestSettings copyWith({
    bool? requireApproval,
    bool? allowSeasonChoice,
    String? defaultSeasonScope,
    bool? allowQualityChoice,
    int? defaultQualityRadarr,
    int? defaultQualitySonarr,
  }) =>
      GlobalRequestSettings(
        requireApproval: requireApproval ?? this.requireApproval,
        allowSeasonChoice: allowSeasonChoice ?? this.allowSeasonChoice,
        defaultSeasonScope: defaultSeasonScope ?? this.defaultSeasonScope,
        allowQualityChoice: allowQualityChoice ?? this.allowQualityChoice,
        defaultQualityRadarr: defaultQualityRadarr ?? this.defaultQualityRadarr,
        defaultQualitySonarr: defaultQualitySonarr ?? this.defaultQualitySonarr,
      );
}

/// Global defaults plus the arr quality profiles available for selection.
class AdminRequestSettings {
  final GlobalRequestSettings settings;
  final List<QualityProfile> radarrProfiles;
  final List<QualityProfile> sonarrProfiles;

  const AdminRequestSettings({
    required this.settings,
    required this.radarrProfiles,
    required this.sonarrProfiles,
  });

  factory AdminRequestSettings.fromJson(Map<String, dynamic> json) =>
      AdminRequestSettings(
        settings: GlobalRequestSettings.fromJson(
            json['settings'] as Map<String, dynamic>? ?? const {}),
        radarrProfiles: _profiles(json['radarr_profiles']),
        sonarrProfiles: _profiles(json['sonarr_profiles']),
      );

  static List<QualityProfile> _profiles(dynamic raw) =>
      ((raw as List?) ?? const [])
          .map((e) => QualityProfile.fromJson(e as Map<String, dynamic>))
          .toList();
}

/// One user's per-user overrides. A null field means "inherit the global
/// default" for that option.
class UserRequestSettings {
  final bool? requireApproval;
  final bool? allowSeasonChoice;
  final String? seasonScope;
  final bool? allowQualityChoice;
  final int? qualityProfileRadarr;
  final int? qualityProfileSonarr;

  const UserRequestSettings({
    this.requireApproval,
    this.allowSeasonChoice,
    this.seasonScope,
    this.allowQualityChoice,
    this.qualityProfileRadarr,
    this.qualityProfileSonarr,
  });

  factory UserRequestSettings.fromJson(Map<String, dynamic> json) =>
      UserRequestSettings(
        requireApproval: json['require_approval'] as bool?,
        allowSeasonChoice: json['allow_season_choice'] as bool?,
        seasonScope: json['season_scope'] as String?,
        allowQualityChoice: json['allow_quality_choice'] as bool?,
        qualityProfileRadarr: json['quality_profile_radarr'] as int?,
        qualityProfileSonarr: json['quality_profile_sonarr'] as int?,
      );

  /// Serializes including nulls so the backend stores NULL (= inherit).
  Map<String, dynamic> toJson() => {
        'require_approval': requireApproval,
        'allow_season_choice': allowSeasonChoice,
        'season_scope': seasonScope,
        'allow_quality_choice': allowQualityChoice,
        'quality_profile_radarr': qualityProfileRadarr,
        'quality_profile_sonarr': qualityProfileSonarr,
      };
}

/// One row of the admin approval queue.
class PendingRequestItem {
  final int id;
  final int userId;
  final String username;
  final int tmdbId;
  final int tvdbId;

  /// The Chaptarr identity a book row is addressed by; empty for movies/TV.
  final String foreignId;
  final String mediaType;
  final String title;

  /// TMDB artwork path, best-effort: empty means the server resolved none for
  /// this load (or the row is a book, which carries no cover), not that the
  /// title has no artwork.
  final String posterPath;
  final String bookFormat;
  final String instanceId;
  final String instanceName;
  final int requesterCount;
  final String seasonScope;
  final int qualityProfileId;
  final DateTime? requestedAt;

  /// Set only on rows from the waiting list: why the server is holding this
  /// request itself. Empty on every approval-queue row, which is waiting on a
  /// person instead.
  final String waitReason;

  /// When the server last retried a waiting row. Null means it could not vouch
  /// for an attempt (it restarted since the request was parked) — the honest
  /// answer is "unknown", not "never".
  final DateTime? lastAttemptAt;

  /// Set when this queue row is not a policy question: the automatic add
  /// already ran and failed. Empty on an ordinary decision.
  final String addFailureReason;

  const PendingRequestItem({
    required this.id,
    required this.userId,
    required this.username,
    required this.tmdbId,
    required this.tvdbId,
    required this.foreignId,
    required this.mediaType,
    required this.title,
    required this.posterPath,
    required this.bookFormat,
    required this.instanceId,
    required this.instanceName,
    required this.requesterCount,
    required this.seasonScope,
    required this.qualityProfileId,
    required this.requestedAt,
    this.waitReason = '',
    this.lastAttemptAt,
    this.addFailureReason = '',
  });

  /// What the server is waiting on, in admin vocabulary. Null for an actionable
  /// row; a generic line for a reason this app version does not know, because a
  /// wait it cannot name is still a wait it must not hide.
  String? get waitDescription => switch (waitReason) {
        '' => null,
        'author_import' => 'The library is still importing this author',
        _ => 'The library is not ready for this book yet',
      };

  /// Why this row is in the queue when it isn't a routine yes/no, and what the
  /// admin would actually do about it. Null for an ordinary decision — most
  /// rows — so the queue stays quiet unless there is something to say.
  ({String reason, String action})? get addFailure => switch (addFailureReason) {
        '' => null,
        'metadata_unresolved' => (
            reason: 'The library couldn’t match this book',
            action: 'Approving retries the same add. Add it in the library '
                'first, then approve.',
          ),
        'import_abandoned' => (
            reason: 'The automatic add failed while waiting for the author '
                'import',
            action: 'Try again to resume waiting, or deny to close the '
                'request.',
          ),
        'import_failed' => (
            reason: 'The library gave up importing this author',
            action: 'Its metadata service reported the import failed. Try '
                'again to reopen it and keep waiting, or deny to close the '
                'request.',
          ),
        'import_cancelled' => (
            reason: 'The author import was cancelled in the library',
            action: 'Try again to queue it once more and keep waiting, or '
                'deny to close the request.',
          ),
        // A reason this version doesn't know is still not a routine decision.
        _ => (
            reason: 'The automatic add already failed',
            action: 'Approving retries it. Check the library first.',
          ),
      };

  /// True when this row is in the queue because a server-watched author-import
  /// wait ended — the rows whose honest verbs are "try again" and deny, since
  /// approving just replays an add the library already refused.
  bool get isImportWait => switch (addFailureReason) {
        'import_abandoned' || 'import_failed' || 'import_cancelled' => true,
        _ => false,
      };

  bool get isTv => mediaType == 'tv';
  bool get isBook => mediaType == 'book';

  /// Route to the content this request is for, or null when the row can't
  /// address one (a legacy book row stored without its foreign id, a movie row
  /// with no TMDB id). Books pin the library the request named — an approval can
  /// outlive the admin switching their drawer to another Chaptarr instance.
  String? get detailRoute {
    if (isBook) {
      final id = foreignId.trim();
      if (id.isEmpty) return null;
      final query = <String>[
        if (title.trim().isNotEmpty)
          'title=${Uri.encodeQueryComponent(title.trim())}',
        if (instanceId.trim().isNotEmpty)
          'instance_id=${Uri.encodeQueryComponent(instanceId.trim())}',
      ];
      final suffix = query.isEmpty ? '' : '?${query.join('&')}';
      return '/detail/book/${Uri.encodeComponent(id)}$suffix';
    }
    if (tmdbId <= 0) return null;
    return '/detail/${isTv ? 'tv' : 'movie'}/$tmdbId';
  }
  String get mediaLabel => switch (mediaType) {
        'tv' => 'TV',
        'book' => 'Book',
        _ => 'Movie',
      };
  BookRequestFormat? get requestedBookFormat =>
      BookRequestFormat.tryFromValue(bookFormat);
  String get requestedByLabel {
    final requester = username.trim().isEmpty ? 'a user' : username.trim();
    final others = requesterCount - 1;
    if (others <= 0) return 'Requested by $requester';
    return 'Requested by $requester and $others ${others == 1 ? 'other' : 'others'}';
  }

  factory PendingRequestItem.fromJson(Map<String, dynamic> json) =>
      PendingRequestItem(
        id: json['id'] as int? ?? 0,
        userId: json['user_id'] as int? ?? 0,
        username: json['username'] as String? ?? '',
        tmdbId: json['tmdb_id'] as int? ?? 0,
        tvdbId: json['tvdb_id'] as int? ?? 0,
        foreignId: json['foreign_id'] as String? ?? '',
        mediaType: json['media_type'] as String? ?? 'movie',
        title: json['title'] as String? ?? '',
        posterPath: json['poster_path'] as String? ?? '',
        bookFormat: json['book_format'] as String? ?? 'both',
        instanceId: json['instance_id'] as String? ?? '',
        instanceName: json['instance_name'] as String? ?? '',
        requesterCount: _positiveRequesterCount(json['requester_count']),
        seasonScope: json['season_scope'] as String? ?? '',
        qualityProfileId: json['quality_profile_id'] as int? ?? 0,
        requestedAt:
            DateTime.tryParse(json['requested_at'] as String? ?? '')?.toLocal(),
        waitReason: json['wait_reason'] as String? ?? '',
        lastAttemptAt: DateTime.tryParse(
                json['last_attempt_at'] as String? ?? '')
            ?.toLocal(),
        addFailureReason: json['add_failure_reason'] as String? ?? '',
      );
}

class BookApprovalResult {
  final RequestStatus? status;
  final Map<BookRequestFormat, RequestStatus> formats;
  final bool isKnown;

  const BookApprovalResult({
    required this.status,
    required this.formats,
    required this.isKnown,
  });

  factory BookApprovalResult.fromJson(Object? value) {
    if (value is! Map) {
      return const BookApprovalResult(
        status: null,
        formats: {},
        isKnown: false,
      );
    }
    RequestStatus? parseStatus(Object? raw) {
      for (final status in RequestStatus.values) {
        if (status.name == raw?.toString()) return status;
      }
      return null;
    }

    final status = parseStatus(value['status']);
    var isKnown = status != null;
    final formats = <BookRequestFormat, RequestStatus>{};
    final rawFormats = value['book_formats'];
    if (rawFormats is Map) {
      rawFormats.forEach((key, rawStatus) {
        final format = BookRequestFormat.tryFromValue(key.toString());
        final parsedStatus = parseStatus(rawStatus);
        if (format == null ||
            format == BookRequestFormat.both ||
            parsedStatus == null) {
          isKnown = false;
          return;
        }
        formats[format] = parsedStatus;
      });
    }
    if (status == RequestStatus.partial && formats.isEmpty) isKnown = false;
    return BookApprovalResult(
      status: status,
      formats: formats,
      isKnown: isKnown,
    );
  }
}

/// Admin API client for media-request settings + the approval queue.
class RequestSettingsService {
  final Dio _dio;

  RequestSettingsService({required Dio backendDio}) : _dio = backendDio;

  Future<AdminRequestSettings> getAdminSettings() async {
    final resp = await _dio.get('/api/admin/request-settings');
    return AdminRequestSettings.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<AdminRequestSettings> updateGlobalSettings(
      GlobalRequestSettings settings) async {
    final resp =
        await _dio.put('/api/admin/request-settings', data: settings.toJson());
    return AdminRequestSettings.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<UserRequestSettings> getUserSettings(int userId) async {
    final resp = await _dio.get('/api/admin/users/$userId/request-settings');
    return UserRequestSettings.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> updateUserSettings(
      int userId, UserRequestSettings settings) async {
    await _dio.put('/api/admin/users/$userId/request-settings',
        data: settings.toJson());
  }

  /// The user's per-service default-instance overrides, keyed by service type.
  /// Only set overrides are returned; absent keys inherit the global default.
  Future<Map<String, String>> getUserDefaultInstances(int userId) async {
    final resp = await _dio.get('/api/admin/users/$userId/default-instances');
    final data = (resp.data as Map?) ?? const {};
    return data.map((k, v) => MapEntry(k.toString(), v.toString()));
  }

  /// Sets the user's default-instance overrides. A null value clears that
  /// override (for chaptarr, clearing revokes access). Returns the updated map.
  Future<void> updateUserDefaultInstances(
      int userId, Map<String, String?> defaults) async {
    await _dio.put('/api/admin/users/$userId/default-instances',
        data: defaults);
  }

  /// The user's additional instance access grants, keyed by service type.
  /// Grants widen what the user may pick per request; they never move the
  /// default above.
  Future<Map<String, List<String>>> getUserInstanceGrants(int userId) async {
    final resp = await _dio.get('/api/admin/users/$userId/instance-grants');
    final data = resp.data as Map<String, dynamic>? ?? const {};
    return {
      for (final entry in data.entries)
        entry.key: ((entry.value as List?) ?? const [])
            .map((id) => id as String)
            .toList(),
    };
  }

  /// Replaces the user's grant rows for every service type present as a key;
  /// an empty list clears that type's grants, an absent key leaves it alone.
  Future<void> updateUserInstanceGrants(
      int userId, Map<String, List<String>> grants) async {
    await _dio.put('/api/admin/users/$userId/instance-grants', data: grants);
  }

  Future<List<PendingRequestItem>> listPending() async {
    final resp = await _dio.get('/api/admin/requests');
    return ((resp.data as List?) ?? const [])
        .map((e) => PendingRequestItem.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// The requests the server is retrying itself. Informational: these rows have
  /// no approve/deny.
  ///
  /// Returns null when this server has no such endpoint — a 404, or a body that
  /// isn't a list, both meaning "older build". That is absence of the feature,
  /// and the caller hides the section, leaving Approvals exactly as it was
  /// before this existed.
  ///
  /// Every other failure throws, so the caller can say it went blind. The one
  /// thing neither may do is quietly render an empty waiting list: "nothing is
  /// waiting" and "I couldn't look" are the two answers this whole change is
  /// about telling apart.
  Future<List<PendingRequestItem>?> listWaiting() async {
    try {
      final resp = await _dio.get('/api/admin/requests/waiting');
      final rows = resp.data;
      if (rows is! List) return null;
      return rows
          .whereType<Map<String, dynamic>>()
          .map(PendingRequestItem.fromJson)
          .toList();
    } on DioException catch (e) {
      if (e.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<BookApprovalResult> approve(int id,
      {String? seasonScope, int? qualityProfileId}) async {
    final body = <String, dynamic>{};
    if (seasonScope != null) body['season_scope'] = seasonScope;
    if (qualityProfileId != null && qualityProfileId != 0) {
      body['quality_profile_id'] = qualityProfileId;
    }
    final response =
        await _dio.post('/api/admin/requests/$id/approve', data: body);
    return BookApprovalResult.fromJson(response.data);
  }

  Future<void> deny(int id, {String? reason}) async {
    await _dio.post('/api/admin/requests/$id/deny',
        data: {if (reason != null && reason.isNotEmpty) 'reason': reason});
  }

  /// "Try again" on a request whose author-import wait ended: the server
  /// replays the add once and either completes the request (the author landed
  /// since) or puts the row back under its automatic watch. Returns the
  /// server's confirmation message — empty when the replay completed the
  /// request outright.
  Future<String> wait(int id) async {
    final resp = await _dio.post('/api/admin/requests/$id/wait');
    final data = resp.data;
    if (data is Map<String, dynamic>) {
      return data['message'] as String? ?? '';
    }
    return '';
  }
}
