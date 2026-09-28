import 'package:flutter/foundation.dart';

import '../data/discover_api_service.dart';
import '../data/tmdb_models.dart';
import 'browse_query.dart';
import 'paged_feed.dart';
import 'discover_session.dart';

/// State behind one browse grid: the query, the titles paged so far, and
/// whether the last read failed. Retained by the bounded session cache.
class BrowseGridNotifier extends ChangeNotifier {
  BrowseGridNotifier(this._api, BrowseQuery query, {this.isCurrent}) : _query = query;

  final bool Function()? isCurrent;
  bool get _current => !_disposed && (isCurrent?.call() ?? true);

  /// A grid stops asking after this many pages (a thousand posters) however
  /// far the feed reports going; nobody scrolls further, and the memory is
  /// bounded.
  static const maxPages = 50;

  /// Sent alongside a rating floor or a rating sort so one-vote titles do not
  /// lead the grid.
  static const ratedMinVotes = 100;

  final DiscoverApiService _api;
  final PagedFeed _feed = PagedFeed(pageLimit: maxPages);

  BrowseQuery _query;
  BrowseQuery get query => _query;

  List<MediaItem> _items = const [];
  List<MediaItem> get items => _items;

  bool _isLoading = false;
  bool get isLoading => _isLoading;

  /// Set when the last read threw; the grid shows it instead of an empty
  /// state, since an unreadable feed is not an empty one.
  Object? _error;
  Object? get error => _error;

  /// The headline feed names the source that answered, which titles the grid.
  String? _featuredSource;
  String? _pendingFeaturedSource;
  String? get featuredSource => _featuredSource;

  /// Set once a page added nothing, whether the feed ended, its next pages
  /// were all thinned away, or the read failed. The grid stops asking on its
  /// own from then on; only a reload (a retry, a new query) asks again. Without
  /// this the end-of-grid check would re-request a failing page every frame.
  bool _stalled = false;

  bool get hasMore => !_stalled && _feed.hasMore && _feed.page <= maxPages;

  bool _disposed = false;
  int _generation = 0;
  Future<void>? _refresh;
  bool _hasLoaded = false;
  bool get hasLoaded => _hasLoaded;
  bool get isRefreshing => _refresh != null;

  /// Saved with this complete query for remounts and browser Back.
  double scrollOffset = 0;

  Future<void> load() => refresh();

  Future<void> refresh() {
    if (!_current) return Future.value();
    if (_refresh != null) return _refresh!;
    final future = _refreshWindow();
    _refresh = future;
    return future.whenComplete(() {
      if (identical(_refresh, future)) _refresh = null;
    });
  }

  Future<void> _refreshWindow() async {
    final generation = ++_generation;
    final query = _query;
    _pendingFeaturedSource = null;
    _isLoading = !_hasLoaded;
    notifyListeners();
    final fresh = await _feed.refresh((page) => _fetch(query, page, generation));
    if (!_current || generation != _generation || fresh == null) return;
    _error = _feed.lastError;
    if (_error == null) {
      _items = fresh;
      _hasLoaded = true;
      _stalled = fresh.isEmpty;
      _featuredSource = _pendingFeaturedSource ?? _featuredSource;
    } else if (discoverAccessDenied(_error)) {
      _items = const [];
      _hasLoaded = false;
    }
    _isLoading = false;
    notifyListeners();
  }

  /// Appends only outside a refresh. A late page cannot overwrite its window.
  Future<void> loadMore() async {
    if (!_current || !_hasLoaded || _refresh != null || _isLoading || !hasMore ||
        _error != null) {
      return;
    }
    final generation = _generation;
    final query = _query;
    _isLoading = true;
    notifyListeners();
    final fresh = await _feed.nextPage((page) => _fetch(query, page, generation));
    if (!_current || generation != _generation || fresh == null) return;
    _error = _feed.lastError;
    _items = discoverAccessDenied(_error) ? const [] : [..._items, ...fresh];
    _stalled = fresh.isEmpty;
    _isLoading = false;
    notifyListeners();
  }

  /// Used outside the session cache; cached screens acquire a separate entry
  /// for each complete query so returning to previous filters is immediate.
  Future<void> setQuery(BrowseQuery query) {
    _generation++;
    _feed.reset();
    _refresh = null;
    _query = query;
    _items = const [];
    _hasLoaded = false;
    _featuredSource = null;
    _error = null;
    _stalled = false;
    scrollOffset = 0;
    return refresh();
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _generation++;
    _feed.reset();
    super.dispose();
  }

  Future<TmdbPage<MediaItem>> _fetch(
      BrowseQuery query, int page, int generation) async {
    final type = query.type;
    final tv = type == MediaType.tv;
    final id = query.id ?? 0;
    switch (query.feed) {
      case BrowseFeed.featured:
        final feed = tv
            ? await _api.fetchFeaturedTV(page: page)
            : await _api.fetchFeaturedMovies(page: page);
        if (_current && generation == _generation) {
          _pendingFeaturedSource = feed.source;
        }
        return feed.asPage;
      case BrowseFeed.popular:
        return tv
            ? _api.fetchPopularTV(page: page)
            : _api.fetchPopularMovies(page: page);
      case BrowseFeed.topRated:
        return tv
            ? _api.fetchTopRatedTV(page: page)
            : _api.fetchTopRatedMovies(page: page);
      case BrowseFeed.upcoming:
        return tv
            ? _api.fetchUpcomingTV(page: page)
            : _api.fetchUpcomingMovies(page: page);
      case BrowseFeed.nowPlaying:
        return _api.fetchNowPlayingMovies(page: page);
      case BrowseFeed.onTheAir:
        return _api.fetchOnTheAirTV(page: page);
      case BrowseFeed.anticipated:
        final items =
            await _api.getTraktAnticipated(tv ? 'shows' : 'movies', page: page);
        return openEndedPage(page, [for (final i in items) i.toMediaItem()]);
      case BrowseFeed.discover:
        return _discover(query, page);
      case BrowseFeed.recommendations:
        return tv
            ? _api.tvRecommendations(id, page: page)
            : _api.movieRecommendations(id, page: page);
      case BrowseFeed.similar:
        return tv
            ? _api.similarTV(id, page: page)
            : _api.similarMovies(id, page: page);
    }
  }

  Future<TmdbPage<MediaItem>> _discover(BrowseQuery query, int page) {
    final filters = query.filters;
    final sort = query.sort;
    final rated = filters.minRating != null || sort == BrowseSort.topRated;
    final from = filters.yearFrom == null ? null : '${filters.yearFrom}-01-01';
    final to = filters.yearTo == null ? null : '${filters.yearTo}-12-31';
    final genreIds = filters.genreIds.isEmpty ? null : filters.genreIds;
    final minRating = filters.minRating?.toDouble();
    final minVotes = rated ? ratedMinVotes : null;
    final providerIds = filters.providerIds.isEmpty ? null : filters.providerIds;
    final keywordIds = filters.keywords.isEmpty
        ? null
        : [for (final k in filters.keywords) k.id];
    final companyIds = filters.companies.isEmpty
        ? null
        : [for (final c in filters.companies) c.id];
    return query.type == MediaType.tv
        ? _api.discoverTV(
            page: page,
            genreIds: genreIds,
            sortBy: sort.tmdbSortBy(MediaType.tv),
            airedFrom: from,
            airedTo: to,
            minRating: minRating,
            minVotes: minVotes,
            language: filters.language,
            watchProviderIds: providerIds,
            watchRegion: filters.watchRegion,
            keywordIds: keywordIds,
            companyIds: companyIds,
          )
        : _api.discoverMovies(
            page: page,
            genreIds: genreIds,
            sortBy: sort.tmdbSortBy(MediaType.movie),
            releasedFrom: from,
            releasedTo: to,
            minRating: minRating,
            minVotes: minVotes,
            language: filters.language,
            watchProviderIds: providerIds,
            watchRegion: filters.watchRegion,
            keywordIds: keywordIds,
            companyIds: companyIds,
          );
  }
}
