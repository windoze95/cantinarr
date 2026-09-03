import '../data/tmdb_models.dart';

/// The feeds a browse grid can page. Each is a row on a discovery tab, a
/// detail-page row, or the filterable discover query behind the Browse page.
enum BrowseFeed {
  /// The admin-configured headline feed, continued past the row.
  featured('featured'),
  popular('popular'),
  topRated('top-rated'),
  upcoming('upcoming'),
  nowPlaying('now-playing'),
  anticipated('anticipated'),

  /// TMDB discover with the user's filters and sort.
  discover('discover'),
  recommendations('recommendations'),
  similar('similar');

  const BrowseFeed(this.slug);

  /// The path segment that names this feed in a browse URL.
  final String slug;

  static BrowseFeed? fromSlug(String slug) {
    for (final feed in values) {
      if (feed.slug == slug) return feed;
    }
    return null;
  }

  /// Feeds anchored on one title carry its TMDB id.
  bool get needsId => this == recommendations || this == similar;

  /// The server has movie-only routes for these three.
  bool supports(MediaType type) => switch (this) {
        topRated || upcoming || nowPlaying => type == MediaType.movie,
        _ => type == MediaType.movie || type == MediaType.tv,
      };

  /// Only the discover feed takes filters and a sort; every other feed is
  /// ordered by its source.
  bool get isFilterable => this == discover;
}

/// The orders the Browse page offers, in requester words. Each maps to the
/// TMDB `sort_by` the server validates for the media type.
enum BrowseSort {
  popular('popular', 'Most popular'),
  newest('newest', 'Newest first'),
  oldest('oldest', 'Oldest first'),
  topRated('top-rated', 'Top rated'),
  titleAz('title', 'Title A to Z');

  const BrowseSort(this.slug, this.label);

  final String slug;
  final String label;

  static BrowseSort? fromSlug(String slug) {
    for (final sort in values) {
      if (sort.slug == slug) return sort;
    }
    return null;
  }

  String tmdbSortBy(MediaType type) {
    final tv = type == MediaType.tv;
    return switch (this) {
      popular => 'popularity.desc',
      newest => tv ? 'first_air_date.desc' : 'primary_release_date.desc',
      oldest => tv ? 'first_air_date.asc' : 'primary_release_date.asc',
      topRated => 'vote_average.desc',
      titleAz => tv ? 'name.asc' : 'title.asc',
    };
  }
}

/// The filters the Browse page applies to the discover feed.
class BrowseFilters {
  const BrowseFilters({
    this.genreIds = const [],
    this.yearFrom,
    this.yearTo,
    this.minRating,
  });

  static const none = BrowseFilters();

  /// TMDB genre ids; a title must carry every one of them.
  final List<int> genreIds;

  /// Inclusive release-year bounds; either side may be open.
  final int? yearFrom;
  final int? yearTo;

  /// Minimum TMDB score, 0 to 10.
  final int? minRating;

  bool get isEmpty =>
      genreIds.isEmpty && yearFrom == null && yearTo == null && minRating == null;

  /// How many filter groups are active, for the Filters button's count.
  int get count =>
      (genreIds.isNotEmpty ? 1 : 0) +
      (yearFrom != null || yearTo != null ? 1 : 0) +
      (minRating != null ? 1 : 0);

  BrowseFilters copyWith({
    List<int>? genreIds,
    int? Function()? yearFrom,
    int? Function()? yearTo,
    int? Function()? minRating,
  }) =>
      BrowseFilters(
        genreIds: genreIds ?? this.genreIds,
        yearFrom: yearFrom == null ? this.yearFrom : yearFrom(),
        yearTo: yearTo == null ? this.yearTo : yearTo(),
        minRating: minRating == null ? this.minRating : minRating(),
      );

  /// The filters in words, so an empty grid can say what it looked for:
  /// `Action, Comedy · 2010 to 2019 · rated 7+`.
  String describe(Map<int, String> genreNames) {
    final parts = <String>[];
    if (genreIds.isNotEmpty) {
      parts.add(genreIds.map((id) => genreNames[id] ?? 'genre $id').join(', '));
    }
    if (yearFrom != null && yearTo != null) {
      parts.add(yearFrom == yearTo ? '$yearFrom' : '$yearFrom to $yearTo');
    } else if (yearFrom != null) {
      parts.add('$yearFrom onward');
    } else if (yearTo != null) {
      parts.add('up to $yearTo');
    }
    if (minRating != null) parts.add('rated $minRating+');
    return parts.join(' · ');
  }
}

/// Everything a browse grid needs, and everything its URL carries: web deep
/// links and pushes both go through [toLocation] and [tryParse], never
/// through router `extra`.
class BrowseQuery {
  const BrowseQuery({
    required this.type,
    required this.feed,
    this.title,
    this.id,
    this.sort = BrowseSort.popular,
    this.filters = BrowseFilters.none,
  });

  final MediaType type;
  final BrowseFeed feed;

  /// The heading the grid opens with, usually the row it came from.
  final String? title;

  /// The anchoring title's TMDB id for [BrowseFeed.needsId] feeds.
  final int? id;
  final BrowseSort sort;
  final BrowseFilters filters;

  BrowseQuery copyWith({
    String? title,
    BrowseSort? sort,
    BrowseFilters? filters,
  }) =>
      BrowseQuery(
        type: type,
        feed: feed,
        title: title ?? this.title,
        id: id,
        sort: sort ?? this.sort,
        filters: filters ?? this.filters,
      );

  /// `/browse/movie/discover?genres=28,12&sort=top-rated&from=2010&to=2019&rating=7&title=Action`
  String toLocation() {
    final params = <String, String>{
      if (id != null) 'id': '$id',
      if (filters.genreIds.isNotEmpty) 'genres': filters.genreIds.join(','),
      if (sort != BrowseSort.popular) 'sort': sort.slug,
      if (filters.yearFrom != null) 'from': '${filters.yearFrom}',
      if (filters.yearTo != null) 'to': '${filters.yearTo}',
      if (filters.minRating != null) 'rating': '${filters.minRating}',
      if (title != null && title!.isNotEmpty) 'title': title!,
    };
    return Uri(
      path: '/browse/${type.name}/${feed.slug}',
      queryParameters: params.isEmpty ? null : params,
    ).toString();
  }

  /// Reads a browse URL back, or null for anything that is not a valid one:
  /// an unknown media type or feed, a feed the type has no route for, an
  /// anchored feed without a positive id. Junk numbers in the query string
  /// are dropped rather than failing the whole link.
  static BrowseQuery? tryParse(Uri uri) {
    final segments = uri.pathSegments;
    if (segments.length != 3 || segments[0] != 'browse') return null;
    final type = switch (segments[1]) {
      'movie' => MediaType.movie,
      'tv' => MediaType.tv,
      _ => null,
    };
    final feed = BrowseFeed.fromSlug(segments[2]);
    if (type == null || feed == null || !feed.supports(type)) return null;

    final q = uri.queryParameters;
    final id = _positiveInt(q['id']);
    if (feed.needsId && id == null) return null;

    final title = q['title']?.trim();
    return BrowseQuery(
      type: type,
      feed: feed,
      title: title == null || title.isEmpty ? null : title,
      id: feed.needsId ? id : null,
      sort: BrowseSort.fromSlug(q['sort'] ?? '') ?? BrowseSort.popular,
      filters: BrowseFilters(
        genreIds: [
          for (final raw in (q['genres'] ?? '').split(','))
            if (_positiveInt(raw) case final genre?) genre,
        ],
        yearFrom: _year(q['from']),
        yearTo: _year(q['to']),
        minRating: _rating(q['rating']),
      ),
    );
  }

  static int? _positiveInt(String? raw) {
    final value = int.tryParse(raw?.trim() ?? '');
    return value != null && value > 0 ? value : null;
  }

  static int? _year(String? raw) {
    final value = int.tryParse(raw ?? '');
    return value != null && value >= 1800 && value <= 2200 ? value : null;
  }

  static int? _rating(String? raw) {
    final value = int.tryParse(raw ?? '');
    return value != null && value >= 0 && value <= 10 ? value : null;
  }
}
