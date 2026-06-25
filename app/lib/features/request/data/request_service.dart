import 'dart:convert';

import 'package:dio/dio.dart';
import '../../discover/data/tmdb_models.dart';
import 'book_ownership.dart';

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

enum BookRequestFormat {
  ebook('ebook', 'eBook'),
  audiobook('audiobook', 'Audiobook'),
  both('both', 'eBook + Audiobook');

  const BookRequestFormat(this.value, this.label);

  final String value;
  final String label;

  static BookRequestFormat fromValue(String value) =>
      BookRequestFormat.values.firstWhere(
        (format) => format.value == value,
        orElse: () => BookRequestFormat.both,
      );
}

/// A user's per-format request state for a book. [status] is the collapsed
/// (latest) state; [formats] maps each already-requested concrete format to its
/// status (a stored "both" request fills both ebook and audiobook); [ownership]
/// is what the user already has in the Chaptarr library. A format is covered
/// (no longer requestable) when it's already requested OR already owned, so the
/// dashboard keeps offering only the formats the user neither requested nor owns.
class BookRequestStatusDetail {
  final RequestStatus status;
  final Map<BookRequestFormat, RequestStatus> formats;
  final BookOwnership? ownership;

  const BookRequestStatusDetail({
    this.status = RequestStatus.unavailable,
    this.formats = const {},
    this.ownership,
  });

  /// Returns a copy carrying library [ownership] (from the owned-books digest).
  BookRequestStatusDetail withOwnership(BookOwnership? ownership) =>
      BookRequestStatusDetail(
          status: status, formats: formats, ownership: ownership);

  /// Covered = already requested (non-denied) OR already owned in the library.
  /// "both" is covered only when each concrete format is covered (possibly from
  /// different sources — e.g. ebook owned + audiobook requested). The backend
  /// expands a stored "both" request into ebook+audiobook, so [formats] never
  /// carries a literal "both" key.
  bool isCovered(BookRequestFormat format) {
    if (format == BookRequestFormat.both) {
      return isCovered(BookRequestFormat.ebook) &&
          isCovered(BookRequestFormat.audiobook);
    }
    return _requestCovered(format) || _ownershipCovers(format);
  }

  bool _requestCovered(BookRequestFormat format) {
    final s = formats[format];
    return s != null &&
        s != RequestStatus.denied &&
        s != RequestStatus.unavailable;
  }

  bool _ownershipCovers(BookRequestFormat format) {
    final o = ownership;
    if (o == null) return false;
    return switch (format) {
      BookRequestFormat.ebook => o.ebook.owned,
      BookRequestFormat.audiobook => o.audiobook.owned,
      BookRequestFormat.both => o.ebook.owned && o.audiobook.owned,
    };
  }

  /// Short reason a [format] is covered, for the request sheet: a request status
  /// label, else "Downloaded"/"In Library" from library ownership, else null.
  String? coverageLabel(BookRequestFormat format) {
    if (!isCovered(format)) return null;
    final reqKey =
        format == BookRequestFormat.both ? BookRequestFormat.ebook : format;
    final s = formats[reqKey];
    if (s != null &&
        s != RequestStatus.denied &&
        s != RequestStatus.unavailable) {
      return s.label;
    }
    final o = ownership;
    if (o != null) {
      final fo = format == BookRequestFormat.audiobook ? o.audiobook : o.ebook;
      if (fo.downloaded) return 'Downloaded';
      if (fo.monitored) return 'In Library';
    }
    return null;
  }
}

class RequestSubmissionException implements Exception {
  final String message;

  const RequestSubmissionException(this.message);

  @override
  String toString() => message;
}

String _requestErrorMessage(DioException error) {
  final data = error.response?.data;
  if (data is Map) {
    final message = data['error'] ?? data['message'];
    if (message is String && message.isNotEmpty) return message;
  }
  if (data is String && data.isNotEmpty) return data;
  return error.message ?? 'Request failed. Please try again.';
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

  static String labelFor(String value) => choices
      .firstWhere((c) => c.value == value, orElse: () => choices.last)
      .label;

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
        defaultSeasonScope:
            json['default_season_scope'] as String? ?? SeasonScope.all,
        qualityProfiles: ((json['quality_profiles'] as List?) ?? const [])
            .map(
                (e) => QualityProfileOption.fromJson(e as Map<String, dynamic>))
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

  /// Check the current user's request state for a book, keyed by the Chaptarr/
  /// Readarr foreignBookId (books have no tmdb_id). Returns one of
  /// unavailable / pending / requested / denied.
  Future<RequestStatus> checkBookStatus(String foreignId) async =>
      (await checkBookStatusDetail(foreignId)).status;

  /// Like [checkBookStatus] but also returns the per-format breakdown so the
  /// caller can still offer a not-yet-requested format. Falls back to an
  /// unavailable/empty detail on any failure.
  Future<BookRequestStatusDetail> checkBookStatusDetail(
      String foreignId) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/book-status',
        queryParameters: {'foreign_id': foreignId},
      );
      final data = resp.data as Map<String, dynamic>;
      final status = RequestStatus.values.firstWhere(
        (s) => s.name == (data['status'] as String? ?? 'unavailable'),
        orElse: () => RequestStatus.unavailable,
      );
      final formats = <BookRequestFormat, RequestStatus>{};
      final raw = data['book_formats'];
      if (raw is Map) {
        raw.forEach((key, value) {
          BookRequestFormat? fmt;
          for (final f in BookRequestFormat.values) {
            if (f.value == key.toString()) {
              fmt = f;
              break;
            }
          }
          if (fmt == null) return;
          formats[fmt] = RequestStatus.values.firstWhere(
            (s) => s.name == value.toString(),
            orElse: () => RequestStatus.requested,
          );
        });
      }
      return BookRequestStatusDetail(status: status, formats: formats);
    } catch (_) {
      return const BookRequestStatusDetail();
    }
  }

  /// Submit a book request. Books are keyed by the foreignBookId, not a tmdb_id;
  /// the backend adds the book to the user's granted Chaptarr instance (after
  /// approval when the user's policy requires it). Returns the resulting status
  /// (e.g. [RequestStatus.pending]) or null on non-HTTP failure.
  Future<RequestStatus?> requestBook({
    required String foreignId,
    required String title,
    BookRequestFormat format = BookRequestFormat.both,
  }) async {
    try {
      final resp = await _backendDio.post('/api/requests', data: {
        'media_type': 'book',
        'foreign_id': foreignId,
        'title': title,
        'book_format': format.value,
      });
      if (resp.statusCode != 200 && resp.statusCode != 201) return null;
      final data = resp.data as Map<String, dynamic>?;
      final name = data?['status'] as String? ?? 'requested';
      return RequestStatus.values.firstWhere(
        (s) => s.name == name,
        orElse: () => RequestStatus.requested,
      );
    } on DioException catch (e) {
      throw RequestSubmissionException(_requestErrorMessage(e));
    } catch (_) {
      return null;
    }
  }
}
