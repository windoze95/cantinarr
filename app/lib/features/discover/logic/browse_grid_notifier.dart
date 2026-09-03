import 'package:flutter/foundation.dart';

import '../data/discover_api_service.dart';
import '../data/tmdb_models.dart';
import 'browse_query.dart';
import 'paged_feed.dart';

/// State behind one browse grid: the query, the titles paged so far, and
/// whether the last read failed. Screen-local, like the detail and person
/// notifiers, since a grid is one query for the life of its screen.
class BrowseGridNotifier extends ChangeNotifier {
  BrowseGridNotifier(this._api, BrowseQuery query) : _query = query;

  /// A grid stops asking after this many pages (a thousand posters) however
  /// far the feed reports going; nobody scrolls further, and the memory is
  /// bounded.
  static const maxPages = 50;

  /// Sent alongside a rating floor or a rating sort so one-vote titles do not
  /// lead the grid.
  static const ratedMinVotes = 100;

  final DiscoverApiService _api;
  final PagedFeed _feed = PagedFeed();

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
  String? get featuredSource => _featuredSource;

  /// Set once a page added nothing, whether the feed ended, its next pages
  /// were all thinned away, or the read failed. The grid stops asking on its
  /// own from then on; only a reload (a retry, a new query) asks again. Without
  /// this the end-of-grid check would re-request a failing page every frame.
  bool _stalled = false;

  bool get hasMore => !_stalled && _feed.hasMore && _feed.page <= maxPages;

  /// Loads the first page, replacing whatever was shown.
  Future<void> load() async {
    _feed.reset();
    _items = const [];
    _error = null;
    _stalled = false;
    await _run(replace: true);
  }

  /// Appends the next page, if there is one and none is in flight.
  Future<void> loadMore() async {
    if (_isLoading || !hasMore) return;
    await _run(replace: false);
  }

  /// Changes the query (filters or sort) and reloads from page one.
  Future<void> setQuery(BrowseQuery query) {
    _query = query;
    return load();
  }

  Future<void> _run({required bool replace}) async {
    _isLoading = true;
    notifyListeners();
    final fresh = await _feed.nextPage(_fetch);
    if (fresh == null) return; // superseded by a reset
    _items = replace ? fresh : [..._items, ...fresh];
    _error = fresh.isEmpty ? _feed.lastError : null;
    _stalled = fresh.isEmpty;
    _isLoading = false;
    notifyListeners();
  }

  Future<TmdbPage<MediaItem>> _fetch(int page) async {
    final type = _query.type;
    final tv = type == MediaType.tv;
    final id = _query.id ?? 0;
    switch (_query.feed) {
      case BrowseFeed.featured:
        final feed = tv
            ? await _api.fetchFeaturedTV(page: page)
            : await _api.fetchFeaturedMovies(page: page);
        _featuredSource ??= feed.source;
        return feed.asPage;
      case BrowseFeed.popular:
        return tv
            ? _api.fetchPopularTV(page: page)
            : _api.fetchPopularMovies(page: page);
      case BrowseFeed.topRated:
        return _api.fetchTopRatedMovies(page: page);
      case BrowseFeed.upcoming:
        return _api.fetchUpcomingMovies(page: page);
      case BrowseFeed.nowPlaying:
        return _api.fetchNowPlayingMovies(page: page);
      case BrowseFeed.anticipated:
        final items =
            await _api.getTraktAnticipated(tv ? 'shows' : 'movies', page: page);
        return openEndedPage(page, [for (final i in items) i.toMediaItem()]);
      case BrowseFeed.discover:
        return _discover(page);
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

  Future<TmdbPage<MediaItem>> _discover(int page) {
    final filters = _query.filters;
    final sort = _query.sort;
    final rated = filters.minRating != null || sort == BrowseSort.topRated;
    final from = filters.yearFrom == null ? null : '${filters.yearFrom}-01-01';
    final to = filters.yearTo == null ? null : '${filters.yearTo}-12-31';
    final genreIds = filters.genreIds.isEmpty ? null : filters.genreIds;
    final minRating = filters.minRating?.toDouble();
    final minVotes = rated ? ratedMinVotes : null;
    return _query.type == MediaType.tv
        ? _api.discoverTV(
            page: page,
            genreIds: genreIds,
            sortBy: sort.tmdbSortBy(MediaType.tv),
            airedFrom: from,
            airedTo: to,
            minRating: minRating,
            minVotes: minVotes,
          )
        : _api.discoverMovies(
            page: page,
            genreIds: genreIds,
            sortBy: sort.tmdbSortBy(MediaType.movie),
            releasedFrom: from,
            releasedTo: to,
            minRating: minRating,
            minVotes: minVotes,
          );
  }
}
