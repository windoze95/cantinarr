import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/paged_feed.dart';
import '../../discover/logic/discover_session.dart';

/// Discovery state for the TV Shows tab.
class TvDiscoverState {
  /// The headline row, from whichever feed the admin configured.
  final List<MediaItem> featured;

  /// Which feed answered, so the row can title itself honestly.
  final String featuredSource;
  final List<MediaItem> onTheAir;
  final List<MediaItem> topRated;
  final List<MediaItem> upcoming;
  final List<MediaItem> anticipated;

  /// TMDB's TV genres, for the Browse-by-genre strip; empty until read.
  final List<Genre> genres;
  final Set<String> failedRows;
  final bool hasLoaded;
  final int refreshRevision;
  final bool isLoadingFeatured;
  final bool isLoadingOnTheAir;
  final bool isLoadingTopRated;
  final bool isLoadingUpcoming;
  final bool isLoadingAnticipated;

  const TvDiscoverState({
    this.featured = const [],
    this.featuredSource = '',
    this.onTheAir = const [],
    this.topRated = const [],
    this.upcoming = const [],
    this.anticipated = const [],
    this.genres = const [],
    this.failedRows = const {},
    this.hasLoaded = false,
    this.refreshRevision = 0,
    this.isLoadingFeatured = false,
    this.isLoadingOnTheAir = false,
    this.isLoadingTopRated = false,
    this.isLoadingUpcoming = false,
    this.isLoadingAnticipated = false,
  });

  /// The row's title for the feed that answered.
  String get featuredTitle => featuredRowTitle(featuredSource, isTv: true);

  TvDiscoverState copyWith({
    List<MediaItem>? featured,
    String? featuredSource,
    List<MediaItem>? onTheAir,
    List<MediaItem>? topRated,
    List<MediaItem>? upcoming,
    List<MediaItem>? anticipated,
    List<Genre>? genres,
    Set<String>? failedRows,
    bool? hasLoaded,
    int? refreshRevision,
    bool? isLoadingFeatured,
    bool? isLoadingOnTheAir,
    bool? isLoadingTopRated,
    bool? isLoadingUpcoming,
    bool? isLoadingAnticipated,
  }) =>
      TvDiscoverState(
        featured: featured ?? this.featured,
        featuredSource: featuredSource ?? this.featuredSource,
        onTheAir: onTheAir ?? this.onTheAir,
        topRated: topRated ?? this.topRated,
        upcoming: upcoming ?? this.upcoming,
        anticipated: anticipated ?? this.anticipated,
        genres: genres ?? this.genres,
        failedRows: failedRows ?? this.failedRows,
        hasLoaded: hasLoaded ?? this.hasLoaded,
        refreshRevision: refreshRevision ?? this.refreshRevision,
        isLoadingFeatured: isLoadingFeatured ?? this.isLoadingFeatured,
        isLoadingOnTheAir: isLoadingOnTheAir ?? this.isLoadingOnTheAir,
        isLoadingTopRated: isLoadingTopRated ?? this.isLoadingTopRated,
        isLoadingUpcoming: isLoadingUpcoming ?? this.isLoadingUpcoming,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// The rows that grow as they are scrolled, as opposed to the headline row,
/// which is a single server-capped page.
enum _Row { onTheAir, topRated, upcoming, anticipated }

extension on TvDiscoverState {
  List<MediaItem> itemsOf(_Row row) => switch (row) {
        _Row.onTheAir => onTheAir,
        _Row.topRated => topRated,
        _Row.upcoming => upcoming,
        _Row.anticipated => anticipated,
      };

  TvDiscoverState withRow(
    _Row row, {
    required List<MediaItem> items,
    required bool isLoading,
  }) =>
      switch (row) {
        _Row.onTheAir =>
          copyWith(onTheAir: items, isLoadingOnTheAir: isLoading),
        _Row.topRated =>
          copyWith(topRated: items, isLoadingTopRated: isLoading),
        _Row.upcoming =>
          copyWith(upcoming: items, isLoadingUpcoming: isLoading),
        _Row.anticipated =>
          copyWith(anticipated: items, isLoadingAnticipated: isLoading),
      };
}

/// Fetches the TV discovery rows: the headline feed, Airing This Week, Top
/// Rated, Coming Soon, and Most Anticipated.
///
/// Every row but the headline is a [PagedFeed]: [bootstrap] loads its first
/// page and the `loadMore*` callbacks append the next one as the row is
/// scrolled toward its end.
class TvDiscoverNotifier extends StateNotifier<TvDiscoverState> {
  final DiscoverApiService _api;
  final Map<_Row, PagedFeed> _feeds = {
    for (final row in _Row.values) row: PagedFeed(),
  };

  final bool Function()? isCurrent;
  bool get _current => mounted && (isCurrent?.call() ?? true);

  TvDiscoverNotifier(this._api, {this.isCurrent})
      : super(const TvDiscoverState());

  Future<void>? _refresh;

  Future<void> bootstrap() {
    if (!_current) return Future.value();
    return _refresh ??= _refreshAll().whenComplete(() => _refresh = null);
  }

  Future<void> _refreshAll() async {
    await Future.wait([
      _fetchFeatured(),
      _fetchGenres(),
      for (final row in _Row.values) _restart(row),
    ]);
    if (_current) {
      state = state.copyWith(hasLoaded: true,
          refreshRevision: state.refreshRevision + 1);
    }
  }

  void _recordError(String row, Object? error) {
    if (!_current) return;
    state = state.copyWith(failedRows: {
      ...state.failedRows.where((name) => name != row),
      if (error != null) row,
    });
  }

  @override
  void dispose() {
    for (final feed in _feeds.values) {
      feed.reset();
    }
    super.dispose();
  }

  Future<void> loadMoreOnTheAir() => _loadMore(_Row.onTheAir);
  Future<void> loadMoreTopRated() => _loadMore(_Row.topRated);
  Future<void> loadMoreUpcoming() => _loadMore(_Row.upcoming);
  Future<void> loadMoreAnticipated() => _loadMore(_Row.anticipated);

  Future<void> _fetchFeatured() async {
    state = state.copyWith(isLoadingFeatured: !state.hasLoaded);
    try {
      final feed = await _api.fetchFeaturedTV();
      if (!_current) return;
      _recordError('featured', null);
      state = state.copyWith(
        featured: feed.items,
        featuredSource: feed.source,
        isLoadingFeatured: false,
      );
    } catch (error) {
      if (!_current) return;
      _recordError('featured', error);
      state = state.copyWith(
        featured: discoverAccessDenied(error) ? const [] : null,
        isLoadingFeatured: false,
      );
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
      final genres = await _api.tvGenres();
      if (_current) state = state.copyWith(genres: genres);
    } catch (_) {}
  }

  /// Refresh the loaded window in a separate buffer, keeping visible rows.
  Future<void> _restart(_Row row) async {
    final feed = _feeds[row]!;
    state = state.withRow(row,
        items: state.itemsOf(row), isLoading: !state.hasLoaded);
    final fresh = await feed.refresh((page) => _fetchPage(row, page));
    if (!_current || fresh == null) return;
    _recordError(row.name, feed.lastError);
    state = state.withRow(
      row,
      items: discoverAccessDenied(feed.lastError)
          ? const []
          : feed.lastError == null ? fresh : state.itemsOf(row),
      isLoading: false,
    );
  }

  Future<void> _loadMore(_Row row) async {
    if (!_current) return;
    final feed = _feeds[row]!;
    if (_refresh != null || feed.isLoading || !feed.hasMore ||
        state.failedRows.contains(row.name)) {
      return;
    }
    state = state.withRow(row, items: state.itemsOf(row), isLoading: true);
    final fresh = await feed.nextPage((page) => _fetchPage(row, page));
    if (!_current || fresh == null) return;
    _recordError(row.name, feed.lastError);
    state = state.withRow(
      row,
      items: discoverAccessDenied(feed.lastError)
          ? const [] : [...state.itemsOf(row), ...fresh],
      isLoading: false,
    );
  }

  Future<TmdbPage<MediaItem>> _fetchPage(_Row row, int page) => switch (row) {
        _Row.onTheAir => _api.fetchOnTheAirTV(page: page),
        _Row.topRated => _api.fetchTopRatedTV(page: page),
        _Row.upcoming => _api.fetchUpcomingTV(page: page),
        _Row.anticipated => _fetchAnticipatedPage(page),
      };

  Future<TmdbPage<MediaItem>> _fetchAnticipatedPage(int page) async {
    final items = await _api.getTraktAnticipated('shows', page: page);
    return openEndedPage(page, [for (final item in items) item.toMediaItem()]);
  }
}

/// Provider for TV discovery data.
final tvDiscoverProvider =
    StateNotifierProvider<TvDiscoverNotifier, TvDiscoverState>(
  (ref) {
    final scope = ref.watch(discoverSessionProvider);
    final api = ref.watch(discoverServiceProvider);
    return TvDiscoverNotifier(api,
        isCurrent: () => ref.read(discoverSessionProvider) == scope);
  },
);
