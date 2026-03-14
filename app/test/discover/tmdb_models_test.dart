import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';

void main() {
  group('MediaItem', () {
    test('fromMovieJson parses correctly', () {
      final json = {
        'id': 123,
        'title': 'Test Movie',
        'poster_path': '/poster.jpg',
        'backdrop_path': '/backdrop.jpg',
        'vote_average': 7.5,
        'release_date': '2024-01-15',
        'overview': 'A test movie.',
      };

      final item = MediaItem.fromMovieJson(json);
      expect(item.id, 123);
      expect(item.title, 'Test Movie');
      expect(item.posterPath, '/poster.jpg');
      expect(item.mediaType, MediaType.movie);
      expect(item.voteAverage, 7.5);
      expect(item.releaseDate, '2024-01-15');
    });

    test('fromTVJson parses correctly', () {
      final json = {
        'id': 456,
        'name': 'Test Show',
        'poster_path': '/poster.jpg',
        'first_air_date': '2023-06-01',
        'vote_average': 8.2,
        'overview': 'A test TV show.',
      };

      final item = MediaItem.fromTVJson(json);
      expect(item.id, 456);
      expect(item.title, 'Test Show');
      expect(item.mediaType, MediaType.tv);
      expect(item.voteAverage, 8.2);
    });

    test('handles missing fields gracefully', () {
      final json = <String, dynamic>{'id': 1};
      final item = MediaItem.fromMovieJson(json);
      expect(item.title, 'Untitled');
      expect(item.posterPath, isNull);
    });
  });

  group('TmdbPage', () {
    test('parses page metadata correctly', () {
      final json = {
        'page': 2,
        'total_pages': 10,
        'total_results': 200,
        'results': <dynamic>[],
      };

      final page = TmdbPage.fromJson(json, MediaItem.fromMovieJson);
      expect(page.page, 2);
      expect(page.totalPages, 10);
      expect(page.totalResults, 200);
      expect(page.hasMore, true);
    });

    test('hasMore is false on last page', () {
      final json = {
        'page': 5,
        'total_pages': 5,
        'total_results': 100,
        'results': <dynamic>[],
      };

      final page = TmdbPage.fromJson(json, MediaItem.fromMovieJson);
      expect(page.hasMore, false);
    });
  });

  group('MovieDetail', () {
    test('trailerKey extracts YouTube trailer', () {
      final json = {
        'id': 1,
        'title': 'Movie',
        'videos': {
          'results': [
            {'key': 'abc123', 'site': 'YouTube', 'type': 'Trailer', 'name': 'Official'},
            {'key': 'xyz789', 'site': 'YouTube', 'type': 'Teaser', 'name': 'Teaser'},
          ],
        },
      };

      final detail = MovieDetail.fromJson(json);
      expect(detail.trailerKey, 'abc123');
    });

    test('trailerKey falls back to any YouTube video', () {
      final json = {
        'id': 1,
        'title': 'Movie',
        'videos': {
          'results': [
            {'key': 'xyz789', 'site': 'YouTube', 'type': 'Teaser', 'name': 'Teaser'},
          ],
        },
      };

      final detail = MovieDetail.fromJson(json);
      expect(detail.trailerKey, 'xyz789');
    });
  });
}
