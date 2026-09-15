import 'package:flutter/foundation.dart';
import 'package:dio/dio.dart' show DioException;
import '../../discover/data/tmdb_models.dart';
import '../data/request_service.dart';
import '../data/tv_match_service.dart';

/// State for a single media item's request status.
class RequestState {
  final bool libraryUnavailable;
  final TVMatch? match;
  final List<String> deliveryMessages;
  final Set<String> unknownInstanceIds;
  final RequestStatus status;
  final bool isRequesting;
  final bool isCheckingStatus;

  /// The selected library has a successful status read. A failed refresh
  /// leaves this false, while retaining the last known status for display.
  final bool hasStatus;
  final String? error;
  // Refused requests use a toast, without turning the title into an error.
  final String? quotaMessage;

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
    this.libraryUnavailable = false,
    this.match,
    this.deliveryMessages = const [],
    this.unknownInstanceIds = const {},
    this.status = RequestStatus.unavailable,
    this.isRequesting = false,
    this.isCheckingStatus = false,
    this.hasStatus = false,
    this.error,
    this.quotaMessage,
    this.seasons = const [],
    this.releases = MovieReleaseDates.none,
    this.instanceStatuses = const {},
  });

  /// A report becomes relevant after an accepted request or a completed
  /// failure, including a match that blocks submission. Loading alone is not
  /// a failure; neither is opening/cancelling options or a denied approval.
  bool get canReportProblem => switch (status) {
        RequestStatus.pending ||
        RequestStatus.requested ||
        RequestStatus.downloading ||
        RequestStatus.partial ||
        RequestStatus.available => true,
        _ => !isCheckingStatus && !isRequesting &&
            ((error?.trim().isNotEmpty ?? false) ||
                (match != null && !match!.isResolved)),
      };

  RequestState copyWith({
    bool? libraryUnavailable,
    TVMatch? match,
    List<String>? deliveryMessages,
    Set<String>? unknownInstanceIds,
    RequestStatus? status,
    bool? isRequesting,
    bool? isCheckingStatus,
    bool? hasStatus,
    String? error,
    String? quotaMessage,
    List<RequestSeasonStatus>? seasons,
    MovieReleaseDates? releases,
    Map<String, RequestStatus>? instanceStatuses,
  }) =>
      RequestState(
        libraryUnavailable: libraryUnavailable ?? this.libraryUnavailable,
        match: match ?? this.match,
        deliveryMessages: deliveryMessages ?? this.deliveryMessages,
        unknownInstanceIds: unknownInstanceIds ?? this.unknownInstanceIds,
        status: status ?? this.status,
        isRequesting: isRequesting ?? this.isRequesting,
        isCheckingStatus: isCheckingStatus ?? this.isCheckingStatus,
        hasStatus: hasStatus ?? this.hasStatus,
        error: error,
        quotaMessage: quotaMessage,
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
  int _statusVersion = 0;
  int _libraryVersion = 0;
  bool _disposed = false;

  RequestState _state = const RequestState();
  RequestState get state => _state;
  int get tmdbId => _tmdbId;
  set state(RequestState value) {
    _state = value;
    notifyListeners();
  }

  /// The library this screen currently reads and requests against; null means
  /// the user's default. Set by the detail screen when the user picks a
  /// library chip or a Library option on the request sheet, so every
  /// subsequent status check and submit follows the same selection.
  String? _instanceId;
  String? get instanceId => _instanceId;
  set instanceId(String? value) {
    if (_instanceId == value) return;
    _instanceId = value;
    _libraryVersion++;
    _statusVersion++;
    // Never let a newly selected library use the previous library's seasons.
    state = RequestState(instanceStatuses: state.instanceStatuses,
        unknownInstanceIds: state.unknownInstanceIds);
  }

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
    final version = ++_statusVersion;
    state = state.copyWith(isCheckingStatus: true, hasStatus: false);
    try {
      final detail = await _service.checkStatusDetail(
        _tmdbId,
        _mediaType,
        instanceId: instanceId,
      );
      if (_disposed || version != _statusVersion) return;
      state = state.copyWith(
        libraryUnavailable: false,
        status: detail.status,
        seasons: detail.seasons,
        releases: detail.releases,
        instanceStatuses: detail.instanceStatuses,
        unknownInstanceIds: detail.unknownInstanceIds,
        match: detail.match,
        deliveryMessages: detail.deliveryMessages,
        error: detail.statusMessage,
        isCheckingStatus: false,
        hasStatus: detail.isKnown && (detail.match?.isResolved ?? true),
      );
    } catch (e) {
      if (_disposed || version != _statusVersion) return;
      state = state.copyWith(
        // The status endpoint uses 400 for a deleted/wrong-type instance
        // and 403 for a revoked grant. A 404 may be a hidden title instead.
        libraryUnavailable: instanceId != null && e is DioException &&
            (e.response?.statusCode == 400 || e.response?.statusCode == 403),
        isCheckingStatus: false,
        error: 'Could not check status',
      );
    }
  }

  /// Fetch the option set the current user may choose for this item, scoped
  /// to [libraryId] (defaults to the current selection).
  Future<RequestOptions?> fetchOptions({String? libraryId}) =>
      _service.fetchOptions(_mediaType, instanceId: libraryId ?? instanceId);

  /// Submit the request, optionally with chosen season scope / quality. The
  /// resulting status (which may be [RequestStatus.pending]) is reflected in
  /// state rather than assuming "requested". Returns whether the write was
  /// accepted, separately from a later status-refresh error. TV requests keep
  /// selection blocked until their updated season breakdown has been read.
  Future<bool> request({
    String? title,
    int? tvdbId,
    String? seasonScope,
    List<int>? seasons,
    int? qualityProfileId,
  }) async {
    if (state.isRequesting) return false;
    if (_mediaType == MediaType.tv && !state.hasStatus) return false;
    final libraryVersion = _libraryVersion;
    // A read started before this write cannot overwrite the accepted result.
    _statusVersion++;
    state = state.copyWith(
      isRequesting: true,
      isCheckingStatus: false,
      error: null,
    );

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

    if (_disposed || libraryVersion != _libraryVersion) return status != null;
    _statusVersion++;
    if (status == null) {
      state = state.copyWith(
        isRequesting: false,
        isCheckingStatus: false,
        error: _service.lastRequestQuotaExceeded
            ? null
            : _service.lastRequestError ?? 'Request failed. Please try again.',
        quotaMessage: _service.lastRequestQuotaExceeded ? _service.lastRequestError : null,
      );
      return false;
    }
    state = state.copyWith(status: status, isCheckingStatus: false);
    if (_mediaType == MediaType.tv) await checkStatus();
    if (_disposed || libraryVersion != _libraryVersion) return true;
    state = state.copyWith(isRequesting: false, error: state.error);
    return true;
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}
