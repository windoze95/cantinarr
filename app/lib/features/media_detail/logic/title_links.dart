import '../../discover/data/tmdb_models.dart';

/// One place to read about a title off-app: a short site name and this
/// title's own page there. A link exists only when the id it needs is
/// known, so nothing is ever guessed from a title.
class TitleLink {
  final String label;
  final String url;

  const TitleLink(this.label, this.url);

  @override
  bool operator ==(Object other) =>
      other is TitleLink && other.label == label && other.url == url;

  @override
  int get hashCode => Object.hash(label, url);

  @override
  String toString() => 'TitleLink($label: $url)';
}

/// IMDb ids are `tt` and digits; anything else TMDB might carry in the
/// field is not a page to send someone to.
final _imdbId = RegExp(r'^tt\d+$');

/// The sites a movie or show can be looked up on, in a fixed order: IMDb
/// when the IMDb id is known, TMDB when its id is known (always, on a page
/// built from TMDB's own record; a Radarr movie normally carries it too),
/// then Trakt, which resolves an IMDb id under its own `/movies/` and
/// `/shows/` paths, so no Trakt id is needed. Trakt's older
/// `/search/imdb/<id>` form is gone -- it answers 404 -- and its app renders
/// client-side, so a link there has to name the media type up front. Shared
/// by the requester title page's Links row and the Radarr movie page's
/// links sheet, so the two never drift apart again.
List<TitleLink> titleLinks({
  required MediaType type,
  int? tmdbId,
  String? imdbId,
}) {
  final imdb = imdbId?.trim() ?? '';
  final known = _imdbId.hasMatch(imdb);
  final tmdb = tmdbId ?? 0;
  final isShow = type == MediaType.tv;
  // The two sites name the same thing differently: TMDB's path segment is
  // `tv`, Trakt's is `shows`.
  final tmdbKind = isShow ? 'tv' : 'movie';
  final traktKind = isShow ? 'shows' : 'movies';
  return [
    if (known) TitleLink('IMDb', 'https://www.imdb.com/title/$imdb/'),
    if (tmdb > 0) TitleLink('TMDB', 'https://www.themoviedb.org/$tmdbKind/$tmdb'),
    if (known) TitleLink('Trakt', 'https://trakt.tv/$traktKind/$imdb'),
  ];
}
