import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';

/// Discovery state for the Movies tab.
class MovieDiscoverState {
  final List<MediaItem> popularMovies;
  final List<MediaItem> topRated;
  final List<MediaItem> upcoming;
  final List<MediaItem> anticipated;
  final bool isLoadingPopular;
  final bool isLoadingTopRated;
  final bool isLoadingUpcoming;
  final bool isLoadingAnticipated;

  const MovieDiscoverState({
    this.popularMovies = const [],
    this.topRated = const [],
    this.upcoming = const [],
    this.anticipated = const [],
    this.isLoadingPopular = false,
    this.isLoadingTopRated = false,
    this.isLoadingUpcoming = false,
    this.isLoadingAnticipated = false,
  });

  MovieDiscoverState copyWith({
    List<MediaItem>? popularMovies,
    List<MediaItem>? topRated,
    List<MediaItem>? upcoming,
    List<MediaItem>? anticipated,
    bool? isLoadingPopular,
    bool? isLoadingTopRated,
    bool? isLoadingUpcoming,
    bool? isLoadingAnticipated,
  }) =>
      MovieDiscoverState(
        popularMovies: popularMovies ?? this.popularMovies,
        topRated: topRated ?? this.topRated,
        upcoming: upcoming ?? this.upcoming,
        anticipated: anticipated ?? this.anticipated,
        isLoadingPopular: isLoadingPopular ?? this.isLoadingPopular,
        isLoadingTopRated: isLoadingTopRated ?? this.isLoadingTopRated,
        isLoadingUpcoming: isLoadingUpcoming ?? this.isLoadingUpcoming,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// Fetches movie discovery rows (Popular, Top Rated, Coming Soon).
class MovieDiscoverNotifier extends StateNotifier<MovieDiscoverState> {
  final DiscoverApiService _api;

  MovieDiscoverNotifier(this._api) : super(const MovieDiscoverState());

  Future<void> bootstrap() async {
    await Future.wait([
      _fetchPopularMovies(),
      _fetchTopRatedMovies(),
      _fetchUpcomingMovies(),
      _fetchAnticipatedMovies(),
    ]);
  }

  Future<void> _fetchPopularMovies() async {
    state = state.copyWith(isLoadingPopular: true);
    try {
      final page = await _api.fetchPopularMovies();
      state = state.copyWith(
        popularMovies: page.results,
        isLoadingPopular: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingPopular: false);
    }
  }

  Future<void> _fetchTopRatedMovies() async {
    state = state.copyWith(isLoadingTopRated: true);
    try {
      final page = await _api.fetchTopRatedMovies();
      state = state.copyWith(
        topRated: page.results,
        isLoadingTopRated: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingTopRated: false);
    }
  }

  Future<void> _fetchUpcomingMovies() async {
    state = state.copyWith(isLoadingUpcoming: true);
    try {
      final page = await _api.fetchUpcomingMovies();
      state = state.copyWith(
        upcoming: page.results,
        isLoadingUpcoming: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingUpcoming: false);
    }
  }

  Future<void> _fetchAnticipatedMovies() async {
    state = state.copyWith(isLoadingAnticipated: true);
    try {
      final items = await _api.getTraktAnticipated('movies');
      state = state.copyWith(
        anticipated: items.map((t) => t.toMediaItem()).toList(),
        isLoadingAnticipated: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingAnticipated: false);
    }
  }
}

/// Provider for movie discovery data.
final movieDiscoverProvider =
    StateNotifierProvider<MovieDiscoverNotifier, MovieDiscoverState>(
  (ref) {
    final api = ref.watch(discoverServiceProvider);
    return MovieDiscoverNotifier(api);
  },
);
