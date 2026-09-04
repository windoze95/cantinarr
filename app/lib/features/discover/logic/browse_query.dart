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

  /// TMDB's on-the-air list: TV's counterpart to In Theaters.
  onTheAir('on-the-air'),
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

  /// In Theaters is a movie route and Airing This Week a TV one; every other
  /// feed has a route for both types.
  bool supports(MediaType type) => switch (this) {
        nowPlaying => type == MediaType.movie,
        onTheAir => type == MediaType.tv,
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

/// The device region for streaming services and release schedules, from a
/// device country code: two ASCII letters uppercased, else the US. The
/// country comes from the device's own locale (the app resolves no locales
/// of its own, so a widget-level locale is always en_US).
String watchRegionFor(String? countryCode) {
  final code = countryCode?.trim().toUpperCase() ?? '';
  return RegExp(r'^[A-Z]{2}$').hasMatch(code) ? code : 'US';
}

/// The filters the Browse page applies to the discover feed.
class BrowseFilters {
  const BrowseFilters({
    this.genreIds = const [],
    this.yearFrom,
    this.yearTo,
    this.minRating,
    this.language,
    this.providerIds = const [],
    this.watchRegion,
    this.keywords = const [],
    this.companies = const [],
  });

  static const none = BrowseFilters();

  /// TMDB genre ids; a title must carry every one of them.
  final List<int> genreIds;

  /// Inclusive release-year bounds; either side may be open.
  final int? yearFrom;
  final int? yearTo;

  /// Minimum TMDB score, 0 to 10.
  final int? minRating;

  /// Original language as an ISO 639-1 code. Naming one is an explicit ask
  /// the server never English-filters.
  final String? language;

  /// Streaming services (TMDB provider ids); any of them may carry the title.
  final List<int> providerIds;

  /// The country [providerIds] applies to. Meaningless on its own, so it is
  /// neither a filter group nor part of [isEmpty].
  final String? watchRegion;

  /// Keywords a title must all carry.
  final List<TaggedId> keywords;

  /// Production companies, any of which may apply.
  final List<TaggedId> companies;

  bool get isEmpty =>
      genreIds.isEmpty &&
      yearFrom == null &&
      yearTo == null &&
      minRating == null &&
      language == null &&
      providerIds.isEmpty &&
      keywords.isEmpty &&
      companies.isEmpty;

  /// How many filter groups are active, for the Filters button's count.
  int get count =>
      (genreIds.isNotEmpty ? 1 : 0) +
      (yearFrom != null || yearTo != null ? 1 : 0) +
      (minRating != null ? 1 : 0) +
      (language != null ? 1 : 0) +
      (providerIds.isNotEmpty ? 1 : 0) +
      (keywords.isNotEmpty ? 1 : 0) +
      (companies.isNotEmpty ? 1 : 0);

  BrowseFilters copyWith({
    List<int>? genreIds,
    int? Function()? yearFrom,
    int? Function()? yearTo,
    int? Function()? minRating,
    String? Function()? language,
    List<int>? providerIds,
    String? Function()? watchRegion,
    List<TaggedId>? keywords,
    List<TaggedId>? companies,
  }) =>
      BrowseFilters(
        genreIds: genreIds ?? this.genreIds,
        yearFrom: yearFrom == null ? this.yearFrom : yearFrom(),
        yearTo: yearTo == null ? this.yearTo : yearTo(),
        minRating: minRating == null ? this.minRating : minRating(),
        language: language == null ? this.language : language(),
        providerIds: providerIds ?? this.providerIds,
        watchRegion: watchRegion == null ? this.watchRegion : watchRegion(),
        keywords: keywords ?? this.keywords,
        companies: companies ?? this.companies,
      );

  /// The filters in words, so an empty grid can say what it looked for:
  /// `Action, Comedy · 2010 to 2019 · rated 7+ · in Korean · on Netflix ·
  /// about heist · from A24`. Names come from the lists the screen loaded;
  /// a value with no name is spoken by its id rather than dropped.
  String describe(
    Map<int, String> genreNames, {
    Map<String, String> languageNames = const {},
    Map<int, String> providerNames = const {},
  }) {
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
    if (language != null) parts.add('in ${languageNames[language] ?? language}');
    if (providerIds.isNotEmpty) {
      parts.add(
        'on ${providerIds.map((id) => providerNames[id] ?? 'service $id').join(', ')}',
      );
    }
    if (keywords.isNotEmpty) {
      parts.add(
        'about ${keywords.map((k) => k.name ?? 'keyword ${k.id}').join(', ')}',
      );
    }
    if (companies.isNotEmpty) {
      parts.add(
        'from ${companies.map((c) => c.name ?? 'studio ${c.id}').join(', ')}',
      );
    }
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
  /// plus `lang`, `prov` with `region`, `kw`, and `co` when set. Keywords and
  /// studios travel as ids only.
  String toLocation() {
    final params = <String, String>{
      if (id != null) 'id': '$id',
      if (filters.genreIds.isNotEmpty) 'genres': filters.genreIds.join(','),
      if (sort != BrowseSort.popular) 'sort': sort.slug,
      if (filters.yearFrom != null) 'from': '${filters.yearFrom}',
      if (filters.yearTo != null) 'to': '${filters.yearTo}',
      if (filters.minRating != null) 'rating': '${filters.minRating}',
      if (filters.language != null) 'lang': filters.language!,
      if (filters.providerIds.isNotEmpty) 'prov': filters.providerIds.join(','),
      if (filters.providerIds.isNotEmpty && filters.watchRegion != null)
        'region': filters.watchRegion!,
      if (filters.keywords.isNotEmpty)
        'kw': filters.keywords.map((k) => k.id).join(','),
      if (filters.companies.isNotEmpty)
        'co': filters.companies.map((c) => c.id).join(','),
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
        genreIds: _idList(q['genres']),
        yearFrom: _year(q['from']),
        yearTo: _year(q['to']),
        minRating: _rating(q['rating']),
        language: _languageCode(q['lang']),
        providerIds: _idList(q['prov']),
        watchRegion: _region(q['region']),
        keywords: [for (final id in _idList(q['kw'])) TaggedId(id: id)],
        companies: [for (final id in _idList(q['co'])) TaggedId(id: id)],
      ),
    );
  }

  static int? _positiveInt(String? raw) {
    final value = int.tryParse(raw?.trim() ?? '');
    return value != null && value > 0 ? value : null;
  }

  /// Comma-separated positive ids; junk entries drop out.
  static List<int> _idList(String? raw) => [
        for (final part in (raw ?? '').split(','))
          if (_positiveInt(part) case final id?) id,
      ];

  static final _languagePattern = RegExp(r'^[a-z]{2,3}$');
  static final _regionPattern = RegExp(r'^[A-Z]{2}$');

  static String? _languageCode(String? raw) {
    final code = raw?.trim().toLowerCase() ?? '';
    return _languagePattern.hasMatch(code) ? code : null;
  }

  static String? _region(String? raw) {
    final code = raw?.trim().toUpperCase() ?? '';
    return _regionPattern.hasMatch(code) ? code : null;
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
