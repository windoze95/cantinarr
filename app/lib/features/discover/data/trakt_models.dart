import 'tmdb_models.dart';

/// IDs block from the Trakt API.
class TraktIds {
  final int? trakt;
  final String? slug;
  final int? tmdb;
  final int? tvdb;
  final String? imdb;

  const TraktIds({this.trakt, this.slug, this.tmdb, this.tvdb, this.imdb});

  factory TraktIds.fromJson(Map<String, dynamic> json) => TraktIds(
        trakt: json['trakt'] as int?,
        slug: json['slug'] as String?,
        tmdb: json['tmdb'] as int?,
        tvdb: json['tvdb'] as int?,
        imdb: json['imdb'] as String?,
      );
}

/// A movie or show returned by Trakt endpoints.
class TraktItem {
  final int? tmdbId;
  final String title;
  final int? year;
  final String? overview;
  final TraktIds ids;
  final int? watchers;
  final String mediaType; // "movie" or "show"

  const TraktItem({
    this.tmdbId,
    required this.title,
    this.year,
    this.overview,
    required this.ids,
    this.watchers,
    required this.mediaType,
  });

  factory TraktItem.fromTrendingJson(
      Map<String, dynamic> json, String type) {
    final inner = json[type == 'movies' ? 'movie' : 'show']
        as Map<String, dynamic>;
    final ids = TraktIds.fromJson(inner['ids'] as Map<String, dynamic>? ?? {});
    return TraktItem(
      tmdbId: ids.tmdb,
      title: (inner['title'] ?? 'Untitled') as String,
      year: inner['year'] as int?,
      overview: inner['overview'] as String?,
      ids: ids,
      watchers: json['watchers'] as int?,
      mediaType: type == 'movies' ? 'movie' : 'show',
    );
  }

  factory TraktItem.fromPopularJson(
      Map<String, dynamic> json, String type) {
    final ids = TraktIds.fromJson(json['ids'] as Map<String, dynamic>? ?? {});
    return TraktItem(
      tmdbId: ids.tmdb,
      title: (json['title'] ?? 'Untitled') as String,
      year: json['year'] as int?,
      overview: json['overview'] as String?,
      ids: ids,
      mediaType: type == 'movies' ? 'movie' : 'show',
    );
  }

  /// Convert to the app's shared MediaItem model for UI reuse.
  MediaItem toMediaItem() => MediaItem(
        id: tmdbId ?? ids.trakt ?? 0,
        title: title,
        mediaType: mediaType == 'movie' ? MediaType.movie : MediaType.tv,
        releaseDate: year?.toString(),
        overview: overview,
      );
}

/// A user-curated list on Trakt.
class TraktList {
  final String name;
  final String? description;
  final int? itemCount;
  final String? slug;
  final TraktListUser? user;

  const TraktList({
    required this.name,
    this.description,
    this.itemCount,
    this.slug,
    this.user,
  });

  factory TraktList.fromJson(Map<String, dynamic> json) => TraktList(
        name: json['name'] as String? ?? 'Untitled',
        description: json['description'] as String?,
        itemCount: json['item_count'] as int?,
        slug: json['ids']?['slug'] as String?,
        user: json['user'] != null
            ? TraktListUser.fromJson(json['user'] as Map<String, dynamic>)
            : null,
      );

  String get listId =>
      user != null ? '${user!.username}/${slug ?? ''}' : slug ?? '';
}

class TraktListUser {
  final String username;
  const TraktListUser({required this.username});

  factory TraktListUser.fromJson(Map<String, dynamic> json) =>
      TraktListUser(username: json['ids']?['slug'] as String? ?? '');
}

/// A calendar entry from Trakt.
class TraktCalendarItem {
  final String? firstAired;
  final TraktItem item;

  const TraktCalendarItem({this.firstAired, required this.item});

  factory TraktCalendarItem.fromJson(Map<String, dynamic> json) {
    final showData = json['show'] as Map<String, dynamic>? ?? {};
    final ids =
        TraktIds.fromJson(showData['ids'] as Map<String, dynamic>? ?? {});
    return TraktCalendarItem(
      firstAired: json['first_aired'] as String?,
      item: TraktItem(
        tmdbId: ids.tmdb,
        title: (showData['title'] ?? 'Untitled') as String,
        year: showData['year'] as int?,
        ids: ids,
        mediaType: 'show',
      ),
    );
  }
}
