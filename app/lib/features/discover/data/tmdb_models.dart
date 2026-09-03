/// Media type enum matching TMDB's multi-search results.
enum MediaType {
  movie,
  tv,
  person;

  String get displayName => switch (this) {
        movie => 'Movie',
        tv => 'TV Show',
        person => 'Person',
      };
}

/// A paginated response wrapper from TMDB.
class TmdbPage<T> {
  final int page;
  final int totalPages;
  final int totalResults;
  final List<T> results;

  const TmdbPage({
    required this.page,
    required this.totalPages,
    required this.totalResults,
    required this.results,
  });

  factory TmdbPage.fromJson(
    Map<String, dynamic> json,
    T Function(Map<String, dynamic>) fromJsonT,
  ) =>
      TmdbPage(
        page: json['page'] as int? ?? 1,
        totalPages: json['total_pages'] as int? ?? 1,
        totalResults: json['total_results'] as int? ?? 0,
        results: (json['results'] as List<dynamic>?)
                ?.map((e) => fromJsonT(e as Map<String, dynamic>))
                .toList() ??
            [],
      );

  bool get hasMore => page < totalPages;
}

/// Lightweight display model used across the app.
class MediaItem {
  final int id;
  final String title;
  final String? posterPath;
  final String? backdropPath;
  final MediaType mediaType;
  final double? voteAverage;
  final String? releaseDate;
  final String? overview;

  const MediaItem({
    required this.id,
    required this.title,
    this.posterPath,
    this.backdropPath,
    required this.mediaType,
    this.voteAverage,
    this.releaseDate,
    this.overview,
  });

  factory MediaItem.fromTrendingJson(Map<String, dynamic> json) {
    final type = json['media_type'] as String?;
    final mediaType = type == 'tv' ? MediaType.tv : MediaType.movie;
    return MediaItem(
      id: json['id'] as int,
      title: (json['title'] ?? json['name'] ?? 'Untitled') as String,
      posterPath: json['poster_path'] as String?,
      backdropPath: json['backdrop_path'] as String?,
      mediaType: mediaType,
      voteAverage: (json['vote_average'] as num?)?.toDouble(),
      releaseDate:
          (json['release_date'] ?? json['first_air_date']) as String?,
      overview: json['overview'] as String?,
    );
  }

  factory MediaItem.fromMovieJson(Map<String, dynamic> json) => MediaItem(
        id: json['id'] as int,
        title: (json['title'] ?? 'Untitled') as String,
        posterPath: json['poster_path'] as String?,
        backdropPath: json['backdrop_path'] as String?,
        mediaType: MediaType.movie,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        releaseDate: json['release_date'] as String?,
        overview: json['overview'] as String?,
      );

  factory MediaItem.fromTVJson(Map<String, dynamic> json) => MediaItem(
        id: json['id'] as int,
        title: (json['name'] ?? 'Untitled') as String,
        posterPath: json['poster_path'] as String?,
        backdropPath: json['backdrop_path'] as String?,
        mediaType: MediaType.tv,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        releaseDate: json['first_air_date'] as String?,
        overview: json['overview'] as String?,
      );

  factory MediaItem.fromMultiSearchJson(Map<String, dynamic> json) {
    final type = json['media_type'] as String?;
    if (type == 'person') {
      return MediaItem(
        id: json['id'] as int,
        title: (json['name'] ?? 'Unknown') as String,
        posterPath: json['profile_path'] as String?,
        mediaType: MediaType.person,
      );
    }
    return MediaItem.fromTrendingJson(json);
  }
}

/// Genre info from TMDB.
class Genre {
  final int id;
  final String name;

  const Genre({required this.id, required this.name});

  factory Genre.fromJson(Map<String, dynamic> json) => Genre(
        id: json['id'] as int,
        name: json['name'] as String,
      );
}

/// Watch provider info from TMDB.
class WatchProvider {
  final int providerId;
  final String providerName;
  final String? logoPath;

  /// TMDB's ordering hint for the region; lower comes first.
  final int displayPriority;

  const WatchProvider({
    required this.providerId,
    required this.providerName,
    this.logoPath,
    this.displayPriority = 0,
  });

  factory WatchProvider.fromJson(Map<String, dynamic> json) => WatchProvider(
        providerId: json['provider_id'] as int,
        providerName: json['provider_name'] as String,
        logoPath: json['logo_path'] as String?,
        displayPriority: json['display_priority'] as int? ?? 0,
      );
}

/// A language TMDB can filter on, labelled for a picker.
class TmdbLanguage {
  final String code;
  final String englishName;

  const TmdbLanguage({required this.code, required this.englishName});

  factory TmdbLanguage.fromJson(Map<String, dynamic> json) {
    final code = json['iso_639_1'] as String;
    final english = json['english_name'] as String?;
    final native = json['name'] as String?;
    return TmdbLanguage(
      code: code,
      englishName: english != null && english.isNotEmpty
          ? english
          : (native != null && native.isNotEmpty ? native : code),
    );
  }
}

/// A country TMDB tracks streaming availability for.
class WatchRegion {
  final String code;
  final String name;

  const WatchRegion({required this.code, required this.name});

  factory WatchRegion.fromJson(Map<String, dynamic> json) {
    final code = json['iso_3166_1'] as String;
    final english = json['english_name'] as String?;
    final native = json['native_name'] as String?;
    return WatchRegion(
      code: code,
      name: english != null && english.isNotEmpty
          ? english
          : (native != null && native.isNotEmpty ? native : code),
    );
  }
}

/// A TMDB keyword or production company: the id a filter sends and the name
/// a chip shows. The name is null when only the id is known (a browse link),
/// and a surface then names it by number.
class TaggedId {
  final int id;
  final String? name;

  const TaggedId({required this.id, this.name});

  factory TaggedId.fromJson(Map<String, dynamic> json) => TaggedId(
        id: json['id'] as int,
        name: json['name'] as String?,
      );

  @override
  bool operator ==(Object other) =>
      other is TaggedId && other.id == id && other.name == name;

  @override
  int get hashCode => Object.hash(id, name);

  @override
  String toString() => 'TaggedId($id, $name)';
}

/// Full movie detail from TMDB.
class MovieDetail {
  final int id;
  final String title;
  final String? tagline;
  final String? overview;
  final String? posterPath;
  final String? backdropPath;
  final double? voteAverage;
  final int? runtime;
  final String? releaseDate;
  final String? status;
  final List<Genre> genres;
  final List<Video> videos;
  final int? budget;
  final int? revenue;
  final List<TmdbReleaseDateRegion> releaseDates;

  const MovieDetail({
    required this.id,
    required this.title,
    this.tagline,
    this.overview,
    this.posterPath,
    this.backdropPath,
    this.voteAverage,
    this.runtime,
    this.releaseDate,
    this.status,
    this.genres = const [],
    this.videos = const [],
    this.budget,
    this.revenue,
    this.releaseDates = const [],
  });

  factory MovieDetail.fromJson(Map<String, dynamic> json) => MovieDetail(
        id: json['id'] as int,
        title: (json['title'] ?? 'Untitled') as String,
        tagline: json['tagline'] as String?,
        overview: json['overview'] as String?,
        posterPath: json['poster_path'] as String?,
        backdropPath: json['backdrop_path'] as String?,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        runtime: json['runtime'] as int?,
        releaseDate: json['release_date'] as String?,
        status: json['status'] as String?,
        genres: (json['genres'] as List<dynamic>?)
                ?.map((g) => Genre.fromJson(g as Map<String, dynamic>))
                .toList() ??
            [],
        videos: _parseVideos(json),
        budget: json['budget'] as int?,
        revenue: json['revenue'] as int?,
        releaseDates: _parseReleaseDates(json),
      );

  String? get trailerKey {
    final trailer = videos.where((v) =>
        v.type?.toLowerCase() == 'trailer' &&
        v.site?.toLowerCase() == 'youtube');
    if (trailer.isNotEmpty) return trailer.first.key;
    final any = videos.where((v) => v.site?.toLowerCase() == 'youtube');
    return any.isNotEmpty ? any.first.key : null;
  }
}

/// Full TV detail from TMDB.
class TVDetail {
  final int id;
  final String name;
  final String? tagline;
  final String? overview;
  final String? posterPath;
  final String? backdropPath;
  final double? voteAverage;
  final String? firstAirDate;
  final String? status;
  final int? numberOfSeasons;
  final int? numberOfEpisodes;
  final List<Genre> genres;
  final List<Video> videos;
  final List<Season> seasons;
  final ExternalIds? externalIds;

  const TVDetail({
    required this.id,
    required this.name,
    this.tagline,
    this.overview,
    this.posterPath,
    this.backdropPath,
    this.voteAverage,
    this.firstAirDate,
    this.status,
    this.numberOfSeasons,
    this.numberOfEpisodes,
    this.genres = const [],
    this.videos = const [],
    this.seasons = const [],
    this.externalIds,
  });

  factory TVDetail.fromJson(Map<String, dynamic> json) => TVDetail(
        id: json['id'] as int,
        name: (json['name'] ?? 'Untitled') as String,
        tagline: json['tagline'] as String?,
        overview: json['overview'] as String?,
        posterPath: json['poster_path'] as String?,
        backdropPath: json['backdrop_path'] as String?,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        firstAirDate: json['first_air_date'] as String?,
        status: json['status'] as String?,
        numberOfSeasons: json['number_of_seasons'] as int?,
        numberOfEpisodes: json['number_of_episodes'] as int?,
        genres: (json['genres'] as List<dynamic>?)
                ?.map((g) => Genre.fromJson(g as Map<String, dynamic>))
                .toList() ??
            [],
        videos: _parseVideos(json),
        seasons: (json['seasons'] as List<dynamic>?)
                ?.map((s) => Season.fromJson(s as Map<String, dynamic>))
                .toList() ??
            [],
        externalIds: json['external_ids'] is Map<String, dynamic>
            ? ExternalIds.fromJson(json['external_ids'] as Map<String, dynamic>)
            : null,
      );

  String? get trailerKey {
    final trailer = videos.where((v) =>
        v.type?.toLowerCase() == 'trailer' &&
        v.site?.toLowerCase() == 'youtube');
    if (trailer.isNotEmpty) return trailer.first.key;
    final any = videos.where((v) => v.site?.toLowerCase() == 'youtube');
    return any.isNotEmpty ? any.first.key : null;
  }
}

/// External IDs (TVDB, IMDb, etc.) from TMDB.
class ExternalIds {
  final int? tvdbId;
  final String? imdbId;

  const ExternalIds({this.tvdbId, this.imdbId});

  factory ExternalIds.fromJson(Map<String, dynamic> json) => ExternalIds(
        tvdbId: json['tvdb_id'] as int?,
        imdbId: json['imdb_id'] as String?,
      );
}

/// One country's entries from TMDB's `release_dates` append
/// (`release_dates.results[]`): an ISO-3166-1 country code plus its raw
/// milestone list. Kept as transport shape only — interpreting which
/// milestones matter and how to label them is
/// `media_detail/logic/release_schedule.dart`'s job, not this file's.
class TmdbReleaseDateRegion {
  final String countryCode;
  final List<TmdbReleaseDateEntry> entries;

  const TmdbReleaseDateRegion({
    required this.countryCode,
    this.entries = const [],
  });

  factory TmdbReleaseDateRegion.fromJson(Map<String, dynamic> json) =>
      TmdbReleaseDateRegion(
        countryCode: (json['iso_3166_1'] ?? '') as String,
        entries: (json['release_dates'] as List<dynamic>?)
                ?.map((e) =>
                    TmdbReleaseDateEntry.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
      );
}

/// One release milestone entry within a region: TMDB's integer `type` (1
/// Premiere .. 6 TV) plus its parsed calendar date, or null when TMDB itself
/// doesn't know the date for that entry.
class TmdbReleaseDateEntry {
  final int type;
  final DateTime? date;

  const TmdbReleaseDateEntry({required this.type, this.date});

  factory TmdbReleaseDateEntry.fromJson(Map<String, dynamic> json) =>
      TmdbReleaseDateEntry(
        type: json['type'] as int? ?? 0,
        date: _parseTmdbCalendarDate(json['release_date'] as String?),
      );
}

/// A video (trailer, teaser, etc.) from TMDB.
class Video {
  final String? key;
  final String? site;
  final String? type;
  final String? name;

  const Video({this.key, this.site, this.type, this.name});

  factory Video.fromJson(Map<String, dynamic> json) => Video(
        key: json['key'] as String?,
        site: json['site'] as String?,
        type: json['type'] as String?,
        name: json['name'] as String?,
      );
}

/// TV season info.
class Season {
  final int id;
  final int seasonNumber;
  final String? name;
  final String? posterPath;
  final int? episodeCount;
  final String? airDate;

  const Season({
    required this.id,
    required this.seasonNumber,
    this.name,
    this.posterPath,
    this.episodeCount,
    this.airDate,
  });

  factory Season.fromJson(Map<String, dynamic> json) => Season(
        id: json['id'] as int,
        seasonNumber: json['season_number'] as int,
        name: json['name'] as String?,
        posterPath: json['poster_path'] as String?,
        episodeCount: json['episode_count'] as int?,
        airDate: json['air_date'] as String?,
      );
}

/// Full person detail from TMDB.
class PersonDetail {
  final int id;
  final String name;
  final String? biography;
  final String? birthday;
  final String? deathday;
  final String? placeOfBirth;
  final String? profilePath;
  final String? knownForDepartment;
  final List<String> alsoKnownAs;

  const PersonDetail({
    required this.id,
    required this.name,
    this.biography,
    this.birthday,
    this.deathday,
    this.placeOfBirth,
    this.profilePath,
    this.knownForDepartment,
    this.alsoKnownAs = const [],
  });

  factory PersonDetail.fromJson(Map<String, dynamic> json) => PersonDetail(
        id: json['id'] as int,
        name: (json['name'] ?? 'Unknown') as String,
        biography: json['biography'] as String?,
        birthday: json['birthday'] as String?,
        deathday: json['deathday'] as String?,
        placeOfBirth: json['place_of_birth'] as String?,
        profilePath: json['profile_path'] as String?,
        knownForDepartment: json['known_for_department'] as String?,
        alsoKnownAs: (json['also_known_as'] as List<dynamic>?)
                ?.map((e) => e as String)
                .toList() ??
            [],
      );

  int? get age {
    if (birthday == null) return null;
    final birth = DateTime.tryParse(birthday!);
    if (birth == null) return null;
    final end = deathday != null ? DateTime.tryParse(deathday!) : null;
    final ref = end ?? DateTime.now();
    int a = ref.year - birth.year;
    if (ref.month < birth.month ||
        (ref.month == birth.month && ref.day < birth.day)) {
      a--;
    }
    return a;
  }
}

/// A single credit entry (cast or crew) from TMDB combined_credits.
class PersonCredit {
  final int id;
  final String title;
  final String mediaType;
  final String? posterPath;
  final double? voteAverage;
  final String? releaseDate;
  final String? character;
  final String? job;
  final String? overview;

  const PersonCredit({
    required this.id,
    required this.title,
    required this.mediaType,
    this.posterPath,
    this.voteAverage,
    this.releaseDate,
    this.character,
    this.job,
    this.overview,
  });

  factory PersonCredit.fromJson(Map<String, dynamic> json) => PersonCredit(
        id: json['id'] as int,
        title: (json['title'] ?? json['name'] ?? 'Untitled') as String,
        mediaType: (json['media_type'] ?? 'movie') as String,
        posterPath: json['poster_path'] as String?,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        releaseDate:
            (json['release_date'] ?? json['first_air_date']) as String?,
        character: json['character'] as String?,
        job: json['job'] as String?,
        overview: json['overview'] as String?,
      );

  String? get year {
    if (releaseDate == null || releaseDate!.length < 4) return null;
    return releaseDate!.substring(0, 4);
  }
}

/// Year of a TMDB date string (`release_date` / `first_air_date`), or null
/// when absent or unparseable.
int? tmdbPremiereYear(String? date) {
  if (date == null || date.length < 4) return null;
  return int.tryParse(date.substring(0, 4));
}

/// Helper to parse the nested videos response.
List<Video> _parseVideos(Map<String, dynamic> json) {
  final videosData = json['videos'];
  if (videosData is Map<String, dynamic>) {
    return (videosData['results'] as List<dynamic>?)
            ?.map((v) => Video.fromJson(v as Map<String, dynamic>))
            .toList() ??
        [];
  }
  return [];
}

/// Helper to parse the nested `release_dates` append. Tolerates a missing or
/// wrongly-shaped key by yielding an empty list rather than throwing: a
/// server that hasn't yet added the append, or is serving a cached
/// pre-change body, sends exactly that shape, and this must not blank the
/// rest of the detail page over it.
List<TmdbReleaseDateRegion> _parseReleaseDates(Map<String, dynamic> json) {
  final releaseDatesData = json['release_dates'];
  if (releaseDatesData is Map<String, dynamic>) {
    return (releaseDatesData['results'] as List<dynamic>?)
            ?.whereType<Map<String, dynamic>>()
            .map((r) => TmdbReleaseDateRegion.fromJson(r))
            .toList() ??
        [];
  }
  return [];
}

/// Parses a `YYYY-MM-DDTHH:mm:ss.SSSZ` TMDB release-date string into local
/// midnight, reading only the `YYYY-MM-DD` calendar prefix. TMDB's release
/// dates carry no meaningful time-of-day, so `DateTime.parse(...).toLocal()`
/// would shift a midnight-UTC date onto the previous day for anyone west of
/// UTC — the same trap `radarr/client.go` and
/// `request_service.dart:_parseCalendarDate` already document. Returns null
/// for a missing or unparseable value.
DateTime? _parseTmdbCalendarDate(String? value) {
  if (value == null || value.length < 10) return null;
  final year = int.tryParse(value.substring(0, 4));
  final month = int.tryParse(value.substring(5, 7));
  final day = int.tryParse(value.substring(8, 10));
  if (year == null || month == null || day == null) return null;
  return DateTime(year, month, day);
}
