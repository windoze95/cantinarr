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

/// The Links line of a title page's Details, in a fixed order: IMDb when
/// TMDB knows the title's IMDb id, TMDB itself always (the page is built from
/// its record), then Trakt, which resolves an IMDb id to its own page for
/// movies and shows alike, so no Trakt id is needed.
List<TitleLink> titleLinks({
  required MediaType type,
  required int tmdbId,
  String? imdbId,
}) {
  final imdb = imdbId?.trim() ?? '';
  final known = _imdbId.hasMatch(imdb);
  final kind = type == MediaType.tv ? 'tv' : 'movie';
  return [
    if (known) TitleLink('IMDb', 'https://www.imdb.com/title/$imdb/'),
    TitleLink('TMDB', 'https://www.themoviedb.org/$kind/$tmdbId'),
    if (known) TitleLink('Trakt', 'https://trakt.tv/search/imdb/$imdb'),
  ];
}
