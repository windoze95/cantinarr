import 'package:cantinarr/core/config/app_config.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('absolute library posters use the same requested TMDB size as relative paths', () {
    for (final path in ['/poster.jpg',
      'https://image.tmdb.org/t/p/original/poster.jpg',
      'https://image.tmdb.org/t/p/w500/poster.jpg']) {
      expect(AppConfig.tmdbPoster(path, width: 342),
          'https://image.tmdb.org/t/p/w342/poster.jpg');
      expect(AppConfig.tmdbPosterForDisplay(path, 124 * 1.025),
          'https://image.tmdb.org/t/p/w154/poster.jpg');
      expect(AppConfig.tmdbPosterForDisplay(path, 124 * 2 * 1.025),
          'https://image.tmdb.org/t/p/w342/poster.jpg');
      expect(AppConfig.tmdbPosterForDisplay(path, 124 * 3 * 1.025),
          'https://image.tmdb.org/t/p/w500/poster.jpg');
    }
    expect(AppConfig.tmdbPosterForDisplay('/poster.jpg', 900),
        'https://image.tmdb.org/t/p/original/poster.jpg');
    expect(AppConfig.tmdbPosterForDisplay(null, 124), isEmpty);
    expect(AppConfig.tmdbPosterForDisplay('', 124), isEmpty);
  });

  test('thumbnail selection preserves unrelated, cropped, and vector image URLs', () {
    for (final url in [
      'https://cdn.example/t/p/original/poster.jpg',
      'https://image.tmdb.org.example/t/p/original/poster.jpg',
      'https://image.tmdb.org/images/poster.jpg',
      'https://image.tmdb.org/t/p/w300_and_h450_face/poster.jpg',
      'https://image.tmdb.org/t/p/original/logo.svg',
      'https://image.tmdb.org:8080/t/p/original/poster.jpg',
      'https://cantina.example/api/instances/1/poster.jpg?token=test',
    ]) {
      expect(AppConfig.tmdbPosterForDisplay(url, 124), url);
    }
    expect(AppConfig.tmdbPoster('https://image.tmdb.org/t/p/original/poster.jpg?v=2#art', width: 185),
        'https://image.tmdb.org/t/p/w185/poster.jpg?v=2#art');
  });

  test('backdrops keep their requested size independently of poster cards', () {
    expect(AppConfig.tmdbBackdrop('https://image.tmdb.org/t/p/original/backdrop.jpg', width: 1280),
        'https://image.tmdb.org/t/p/w1280/backdrop.jpg');
    expect(AppConfig.tmdbBackdropOriginal('https://image.tmdb.org/t/p/w780/backdrop.jpg'),
        'https://image.tmdb.org/t/p/original/backdrop.jpg');
  });
}
