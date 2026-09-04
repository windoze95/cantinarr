import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/title_links.dart';
import 'package:flutter_test/flutter_test.dart';

/// The Links line of a title page: which sites earn a chip, in what order,
/// and the exact page each one opens.
void main() {
  test('a movie with an IMDb id links IMDb, TMDB, and Trakt in that order',
      () {
    expect(
      titleLinks(type: MediaType.movie, tmdbId: 603, imdbId: 'tt0133093'),
      const [
        TitleLink('IMDb', 'https://www.imdb.com/title/tt0133093/'),
        TitleLink('TMDB', 'https://www.themoviedb.org/movie/603'),
        TitleLink('Trakt', 'https://trakt.tv/movies/tt0133093'),
      ],
    );
  });

  test('a show links its TMDB tv page, and IMDb and Trakt from its IMDb id',
      () {
    expect(
      titleLinks(type: MediaType.tv, tmdbId: 1396, imdbId: 'tt0903747'),
      const [
        TitleLink('IMDb', 'https://www.imdb.com/title/tt0903747/'),
        TitleLink('TMDB', 'https://www.themoviedb.org/tv/1396'),
        TitleLink('Trakt', 'https://trakt.tv/shows/tt0903747'),
      ],
    );
  });

  test('without an IMDb id only TMDB is linked, never a guessed page', () {
    expect(
      titleLinks(type: MediaType.tv, tmdbId: 1396),
      const [TitleLink('TMDB', 'https://www.themoviedb.org/tv/1396')],
    );
    expect(
      titleLinks(type: MediaType.movie, tmdbId: 603, imdbId: '  '),
      const [TitleLink('TMDB', 'https://www.themoviedb.org/movie/603')],
    );
  });

  test('an id that is not IMDb-shaped is treated as unknown', () {
    expect(
      titleLinks(type: MediaType.movie, tmdbId: 603, imdbId: 'nm0000206'),
      const [TitleLink('TMDB', 'https://www.themoviedb.org/movie/603')],
    );
  });
}
