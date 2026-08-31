import 'package:flutter/foundation.dart';
import '../../discover/data/tmdb_models.dart';
import '../data/request_service.dart';

/// State for a single media item's request status.
class RequestState {
  final RequestStatus status;
  final bool isRequesting;
  final String? error;

  /// Per-season availability for TV titles (empty for movies or series not yet
  /// in the library). Drives the interactive season table.
  final List<RequestSeasonStatus> seasons;

  /// Theatrical/digital release dates for a movie already in the library, so
  /// the detail screen can say a title is not out yet rather than leaving a
  /// "Requested" badge looking like a stalled download.
  ///
  /// Never null — an absent payload is [MovieReleaseDates.none] — so a refetch
  /// that no longer carries dates overwrites the old ones through [copyWith]
  /// instead of leaving them stranded.
  final MovieReleaseDates releases;

  /// Per-granted-library status chips, keyed by instance id. Empty unless the
  /// user holds more than one granted library for this media type — the
  /// server omits the map for everyone else.
  final Map<String, RequestStatus> instanceStatuses;

  const RequestState({
    this.status = RequestStatus.unavailable,
    this.isRequesting = false,
    this.error,
    this.seasons = const [],
    this.releases = MovieReleaseDates.none,
    this.instanceStatuses = const {},
  });

  RequestState copyWith({
    RequestStatus? status,
    bool? isRequesting,
    String? error,
    List<RequestSeasonStatus>? seasons,
    MovieReleaseDates? releases,
    Map<String, RequestStatus>? instanceStatuses,
  }) =>
      RequestState(
        status: status ?? this.status,
        isRequesting: isRequesting ?? this.isRequesting,
        error: error,
        seasons: seasons ?? this.seasons,
        releases: releases ?? this.releases,
        instanceStatuses: instanceStatuses ?? this.instanceStatuses,
      );
}

/// Manages request status checking and one-tap requesting.
class RequestNotifier extends ChangeNotifier {
  final RequestService _service;
  final int _tmdbId;
  final MediaType _mediaType;

  RequestState _state = const RequestState();
  RequestState get state => _state;
  set state(RequestState value) {
    _state = value;
    notifyListeners();
  }

  /// The library this screen currently reads and requests against; null means
  /// the user's default. Set by the detail screen when the user picks a
  /// library chip or a Library option on the request sheet, so every
  /// subsequent status check and submit follows the same selection.
  String? instanceId;

  RequestNotifier({
    required RequestService service,
    required int tmdbId,
    required MediaType mediaType,
  })  : _service = service,
        _tmdbId = tmdbId,
        _mediaType = mediaType;

  /// Check current status from the backend, including the per-season breakdown
  /// for TV titles.
  Future<void> checkStatus() async {
    try {
      final detail = await _service.checkStatusDetail(
        _tmdbId,
        _mediaType,
        instanceId: instanceId,
      );
      state = state.copyWith(
        status: detail.status,
        seasons: detail.seasons,
        releases: detail.releases,
        instanceStatuses: detail.instanceStatuses,
      );
    } catch (e) {
      state = state.copyWith(error: 'Could not check status');
    }
  }

  /// Fetch the option set the current user may choose for this item, scoped
  /// to [libraryId] (defaults to the current selection).
  Future<RequestOptions?> fetchOptions({String? libraryId}) =>
      _service.fetchOptions(_mediaType, instanceId: libraryId ?? instanceId);

  /// Submit the request, optionally with chosen season scope / quality. The
  /// resulting status (which may be [RequestStatus.pending]) is reflected in
  /// state rather than assuming "requested".
  Future<void> request({
    String? title,
    int? tvdbId,
    String? seasonScope,
    List<int>? seasons,
    int? qualityProfileId,
  }) async {
    if (state.isRequesting) return;
    state = state.copyWith(isRequesting: true, error: null);

    final status = await _service.request(
      tmdbId: _tmdbId,
      mediaType: _mediaType,
      title: title,
      tvdbId: tvdbId,
      seasonScope: seasonScope,
      seasons: seasons,
      qualityProfileId: qualityProfileId,
      instanceId: instanceId,
    );

    if (status != null) {
      state = state.copyWith(
        status: status,
        isRequesting: false,
      );
    } else {
      state = state.copyWith(
        isRequesting: false,
        error: 'Request failed. Please try again.',
      );
    }
  }
}
