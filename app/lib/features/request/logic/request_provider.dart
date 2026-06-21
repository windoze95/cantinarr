import 'package:flutter/foundation.dart';
import '../../discover/data/tmdb_models.dart';
import '../data/request_service.dart';

/// State for a single media item's request status.
class RequestState {
  final RequestStatus status;
  final bool isRequesting;
  final String? error;

  const RequestState({
    this.status = RequestStatus.unavailable,
    this.isRequesting = false,
    this.error,
  });

  RequestState copyWith({
    RequestStatus? status,
    bool? isRequesting,
    String? error,
  }) =>
      RequestState(
        status: status ?? this.status,
        isRequesting: isRequesting ?? this.isRequesting,
        error: error,
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

  RequestNotifier({
    required RequestService service,
    required int tmdbId,
    required MediaType mediaType,
  })  : _service = service,
        _tmdbId = tmdbId,
        _mediaType = mediaType;

  /// Check current status from the backend.
  Future<void> checkStatus() async {
    try {
      final status = await _service.checkStatus(_tmdbId, _mediaType);
      state = state.copyWith(status: status);
    } catch (e) {
      state = state.copyWith(error: 'Could not check status');
    }
  }

  /// Fetch the option set the current user may choose for this item.
  Future<RequestOptions?> fetchOptions() => _service.fetchOptions(_mediaType);

  /// Submit the request, optionally with chosen season scope / quality. The
  /// resulting status (which may be [RequestStatus.pending]) is reflected in
  /// state rather than assuming "requested".
  Future<void> request({
    String? title,
    int? tvdbId,
    String? seasonScope,
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
      qualityProfileId: qualityProfileId,
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
