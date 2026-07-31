import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';

/// Discovery state for the Movies tab.
class MovieDiscoverState {
  /// The headline row, from whichever feed the admin configured.
  final List<MediaItem> featured;

  /// Which feed answered, so the row can title itself honestly.
  final String featuredSource;
  final List<MediaItem> topRated;
  final List<MediaItem> upcoming;
  final List<MediaItem> anticipated;
  final bool isLoadingFeatured;
  final bool isLoadingTopRated;
  final bool isLoadingUpcoming;
  final bool isLoadingAnticipated;

  const MovieDiscoverState({
    this.featured = const [],
    this.featuredSource = '',
    this.topRated = const [],
    this.upcoming = const [],
    this.anticipated = const [],
    this.isLoadingFeatured = false,
    this.isLoadingTopRated = false,
    this.isLoadingUpcoming = false,
    this.isLoadingAnticipated = false,
  });

  /// The row's title for the feed that answered.
  String get featuredTitle => featuredRowTitle(featuredSource, isTv: false);

  MovieDiscoverState copyWith({
    List<MediaItem>? featured,
    String? featuredSource,
    List<MediaItem>? topRated,
    List<MediaItem>? upcoming,
    List<MediaItem>? anticipated,
    bool? isLoadingFeatured,
    bool? isLoadingTopRated,
    bool? isLoadingUpcoming,
    bool? isLoadingAnticipated,
  }) =>
      MovieDiscoverState(
        featured: featured ?? this.featured,
        featuredSource: featuredSource ?? this.featuredSource,
        topRated: topRated ?? this.topRated,
        upcoming: upcoming ?? this.upcoming,
        anticipated: anticipated ?? this.anticipated,
        isLoadingFeatured: isLoadingFeatured ?? this.isLoadingFeatured,
        isLoadingTopRated: isLoadingTopRated ?? this.isLoadingTopRated,
        isLoadingUpcoming: isLoadingUpcoming ?? this.isLoadingUpcoming,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// Fetches movie discovery rows (the headline feed, Top Rated, Coming Soon).
class MovieDiscoverNotifier extends StateNotifier<MovieDiscoverState> {
  final DiscoverApiService _api;

  MovieDiscoverNotifier(this._api) : super(const MovieDiscoverState());

  Future<void> bootstrap() async {
    await Future.wait([
      _fetchFeatured(),
      _fetchTopRatedMovies(),
      _fetchUpcomingMovies(),
      _fetchAnticipatedMovies(),
    ]);
  }

  Future<void> _fetchFeatured() async {
    state = state.copyWith(isLoadingFeatured: true);
    try {
      final feed = await _api.fetchFeaturedMovies();
      state = state.copyWith(
        featured: feed.items,
        featuredSource: feed.source,
        isLoadingFeatured: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingFeatured: false);
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
