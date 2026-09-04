import 'package:cantinarr/features/discover/data/trakt_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('TraktItem.toMediaItem', () {
    test('carries the TMDB id, which is what detail routes open', () {
      final item = TraktItem.fromAnticipatedJson({
        'movie': {
          'title': 'Known',
          'year': 2027,
          'ids': {'trakt': 9, 'tmdb': 603},
        },
      }, 'movies');
      expect(item.toMediaItem().id, 603);
    });

    test('never substitutes the Trakt id for a missing TMDB id', () {
      final item = TraktItem.fromAnticipatedJson({
        'movie': {
          'title': 'Unknown to TMDB',
          'year': 2027,
          'ids': {'trakt': 9},
        },
      }, 'movies');
      // A Trakt id sent as a TMDB id would open some unrelated title.
      expect(item.toMediaItem().id, 0);
    });
  });
}
