import 'package:dio/dio.dart';

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
    required this.seasonScope,
    required this.qualityProfileId,
    required this.requestedAt,
  });

  bool get isTv => mediaType == 'tv';

  factory PendingRequestItem.fromJson(Map<String, dynamic> json) =>
      PendingRequestItem(
        id: json['id'] as int? ?? 0,
        userId: json['user_id'] as int? ?? 0,
        username: json['username'] as String? ?? '',
        tmdbId: json['tmdb_id'] as int? ?? 0,
        tvdbId: json['tvdb_id'] as int? ?? 0,
        mediaType: json['media_type'] as String? ?? 'movie',
        title: json['title'] as String? ?? '',
        seasonScope: json['season_scope'] as String? ?? '',
        qualityProfileId: json['quality_profile_id'] as int? ?? 0,
        requestedAt:
            DateTime.tryParse(json['requested_at'] as String? ?? '')?.toLocal(),
      );
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

  Future<List<PendingRequestItem>> listPending() async {
    final resp = await _dio.get('/api/admin/requests');
    return ((resp.data as List?) ?? const [])
        .map((e) => PendingRequestItem.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> approve(int id, {String? seasonScope, int? qualityProfileId}) async {
    final body = <String, dynamic>{};
    if (seasonScope != null) body['season_scope'] = seasonScope;
    if (qualityProfileId != null && qualityProfileId != 0) {
      body['quality_profile_id'] = qualityProfileId;
    }
    await _dio.post('/api/admin/requests/$id/approve', data: body);
  }

  Future<void> deny(int id, {String? reason}) async {
    await _dio.post('/api/admin/requests/$id/deny',
        data: {if (reason != null && reason.isNotEmpty) 'reason': reason});
  }
}
