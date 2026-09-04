import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/paged_feed.dart';

/// Discovery state for the Movies tab.
class MovieDiscoverState {
  /// The headline row, from whichever feed the admin configured.
  final List<MediaItem> featured;

  /// Which feed answered, so the row can title itself honestly.
  final String featuredSource;
  final List<MediaItem> nowPlaying;
  final List<MediaItem> topRated;
  final List<MediaItem> upcoming;
  final List<MediaItem> anticipated;

  /// TMDB's movie genres, for the Browse-by-genre strip; empty until read.
  final List<Genre> genres;
  final bool isLoadingFeatured;
  final bool isLoadingNowPlaying;
  final bool isLoadingTopRated;
  final bool isLoadingUpcoming;
  final bool isLoadingAnticipated;

  const MovieDiscoverState({
    this.featured = const [],
    this.featuredSource = '',
    this.nowPlaying = const [],
    this.topRated = const [],
    this.upcoming = const [],
    this.anticipated = const [],
    this.genres = const [],
    this.isLoadingFeatured = false,
    this.isLoadingNowPlaying = false,
    this.isLoadingTopRated = false,
    this.isLoadingUpcoming = false,
    this.isLoadingAnticipated = false,
  });

  /// The row's title for the feed that answered.
  String get featuredTitle => featuredRowTitle(featuredSource, isTv: false);

  MovieDiscoverState copyWith({
    List<MediaItem>? featured,
    String? featuredSource,
    List<MediaItem>? nowPlaying,
    List<MediaItem>? topRated,
    List<MediaItem>? upcoming,
    List<MediaItem>? anticipated,
    List<Genre>? genres,
    bool? isLoadingFeatured,
    bool? isLoadingNowPlaying,
    bool? isLoadingTopRated,
    bool? isLoadingUpcoming,
    bool? isLoadingAnticipated,
  }) =>
      MovieDiscoverState(
        featured: featured ?? this.featured,
        featuredSource: featuredSource ?? this.featuredSource,
        nowPlaying: nowPlaying ?? this.nowPlaying,
        topRated: topRated ?? this.topRated,
        upcoming: upcoming ?? this.upcoming,
        anticipated: anticipated ?? this.anticipated,
        genres: genres ?? this.genres,
        isLoadingFeatured: isLoadingFeatured ?? this.isLoadingFeatured,
        isLoadingNowPlaying: isLoadingNowPlaying ?? this.isLoadingNowPlaying,
        isLoadingTopRated: isLoadingTopRated ?? this.isLoadingTopRated,
        isLoadingUpcoming: isLoadingUpcoming ?? this.isLoadingUpcoming,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// The rows that grow as they are scrolled, as opposed to the headline row,
/// which is a single server-capped page.
enum _Row { nowPlaying, topRated, upcoming, anticipated }

extension on MovieDiscoverState {
  List<MediaItem> itemsOf(_Row row) => switch (row) {
        _Row.nowPlaying => nowPlaying,
        _Row.topRated => topRated,
        _Row.upcoming => upcoming,
        _Row.anticipated => anticipated,
      };

  MovieDiscoverState withRow(
    _Row row, {
    required List<MediaItem> items,
    required bool isLoading,
  }) =>
      switch (row) {
        _Row.nowPlaying =>
          copyWith(nowPlaying: items, isLoadingNowPlaying: isLoading),
        _Row.topRated =>
          copyWith(topRated: items, isLoadingTopRated: isLoading),
        _Row.upcoming =>
          copyWith(upcoming: items, isLoadingUpcoming: isLoading),
        _Row.anticipated =>
          copyWith(anticipated: items, isLoadingAnticipated: isLoading),
      };
}

/// Fetches the movie discovery rows: the headline feed, In Theaters, Top
/// Rated, Coming Soon, and Most Anticipated.
///
/// Every row but the headline is a [PagedFeed]: [bootstrap] loads its first
/// page and the `loadMore*` callbacks append the next one as the row is
/// scrolled toward its end.
class MovieDiscoverNotifier extends StateNotifier<MovieDiscoverState> {
  final DiscoverApiService _api;
  final Map<_Row, PagedFeed> _feeds = {
    for (final row in _Row.values) row: PagedFeed(),
  };

  MovieDiscoverNotifier(this._api) : super(const MovieDiscoverState());

  Future<void> bootstrap() async {
    await Future.wait([
      _fetchFeatured(),
      _fetchGenres(),
      for (final row in _Row.values) _restart(row),
    ]);
  }

  Future<void> loadMoreNowPlaying() => _loadMore(_Row.nowPlaying);
  Future<void> loadMoreTopRated() => _loadMore(_Row.topRated);
  Future<void> loadMoreUpcoming() => _loadMore(_Row.upcoming);
  Future<void> loadMoreAnticipated() => _loadMore(_Row.anticipated);

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

  /// The genre strip is a convenience over the Browse page, so a failed read
  /// simply leaves it out rather than marking the tab as broken.
  Future<void> _fetchGenres() async {
    if (state.genres.isNotEmpty) return;
    try {
      // Read the genres before touching state: `state.copyWith(await ...)`
      // would capture the state from before the await and write that stale
      // snapshot back over every row that resolved in the meantime.
      final genres = await _api.movieGenres();
      state = state.copyWith(genres: genres);
    } catch (_) {}
  }

  /// Reloads a row from its first page, replacing what it showed.
  Future<void> _restart(_Row row) async {
    final feed = _feeds[row]!;
    feed.reset();
    state = state.withRow(row, items: state.itemsOf(row), isLoading: true);
    final fresh = await feed.nextPage((page) => _fetchPage(row, page));
    if (fresh == null) return;
    state = state.withRow(row, items: fresh, isLoading: false);
  }

  Future<void> _loadMore(_Row row) async {
    final feed = _feeds[row]!;
    if (feed.isLoading || !feed.hasMore) return;
    state = state.withRow(row, items: state.itemsOf(row), isLoading: true);
    final fresh = await feed.nextPage((page) => _fetchPage(row, page));
    if (fresh == null) return;
    state = state.withRow(
      row,
      items: [...state.itemsOf(row), ...fresh],
      isLoading: false,
    );
  }

  Future<TmdbPage<MediaItem>> _fetchPage(_Row row, int page) => switch (row) {
        _Row.nowPlaying => _api.fetchNowPlayingMovies(page: page),
        _Row.topRated => _api.fetchTopRatedMovies(page: page),
        _Row.upcoming => _api.fetchUpcomingMovies(page: page),
        _Row.anticipated => _fetchAnticipatedPage(page),
      };

  Future<TmdbPage<MediaItem>> _fetchAnticipatedPage(int page) async {
    final items = await _api.getTraktAnticipated('movies', page: page);
    return openEndedPage(page, [for (final item in items) item.toMediaItem()]);
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
