import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';

/// Discovery state for the TV Shows tab.
class TvDiscoverState {
  final List<MediaItem> popularTV;
  final List<MediaItem> anticipated;
  final bool isLoadingPopular;
  final bool isLoadingAnticipated;

  const TvDiscoverState({
    this.popularTV = const [],
    this.anticipated = const [],
    this.isLoadingPopular = false,
    this.isLoadingAnticipated = false,
  });

  TvDiscoverState copyWith({
    List<MediaItem>? popularTV,
    List<MediaItem>? anticipated,
    bool? isLoadingPopular,
    bool? isLoadingAnticipated,
  }) =>
      TvDiscoverState(
        popularTV: popularTV ?? this.popularTV,
        anticipated: anticipated ?? this.anticipated,
        isLoadingPopular: isLoadingPopular ?? this.isLoadingPopular,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// Fetches TV discovery rows (Popular TV Shows).
class TvDiscoverNotifier extends StateNotifier<TvDiscoverState> {
  final DiscoverApiService _api;

  TvDiscoverNotifier(this._api) : super(const TvDiscoverState());

  Future<void> bootstrap() async {
    await Future.wait([
      _fetchPopularTV(),
      _fetchAnticipatedShows(),
    ]);
  }

  Future<void> _fetchPopularTV() async {
    state = state.copyWith(isLoadingPopular: true);
    try {
      final page = await _api.fetchPopularTV();
      state = state.copyWith(
        popularTV: page.results,
        isLoadingPopular: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingPopular: false);
    }
  }

  Future<void> _fetchAnticipatedShows() async {
    state = state.copyWith(isLoadingAnticipated: true);
    try {
      final items = await _api.getTraktAnticipated('shows');
      state = state.copyWith(
        anticipated: items.map((t) => t.toMediaItem()).toList(),
        isLoadingAnticipated: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingAnticipated: false);
    }
  }
}

/// Provider for TV discovery data.
final tvDiscoverProvider =
    StateNotifierProvider<TvDiscoverNotifier, TvDiscoverState>(
  (ref) {
    final api = ref.watch(discoverServiceProvider);
    return TvDiscoverNotifier(api);
  },
);
