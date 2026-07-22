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
  available('Available', 'Available'),

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

  static BookRequestFormat? tryFromValue(String? value) {
    for (final format in BookRequestFormat.values) {
      if (format.value == value) return format;
    }
    return null;
  }
}

enum BookStatusUnknownReason { transient, formatNeedsAttention }

/// A user's per-format request state for a book. [formats] contains the server's
/// live/request-history projection and [ownership] is the current Chaptarr
/// digest. The two are reduced into one requester-facing truth by [statusFor].
/// A null result means the status could not be checked; it must never be treated
/// as "not requested" because doing so could create a duplicate request.
class BookRequestStatusDetail {
  final RequestStatus status;
  final Map<BookRequestFormat, RequestStatus> formats;
  final BookOwnership? ownership;
  final bool isKnown;
  final BookStatusUnknownReason? unknownReason;

  const BookRequestStatusDetail({
    this.status = RequestStatus.unavailable,
    this.formats = const {},
    this.ownership,
    this.isKnown = true,
    this.unknownReason,
  });

  BookStatusUnknownReason? get effectiveUnknownReason =>
      isKnown ? null : (unknownReason ?? BookStatusUnknownReason.transient);

  /// Returns a copy carrying library [ownership] (from the owned-books digest).
  /// A matched digest row whose format truth is unresolved fails the combined
  /// state closed even when the request-status endpoint itself responded.
  BookRequestStatusDetail withOwnership(
    BookOwnership? ownership, {
    bool ownershipStatusKnown = true,
  }) =>
      BookRequestStatusDetail(
        status: status,
        formats: formats,
        ownership: ownership,
        isKnown: isKnown && ownershipStatusKnown,
        unknownReason: !ownershipStatusKnown
            ? BookStatusUnknownReason.formatNeedsAttention
            : unknownReason,
      );

  /// User-facing state precedence: a file is Available; an active queue item is
  /// Downloading; a monitored record is Requested; only then do pending/denied
  /// history states apply. Live Chaptarr truth therefore heals stale request
  /// history without exposing arr vocabulary to requesters.
  RequestStatus? statusFor(BookRequestFormat format) {
    if (format == BookRequestFormat.both) return null;
    final owned = switch (format) {
      BookRequestFormat.ebook => ownership?.ebook,
      BookRequestFormat.audiobook => ownership?.audiobook,
      BookRequestFormat.both => null,
    };
    if (owned?.downloaded ?? false) return RequestStatus.available;

    final server = formats[format];
    if (server == RequestStatus.available ||
        server == RequestStatus.downloading ||
        server == RequestStatus.requested ||
        server == RequestStatus.partial) {
      return server == RequestStatus.partial
          ? RequestStatus.requested
          : server;
    }
    if (owned?.monitored ?? false) return RequestStatus.requested;
    if (server == RequestStatus.pending || server == RequestStatus.denied) {
      return server;
    }
    if (!isKnown) return null;
    return RequestStatus.unavailable;
  }

  /// "Both" is covered only when each concrete format is covered, possibly
  /// from different sources (for example an available eBook and a requested
  /// audiobook).
  bool isCovered(BookRequestFormat format) {
    if (format == BookRequestFormat.both) {
      return isCovered(BookRequestFormat.ebook) &&
          isCovered(BookRequestFormat.audiobook);
    }
    final state = statusFor(format);
    return state == RequestStatus.available ||
        state == RequestStatus.downloading ||
        state == RequestStatus.requested ||
        state == RequestStatus.pending;
  }

  bool isRequestable(BookRequestFormat format) {
    if (format == BookRequestFormat.both) {
      return isRequestable(BookRequestFormat.ebook) &&
          isRequestable(BookRequestFormat.audiobook);
    }
    final state = statusFor(format);
    return state == RequestStatus.unavailable || state == RequestStatus.denied;
  }

  /// Short requester-facing reason a format cannot be selected.
  String? coverageLabel(BookRequestFormat format) {
    if (!isCovered(format)) return null;
    if (format == BookRequestFormat.both) return 'Already covered';
    return statusFor(format)?.label;
  }
}

class RequestSubmissionException implements Exception {
  final String message;
  final bool definitive;

  const RequestSubmissionException(
    this.message, {
    this.definitive = false,
  });

  @override
  String toString() => message;
}

class BookRequestSubmission {
  final RequestStatus? status;
  final Map<BookRequestFormat, RequestStatus> formats;
  final bool isKnown;

  const BookRequestSubmission({
    required this.status,
    this.formats = const {},
    this.isKnown = true,
  });

  bool succeeded(BookRequestFormat format) {
    final state = formats[format];
    return switch (state) {
      RequestStatus.available ||
      RequestStatus.downloading ||
      RequestStatus.requested ||
      RequestStatus.pending ||
      RequestStatus.partial => true,
      RequestStatus.denied || RequestStatus.unavailable || null => false,
    };
  }
}

String _requestErrorMessage(DioException error) {
  final data = error.response?.data;
  String? raw;
  if (data is Map) {
    final message = data['error'] ?? data['message'];
    if (message is String && message.isNotEmpty) raw = message;
  }
  if (data is String && data.isNotEmpty) raw = data;
  final lower = raw?.toLowerCase() ?? '';
  if (lower.contains('no audiobook edition')) {
    return 'No audiobook edition is available for this book.';
  }
  if (lower.contains('no ebook edition')) {
    return 'No eBook edition is available for this book.';
  }
  if (lower.contains('root folder')) {
    return 'No library folder is available for this book format. Ask an admin to check the book settings.';
  }
  if (lower.contains('quality profile') ||
      lower.contains('metadata profile')) {
    return 'Ask an admin to check the book settings.';
  }
  if (lower.contains('book not found') || lower.contains('foreign id')) {
    return 'This book could not be matched in the library. Search for it again and retry.';
  }
  return 'This book could not be requested. Check the connection and try again.';
}

bool _requestErrorIsDefinitive(DioException error) {
  final data = error.response?.data;
  final raw = data is Map
      ? (data['error'] ?? data['message'])?.toString().toLowerCase() ?? ''
      : data?.toString().toLowerCase() ?? '';
  return raw.contains('no audiobook edition') ||
      raw.contains('no ebook edition') ||
      raw.contains('root folder') ||
      raw.contains('quality profile') ||
      raw.contains('metadata profile') ||
      raw.contains('book not found') ||
      raw.contains('foreign id');
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
      // An explicit season list makes the server monitor exactly these
      // seasons. It takes precedence over season_scope, so only send the
      // coarse scope when no explicit list was chosen.
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
  Future<RequestStatus> checkBookStatus(String foreignId,
          {String? instanceId}) async =>
      (await checkBookStatusDetail(foreignId, instanceId: instanceId)).status;

  /// Like [checkBookStatus] but also returns the per-format breakdown so the
  /// caller can still offer a not-yet-requested format. A failed lookup is
  /// returned as unknown so callers cannot turn an outage into a duplicate
  /// request affordance.
  Future<BookRequestStatusDetail> checkBookStatusDetail(
    String foreignId, {
    String? instanceId,
  }) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/book-status',
        queryParameters: {
          'foreign_id': foreignId,
          if (instanceId != null && instanceId.isNotEmpty)
            'instance_id': instanceId,
        },
      );
      final data = resp.data as Map<String, dynamic>;
      var isKnown = data['status_known'] as bool? ?? true;
      final BookStatusUnknownReason? unknownReason = isKnown
          ? null
          : BookStatusUnknownReason.formatNeedsAttention;
      RequestStatus? parseStatus(Object? value) {
        for (final status in RequestStatus.values) {
          if (status.name == value?.toString()) return status;
        }
        return null;
      }

      final status = parseStatus(data['status']);
      if (status == null) isKnown = false;
      final formats = <BookRequestFormat, RequestStatus>{};
      RequestStatus? bothStatus;
      final raw = data['book_formats'];
      if (raw is Map) {
        raw.forEach((key, value) {
          final fmt = BookRequestFormat.tryFromValue(key.toString());
          if (fmt == null) {
            isKnown = false;
            return;
          }
          final parsed = parseStatus(value);
          if (parsed == null) {
            isKnown = false;
            return;
          }
          if (fmt == BookRequestFormat.both) {
            bothStatus = parsed;
          } else {
            formats[fmt] = parsed;
          }
        });
      }
      // Older servers can return one stored "both" request instead of concrete
      // format states. Expand it without overwriting newer per-format truth.
      final legacyBoth = bothStatus;
      if (legacyBoth != null) {
        formats.putIfAbsent(BookRequestFormat.ebook, () => legacyBoth);
        formats.putIfAbsent(BookRequestFormat.audiobook, () => legacyBoth);
      }
      // An aggregate non-empty state without concrete format truth is not safe
      // to turn into two request buttons. Older/malformed responses cannot tell
      // us whether eBook, Audiobook, or both are already covered.
      if (formats.isEmpty &&
          status != null &&
          status != RequestStatus.unavailable) {
        isKnown = false;
      }
      return BookRequestStatusDetail(
        status: status ?? RequestStatus.unavailable,
        formats: formats,
        isKnown: isKnown,
        unknownReason: unknownReason,
      );
    } catch (_) {
      return const BookRequestStatusDetail(isKnown: false);
    }
  }

  /// Submit a book request. Books are keyed by the foreignBookId, not a tmdb_id;
  /// the backend adds the book to the user's granted Chaptarr instance (after
  /// approval when the user's policy requires it). Returns the resulting status
  /// (e.g. [RequestStatus.pending]) or null on non-HTTP failure.
  Future<BookRequestSubmission?> requestBook({
    required String foreignId,
    required String title,
    BookRequestFormat format = BookRequestFormat.both,
    String? instanceId,
  }) async {
    try {
      final resp = await _backendDio.post('/api/requests', data: {
        'media_type': 'book',
        'foreign_id': foreignId,
        'title': title,
        'book_format': format.value,
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      });
      if (resp.statusCode != 200 && resp.statusCode != 201) return null;
      final data = resp.data as Map<String, dynamic>?;
      RequestStatus? parseStatus(Object? value) {
        for (final candidate in RequestStatus.values) {
          if (candidate.name == value?.toString()) return candidate;
        }
        return null;
      }

      final status = parseStatus(data?['status']);
      var isKnown = status != null;
      final formats = <BookRequestFormat, RequestStatus>{};
      final rawFormats = data?['book_formats'];
      if (rawFormats is Map) {
        rawFormats.forEach((key, value) {
          final parsedFormat = BookRequestFormat.tryFromValue(key.toString());
          if (parsedFormat == null || parsedFormat == BookRequestFormat.both) {
            isKnown = false;
            return;
          }
          final parsedStatus = parseStatus(value);
          if (parsedStatus == null) {
            isKnown = false;
            return;
          }
          formats[parsedFormat] = parsedStatus;
        });
      }
      if (status == RequestStatus.partial) {
        final expected = format == BookRequestFormat.both
            ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
            : [format];
        if (expected.any((requested) => !formats.containsKey(requested))) {
          isKnown = false;
        }
      }
      return BookRequestSubmission(
        status: status,
        formats: formats,
        isKnown: isKnown,
      );
    } on DioException catch (e) {
      throw RequestSubmissionException(
        _requestErrorMessage(e),
        definitive: _requestErrorIsDefinitive(e),
      );
    } catch (_) {
      return null;
    }
  }
}
