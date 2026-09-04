import '../data/tmdb_models.dart';
import 'paged_loader.dart';

/// Paging state for one TMDB-shaped feed: the loader, the ids already handed
/// out, and a generation stamp so a reset drops the page a superseded call is
/// still awaiting instead of merging it into the fresh list.
///
/// Rows and grids append what [nextPage] returns; the feed itself never holds
/// items, so the same helper backs a notifier-owned row and a screen-local
/// grid alike.
class PagedFeed {
  /// The server's English-only filter thins a page without touching
  /// `total_pages`, so a page that adds nothing new is normal rather than the
  /// end of the feed. Walk this many further pages within one call before
  /// answering "nothing new": enough to cross a mostly non-English stretch,
  /// bounded so a thin feed never becomes hundreds of upstream calls per
  /// scroll.
  static const maxEmptyPages = 3;

  /// TMDB refuses pages past 500 while still reporting `total_pages` above it.
  static const maxPage = 500;

  final PagedLoader _loader = PagedLoader();
  final Set<int> _seen = {};
  int _generation = 0;
  Object? _lastError;

  /// Whether another page can be asked for.
  bool get hasMore => _loader.hasMore && _loader.page <= maxPage;

  /// Whether a [nextPage] call is in flight.
  bool get isLoading => _loader.isLoading;

  /// The page the next fetch will ask for.
  int get page => _loader.page;

  /// Why the last [nextPage] came back empty, when a fetch threw; null after
  /// a call that fetched cleanly. An empty answer with an error behind it is
  /// blindness, not absence, and a surface must not render it as "nothing
  /// here".
  Object? get lastError => _lastError;

  /// Start over from page one. A [nextPage] call still awaiting its fetch
  /// resolves to null rather than delivering a page of the old run.
  void reset() {
    _loader.reset();
    _seen.clear();
    _generation++;
  }

  /// The items from the next page(s) that have not been handed out before.
  ///
  /// Duplicates across pages are dropped by TMDB id (TMDB re-ranks between
  /// requests, so adjacent pages overlap), and so are entries with no usable
  /// id: a Trakt title TMDB does not know has nothing to open or request.
  ///
  /// Returns null when [reset] superseded this call, and an empty list when
  /// nothing new could be fetched: the feed ended, the fetch failed (the same
  /// page is retried next time), or [maxEmptyPages] pages added nothing.
  Future<List<MediaItem>?> nextPage(
    Future<TmdbPage<MediaItem>> Function(int page) fetch,
  ) async {
    final generation = _generation;
    _lastError = null;
    var emptyPages = 0;
    while (emptyPages < maxEmptyPages && hasMore && _loader.beginLoading()) {
      final TmdbPage<MediaItem> page;
      try {
        page = await fetch(_loader.page);
      } catch (error) {
        if (generation != _generation) return null;
        _loader.cancelLoading();
        _lastError = error;
        return const [];
      }
      if (generation != _generation) return null;
      _loader.endLoading(page.totalPages);

      final fresh = <MediaItem>[];
      for (final item in page.results) {
        if (item.id > 0 && _seen.add(item.id)) fresh.add(item);
      }
      if (fresh.isNotEmpty) return fresh;
      emptyPages++;
    }
    return const [];
  }
}

/// Wraps a list-shaped feed that reports no page count (the Trakt
/// passthroughs) as a page that keeps going until a page comes back empty.
TmdbPage<MediaItem> openEndedPage(int page, List<MediaItem> results) =>
    TmdbPage(
      page: page,
      totalPages: results.isEmpty ? page : page + 1,
      totalResults: 0,
      results: results,
    );
