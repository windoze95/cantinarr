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
  final String mediaType;
  final String title;
  final String bookFormat;
  final String instanceName;
  final int requesterCount;
  final String seasonScope;
  final int qualityProfileId;
  final DateTime? requestedAt;

  const PendingRequestItem({
    required this.id,
    required this.userId,
    required this.username,
    required this.tmdbId,
    required this.tvdbId,
    required this.mediaType,
    required this.title,
    required this.bookFormat,
    required this.instanceName,
    required this.requesterCount,
    required this.seasonScope,
    required this.qualityProfileId,
    required this.requestedAt,
  });

  bool get isTv => mediaType == 'tv';
  bool get isBook => mediaType == 'book';
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
        mediaType: json['media_type'] as String? ?? 'movie',
        title: json['title'] as String? ?? '',
        bookFormat: json['book_format'] as String? ?? 'both',
        instanceName: json['instance_name'] as String? ?? '',
        requesterCount: _positiveRequesterCount(json['requester_count']),
        seasonScope: json['season_scope'] as String? ?? '',
        qualityProfileId: json['quality_profile_id'] as int? ?? 0,
        requestedAt:
            DateTime.tryParse(json['requested_at'] as String? ?? '')?.toLocal(),
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

  Future<List<PendingRequestItem>> listPending() async {
    final resp = await _dio.get('/api/admin/requests');
    return ((resp.data as List?) ?? const [])
        .map((e) => PendingRequestItem.fromJson(e as Map<String, dynamic>))
        .toList();
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
}
