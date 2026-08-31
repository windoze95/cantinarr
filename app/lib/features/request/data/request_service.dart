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
  partial('Partial', 'Request More'),

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

/// Why a book format reads as Requested while the library still has no record
/// of it. The server owns these requests and retries them itself, so they are
/// neither an approval anyone will make nor a failure anyone must fix.
enum BookWaitReason {
  /// The library's metadata service is still importing the book's author, so
  /// the add cannot be made yet. It completes on its own.
  authorImport('author_import'),

  /// A wait this app version has no words for. Still a wait: the format stays
  /// covered and unrequestable, and the generic copy says what is knowable.
  unknown('');

  const BookWaitReason(this.value);
  final String value;

  static BookWaitReason fromValue(String? value) {
    for (final reason in BookWaitReason.values) {
      if (reason != BookWaitReason.unknown && reason.value == value) {
        return reason;
      }
    }
    return BookWaitReason.unknown;
  }
}

/// The durable explanation behind a waiting format.
///
/// Before this existed the only word for the state was "Requested", which
/// promises a library record that is not there — leaving an active retry loop
/// and an app that silently dropped the request looking exactly alike.
class BookFormatWait {
  final BookWaitReason reason;

  /// When the request was accepted, for surfaces that show how long this has
  /// been going. Null only if an older or malformed payload omitted it.
  final DateTime? waitingSince;

  /// When the server last actually retried. Null means the server could not
  /// vouch for an attempt (it restarted since this request was parked) — not
  /// that nothing has been tried.
  final DateTime? lastAttemptAt;

  const BookFormatWait({
    required this.reason,
    this.waitingSince,
    this.lastAttemptAt,
  });

  /// The pill that replaces "Requested" — the whole point is that the requester
  /// can tell the two states apart at a glance.
  String get label => 'Waiting for library';

  /// The persistent explanation. It says what is happening, who is doing it,
  /// and that the requester is not the one being waited on. No ETA is offered
  /// because none is knowable.
  String get explanation => switch (reason) {
        BookWaitReason.authorImport =>
          'Your request is saved. The library is still adding this author. '
              'Cantinarr keeps retrying automatically — no action is needed.',
        BookWaitReason.unknown =>
          'Your request is saved. The library isn’t ready for this book yet. '
              'Cantinarr keeps retrying automatically — no action is needed.',
      };

  static BookFormatWait? tryParse(Object? value) {
    if (value is! Map) return null;
    final reason = value['reason'];
    return BookFormatWait(
      reason: BookWaitReason.fromValue(reason is String ? reason : null),
      waitingSince: _tryParseTimestamp(value['waiting_since']),
      lastAttemptAt: _tryParseTimestamp(value['last_attempt_at']),
    );
  }
}

DateTime? _tryParseTimestamp(Object? value) {
  if (value is! String || value.isEmpty) return null;
  return DateTime.tryParse(value)?.toLocal();
}

/// Reads a `book_format_waits` map, expanding a "both" entry the way stored
/// "both" request rows are expanded elsewhere. An unreadable entry is dropped
/// rather than failing the whole status closed: a wait explains a state the
/// caller already has from `book_formats`, so losing it costs an explanation,
/// never coverage.
Map<BookRequestFormat, BookFormatWait> _parseFormatWaits(Object? raw) {
  final waits = <BookRequestFormat, BookFormatWait>{};
  if (raw is! Map) return waits;
  BookFormatWait? both;
  raw.forEach((key, value) {
    final format = BookRequestFormat.tryFromValue(key.toString());
    final wait = BookFormatWait.tryParse(value);
    if (format == null || wait == null) return;
    if (format == BookRequestFormat.both) {
      both = wait;
    } else {
      waits[format] = wait;
    }
  });
  final shared = both;
  if (shared != null) {
    waits.putIfAbsent(BookRequestFormat.ebook, () => shared);
    waits.putIfAbsent(BookRequestFormat.audiobook, () => shared);
  }
  return waits;
}

/// A user's per-format request state for a book. [formats] contains the server's
/// live/request-history projection and [ownership] is the current Chaptarr
/// digest. The two are reduced into one requester-facing truth by [statusFor].
/// A null result means the status could not be checked; it must never be treated
/// as "not requested" because doing so could create a duplicate request.
class BookRequestStatusDetail {
  final RequestStatus status;
  final Map<BookRequestFormat, RequestStatus> formats;

  /// Per-format explanations for a [RequestStatus.requested] the library cannot
  /// back with a record yet. Empty on older servers, which is why a missing
  /// wait must never be read as "definitely not waiting" — only as "no reason
  /// was given", which is what the app showed before this existed.
  final Map<BookRequestFormat, BookFormatWait> formatWaits;
  final BookOwnership? ownership;
  final bool isKnown;
  final BookStatusUnknownReason? unknownReason;

  /// The foreignBookId the library files this book under today, when it
  /// differs from the id the status lookup used. Chaptarr can re-key a created
  /// record to its own canonical id; the server resolves the request through
  /// the stored record id and reports the live id here so screens can follow.
  final String? canonicalForeignId;

  const BookRequestStatusDetail({
    this.status = RequestStatus.unavailable,
    this.formats = const {},
    this.formatWaits = const {},
    this.ownership,
    this.isKnown = true,
    this.unknownReason,
    this.canonicalForeignId,
  });

  BookStatusUnknownReason? get effectiveUnknownReason =>
      isKnown ? null : (unknownReason ?? BookStatusUnknownReason.transient);

  /// The wait to show for [format], or null when there is nothing to explain.
  ///
  /// Gated on the reduced state rather than on the raw map, so every source of
  /// live truth — including the ownership digest, which the server never saw —
  /// retires the explanation at the same moment it retires the absence.
  BookFormatWait? waitFor(BookRequestFormat format) {
    if (format == BookRequestFormat.both) return null;
    if (statusFor(format) != RequestStatus.requested) return null;
    return formatWaits[format];
  }

  /// Whether any concrete format is waiting on the library.
  bool get hasFormatWait =>
      waitFor(BookRequestFormat.ebook) != null ||
      waitFor(BookRequestFormat.audiobook) != null;

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
        formatWaits: formatWaits,
        ownership: ownership,
        isKnown: isKnown && ownershipStatusKnown,
        unknownReason: !ownershipStatusKnown
            ? BookStatusUnknownReason.formatNeedsAttention
            : unknownReason,
        canonicalForeignId: canonicalForeignId,
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

  /// A server explanation for an outcome the status alone would misrepresent —
  /// today, a book parked for an admin because the library couldn't match it.
  /// Empty when the status speaks for itself.
  final String message;

  /// The durable form of [message] for a format the server is retrying itself.
  /// [message] is a one-shot toast; this is what the row keeps saying after the
  /// toast is gone, and what carries the state through the re-read that follows
  /// a submission before the server has caught up.
  final Map<BookRequestFormat, BookFormatWait> formatWaits;

  const BookRequestSubmission({
    required this.status,
    this.formats = const {},
    this.isKnown = true,
    this.message = '',
    this.formatWaits = const {},
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
  if (_requestErrorIsDefinitive(error)) {
    return 'The library could not complete this request. Try again later.';
  }
  return 'This book could not be requested. Check the connection and try again.';
}

bool _requestErrorIsDefinitive(DioException error) {
  // The server rejects a book create atomically, so an answered request is a
  // definitive "nothing was submitted" and its message is worth showing.
  // No response (timeout, connection drop) and gateway statuses (a proxy
  // answering for a server that may still be working) leave the outcome
  // genuinely unknown — only those fall back to the couldn't-confirm toast.
  final status = error.response?.statusCode;
  if (status == null) return false;
  return status < 502 || status > 504;
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

/// A movie's theatrical and digital release dates, mirroring the backend
/// `StatusResponse.releases` payload. Only movies already in the library carry
/// them; everything else is [none].
///
/// These are calendar dates, not instants. The backend sends plain `YYYY-MM-DD`
/// precisely so nothing localises them — a release date converted across time
/// zones lands a day early or late — so they are parsed component-wise and must
/// never be run through `toLocal()`.
class MovieReleaseDates {
  final DateTime? inCinemas;
  final DateTime? digital;

  const MovieReleaseDates({this.inCinemas, this.digital});

  /// The absence of dates: either the title isn't in the library or the arr
  /// knows neither date.
  static const MovieReleaseDates none = MovieReleaseDates();

  bool get isEmpty => inCinemas == null && digital == null;

  factory MovieReleaseDates.fromJson(Map<String, dynamic> json) =>
      MovieReleaseDates(
        inCinemas: _parseCalendarDate(json['in_cinemas'] as String?),
        digital: _parseCalendarDate(json['digital'] as String?),
      );
}

/// Parses a `YYYY-MM-DD` calendar date into local midnight, ignoring anything
/// after the date part. Returns null for a missing or unparseable value.
DateTime? _parseCalendarDate(String? value) {
  if (value == null || value.length < 10) return null;
  final year = int.tryParse(value.substring(0, 4));
  final month = int.tryParse(value.substring(5, 7));
  final day = int.tryParse(value.substring(8, 10));
  if (year == null || month == null || day == null) return null;
  return DateTime(year, month, day);
}

/// The full request status for a title: the overall [status] plus, for TV, the
/// per-season breakdown (empty for movies or series not in the library) and,
/// for movies in the library, the [releases] dates. When the user is granted
/// more than one library for the media type, [instanceStatuses] carries each
/// granted library's own status so the screen can show one chip per library.
class RequestStatusDetail {
  final RequestStatus status;
  final List<RequestSeasonStatus> seasons;
  final MovieReleaseDates releases;
  final Map<String, RequestStatus> instanceStatuses;

  const RequestStatusDetail({
    this.status = RequestStatus.unavailable,
    this.seasons = const [],
    this.releases = MovieReleaseDates.none,
    this.instanceStatuses = const {},
  });

  factory RequestStatusDetail.fromJson(Map<String, dynamic> json) {
    final statusName = json['status'] as String? ?? 'unavailable';
    final releases = json['releases'];
    final rawInstanceStatuses = json['instance_statuses'];
    final instanceStatuses = <String, RequestStatus>{};
    if (rawInstanceStatuses is Map<String, dynamic>) {
      for (final entry in rawInstanceStatuses.entries) {
        final value = entry.value;
        if (value is! Map<String, dynamic>) continue;
        final name = value['status'] as String? ?? 'unavailable';
        instanceStatuses[entry.key] = RequestStatus.values.firstWhere(
          (s) => s.name == name,
          orElse: () => RequestStatus.unavailable,
        );
      }
    }
    return RequestStatusDetail(
      status: RequestStatus.values.firstWhere(
        (s) => s.name == statusName,
        orElse: () => RequestStatus.unavailable,
      ),
      seasons: ((json['seasons'] as List?) ?? const [])
          .map((e) => RequestSeasonStatus.fromJson(e as Map<String, dynamic>))
          .toList(),
      releases: releases is Map<String, dynamic>
          ? MovieReleaseDates.fromJson(releases)
          : MovieReleaseDates.none,
      instanceStatuses: instanceStatuses,
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
  /// (TV only). An [instanceId] scopes the read to that granted library; null
  /// reads the user's default. Falls back to an unavailable detail with no
  /// seasons on error.
  Future<RequestStatusDetail> checkStatusDetail(
    int tmdbId,
    MediaType mediaType, {
    String? instanceId,
  }) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/$tmdbId/status',
        queryParameters: {
          'media_type': mediaType.name,
          if (instanceId != null && instanceId.isNotEmpty)
            'instance_id': instanceId,
        },
      );
      return RequestStatusDetail.fromJson(resp.data as Map<String, dynamic>);
    } catch (_) {
      return const RequestStatusDetail();
    }
  }

  /// Fetch the option set the current user may choose for [mediaType]. An
  /// [instanceId] scopes the quality profiles to that library (profiles live
  /// inside an instance, so a sibling's ids would be meaningless). Returns
  /// null on error (the caller then submits with no options).
  Future<RequestOptions?> fetchOptions(
    MediaType mediaType, {
    String? instanceId,
  }) async {
    try {
      final resp = await _backendDio.get(
        '/api/requests/options',
        queryParameters: {
          'media_type': mediaType.name,
          if (instanceId != null && instanceId.isNotEmpty)
            'instance_id': instanceId,
        },
      );
      return RequestOptions.fromJson(resp.data as Map<String, dynamic>);
    } catch (_) {
      return null;
    }
  }

  /// Submit a request for a media item. Returns the resulting [RequestStatus]
  /// (e.g. [RequestStatus.pending] when approval is required), or null on
  /// failure. An [instanceId] routes the request to that granted library, the
  /// same wire field book requests already use.
  Future<RequestStatus?> request({
    required int tmdbId,
    required MediaType mediaType,
    String? title,
    int? tvdbId,
    String? seasonScope,
    List<int>? seasons,
    int? qualityProfileId,
    String? instanceId,
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
      if (instanceId != null && instanceId.isNotEmpty) {
        body['instance_id'] = instanceId;
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
      final rawCanonical = data['canonical_foreign_id'];
      final canonical =
          rawCanonical is String && rawCanonical.trim().isNotEmpty
              ? rawCanonical.trim()
              : null;
      return BookRequestStatusDetail(
        status: status ?? RequestStatus.unavailable,
        formats: formats,
        formatWaits: _parseFormatWaits(data['book_format_waits']),
        isKnown: isKnown,
        unknownReason: unknownReason,
        canonicalForeignId: canonical,
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
    String? searchTerm,
  }) async {
    try {
      final term = searchTerm?.trim() ?? '';
      final resp = await _backendDio.post('/api/requests', data: {
        'media_type': 'book',
        'foreign_id': foreignId,
        'title': title,
        'book_format': format.value,
        // The server has to find this book's metadata record again to add it,
        // and Chaptarr's lookup is a fuzzy text search — so hand it the term
        // that already produced this row instead of making it guess from the
        // title. Absent for notification/deep-link arrivals, which had no search.
        if (term.isNotEmpty) 'search_term': term,
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
      final rawMessage = data?['message'];
      return BookRequestSubmission(
        status: status,
        formats: formats,
        isKnown: isKnown,
        message: rawMessage is String ? rawMessage.trim() : '',
        formatWaits: _parseFormatWaits(data?['book_format_waits']),
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
