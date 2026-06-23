import 'dart:convert';

import 'package:dio/dio.dart';
import '../../discover/data/tmdb_models.dart';

/// Status of a media request from the user's perspective.
enum RequestStatus {
  /// Not on the server, can be requested.
  unavailable('Not Available', 'Request'),

  /// Awaiting an administrator's approval.
  pending('Pending Approval', 'Pending'),

  /// Request has been submitted, waiting for processing.
  requested('Requested', 'Requested'),

  /// Actively downloading.
  downloading('Downloading', 'Downloading'),

  /// Fully available on the media server.
  available('Available on Plex', 'Watch Now'),

  /// Partially available (some seasons/episodes).
  partial('Partially Available', 'Request More'),

  /// An administrator declined the request; it can be requested again.
  denied('Request Denied', 'Request');

  const RequestStatus(this.label, this.buttonLabel);
  final String label;
  final String buttonLabel;
}

/// The TV season-scope choices a user may attach to a request. The string
/// values mirror the backend's season_scope enum.
class SeasonScope {
  static const String all = 'all';
  static const String first = 'first';
  static const String latest = 'latest';
  static const String pilot = 'pilot';

  /// Selectable choices, in display order.
  static const List<({String value, String label})> choices = [
    (value: pilot, label: 'Pilot only'),
    (value: first, label: 'First season'),
    (value: latest, label: 'Most recent season'),
    (value: all, label: 'Entire series'),
  ];

  static String labelFor(String value) =>
      choices.firstWhere((c) => c.value == value, orElse: () => choices.last).label;

  /// True when [value] holds an explicit JSON season list (e.g. "[3,5]")
  /// rather than a coarse scope keyword. The backend stores per-season requests
  /// this way in the season_scope column.
  static bool isExplicitList(String value) => value.startsWith('[');

  /// A human label for any stored season_scope value: coarse scopes map to
  /// their choice label; an explicit list renders as "Season 3" / "Seasons 3, 5".
  static String describe(String value) {
    if (isExplicitList(value)) {
      try {
        final list = (jsonDecode(value) as List).map((e) => e as int).toList()
          ..sort();
        if (list.isEmpty) return labelFor(value);
        if (list.length == 1) return 'Season ${list.first}';
        return 'Seasons ${list.join(', ')}';
      } catch (_) {
        return value;
      }
    }
    return labelFor(value);
  }
}

/// One season's availability, mirroring the backend `SeasonStatus` payload
/// (`StatusResponse.seasons[]`). Drives the per-season request table.
class RequestSeasonStatus {
  final int seasonNumber;
  final int episodeFileCount;
  final int episodeCount;
  final RequestStatus status;
  final double progress;

  const RequestSeasonStatus({
    required this.seasonNumber,
    this.episodeFileCount = 0,
    this.episodeCount = 0,
    this.status = RequestStatus.unavailable,
    this.progress = 0,
  });

  factory RequestSeasonStatus.fromJson(Map<String, dynamic> json) {
    final statusName = json['status'] as String? ?? 'unavailable';
    return RequestSeasonStatus(
      seasonNumber: json['season_number'] as int? ?? 0,
      episodeFileCount: json['episode_file_count'] as int? ?? 0,
      episodeCount: json['episode_count'] as int? ?? 0,
      status: RequestStatus.values.firstWhere(
        (s) => s.name == statusName,
        orElse: () => RequestStatus.unavailable,
      ),
      progress: (json['progress'] as num?)?.toDouble() ?? 0,
    );
  }

  /// True once every episode of the season has a file.
  bool get isAvailable => status == RequestStatus.available;

  /// "x/y" episode-file availability, e.g. "7/10".
  String get episodesLabel => '$episodeFileCount/$episodeCount';
}

/// The full request status for a title: the overall [status] plus, for TV, the
/// per-season breakdown (empty for movies or series not in the library).
class RequestStatusDetail {
  final RequestStatus status;
  final List<RequestSeasonStatus> seasons;

  const RequestStatusDetail({
    this.status = RequestStatus.unavailable,
    this.seasons = const [],
  });

  factory RequestStatusDetail.fromJson(Map<String, dynamic> json) {
    final statusName = json['status'] as String? ?? 'unavailable';
    return RequestStatusDetail(
      status: RequestStatus.values.firstWhere(
        (s) => s.name == statusName,
        orElse: () => RequestStatus.unavailable,
      ),
      seasons: ((json['seasons'] as List?) ?? const [])
          .map((e) => RequestSeasonStatus.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// An arr quality profile the user may pick for a request.
class QualityProfileOption {
  final int id;
  final String name;
  const QualityProfileOption({required this.id, required this.name});

  factory QualityProfileOption.fromJson(Map<String, dynamic> json) =>
      QualityProfileOption(
        id: json['id'] as int? ?? 0,
        name: json['name'] as String? ?? '',
      );
}

/// What the current user is permitted to choose for a request, plus the
/// available quality profiles (only populated when quality choice is allowed).
class RequestOptions {
  final bool canChooseSeason;
  final bool canChooseQuality;
  final String defaultSeasonScope;
  final List<QualityProfileOption> qualityProfiles;

  const RequestOptions({
    required this.canChooseSeason,
    required this.canChooseQuality,
    required this.defaultSeasonScope,
    required this.qualityProfiles,
  });

  bool get hasChoices =>
      canChooseSeason || (canChooseQuality && qualityProfiles.isNotEmpty);

  factory RequestOptions.fromJson(Map<String, dynamic> json) => RequestOptions(
        canChooseSeason: json['can_choose_season'] as bool? ?? false,
        canChooseQuality: json['can_choose_quality'] as bool? ?? false,
        defaultSeasonScope: json['default_season_scope'] as String? ?? SeasonScope.all,
        qualityProfiles: ((json['quality_profiles'] as List?) ?? const [])
            .map((e) => QualityProfileOption.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// Routes media requests through the Cantinarr backend.
///
/// The backend handles all TMDB-to-TVDB bridging and Radarr/Sonarr
/// communication transparently.
class RequestService {
  final Dio _backendDio;

  RequestService({required Dio backendDio}) : _backendDio = backendDio;

  /// Check the current status of a media item for the current user (surfaces
  /// the user's own pending/denied state ahead of live availability).
  Future<RequestStatus> checkStatus(int tmdbId, MediaType mediaType) async {
    return (await checkStatusDetail(tmdbId, mediaType)).status;
  }

  /// Like [checkStatus] but also returns the per-season availability breakdown
  /// (TV only). Falls back to an unavailable detail with no seasons on error.
  Future<RequestStatusDetail> checkStatusDetail(
      int tmdbId, MediaType mediaType) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/$tmdbId/status',
        queryParameters: {'media_type': mediaType.name},
      );
      return RequestStatusDetail.fromJson(resp.data as Map<String, dynamic>);
    } catch (_) {
      return const RequestStatusDetail();
    }
  }

  /// Fetch the option set the current user may choose for [mediaType].
  /// Returns null on error (the caller then submits with no options).
  Future<RequestOptions?> fetchOptions(MediaType mediaType) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/options',
        queryParameters: {'media_type': mediaType.name},
      );
      return RequestOptions.fromJson(resp.data as Map<String, dynamic>);
    } catch (_) {
      return null;
    }
  }

  /// Submit a request for a media item. Returns the resulting [RequestStatus]
  /// (e.g. [RequestStatus.pending] when approval is required), or null on
  /// failure.
  Future<RequestStatus?> request({
    required int tmdbId,
    required MediaType mediaType,
    String? title,
    int? tvdbId,
    String? seasonScope,
    List<int>? seasons,
    int? qualityProfileId,
  }) async {
    try {
      final body = <String, dynamic>{
        'tmdb_id': tmdbId,
        'media_type': mediaType.name,
      };
      if (title != null) body['title'] = title;
      if (tvdbId != null && tvdbId != 0) body['tvdb_id'] = tvdbId;
      // An explicit season list routes the server to the seasonpass path
      // (monitor exactly these seasons). It takes precedence over season_scope,
      // so only send the coarse scope when no explicit list was chosen.
      if (seasons != null && seasons.isNotEmpty) {
        body['seasons'] = seasons;
      } else if (seasonScope != null) {
        body['season_scope'] = seasonScope;
      }
      if (qualityProfileId != null && qualityProfileId != 0) {
        body['quality_profile_id'] = qualityProfileId;
      }
      final resp = await _backendDio.post('/api/requests', data: body);
      if (resp.statusCode != 200 && resp.statusCode != 201) return null;
      final data = resp.data as Map<String, dynamic>?;
      final statusName = data?['status'] as String? ?? 'requested';
      return RequestStatus.values.firstWhere(
        (s) => s.name == statusName,
        orElse: () => RequestStatus.requested,
      );
    } catch (_) {
      return null;
    }
  }
}
