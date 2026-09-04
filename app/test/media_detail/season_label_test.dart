import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/season_label.dart';

/// Unit tests for [seasonRowLabel], the pure helper that appends a season's
/// first-air year to its title. Fixtures are built the same way
/// season_table_gating_test.dart builds them.
void main() {
  group('seasonRowLabel', () {
    test('TMDB name plus a real air date appends the year', () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        airDate: '2019-04-14',
      );
      expect(seasonRowLabel(season), 'Season 1 · 2019');
    });

    test('a custom TMDB season name appends the year', () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'The Final Season',
        airDate: '2019-04-14',
      );
      expect(seasonRowLabel(season), 'The Final Season · 2019');
    });

    test('no name falls back to the numbered form, with year', () {
      const season = Season(
        id: 3,
        seasonNumber: 3,
        name: null,
        airDate: '2021-06-01',
      );
      expect(seasonRowLabel(season), 'Season 3 · 2021');
    });

    test('blank name falls back to the numbered form, with year (D-04)', () {
      const season = Season(
        id: 3,
        seasonNumber: 3,
        name: '   ',
        airDate: '2021-06-01',
      );
      expect(seasonRowLabel(season), 'Season 3 · 2021');
    });

    test('null air date renders the bare title (D-03)', () {
      const season = Season(id: 1, seasonNumber: 1, name: 'Season 1');
      expect(seasonRowLabel(season), 'Season 1');
      _expectNoDanglingSeparator(seasonRowLabel(season));
    });

    test('empty-string air date renders the bare title (D-03)', () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        airDate: '',
      );
      expect(seasonRowLabel(season), 'Season 1');
      _expectNoDanglingSeparator(seasonRowLabel(season));
    });

    test('non-date air date "TBA" renders the bare title (D-03)', () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        airDate: 'TBA',
      );
      expect(seasonRowLabel(season), 'Season 1');
      _expectNoDanglingSeparator(seasonRowLabel(season));
    });

    test('non-date air date "unknown-date" renders the bare title (D-03)',
        () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        airDate: 'unknown-date',
      );
      expect(seasonRowLabel(season), 'Season 1');
      _expectNoDanglingSeparator(seasonRowLabel(season));
    });

    test('short air date renders the bare title (D-03)', () {
      const season = Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        airDate: '201',
      );
      expect(seasonRowLabel(season), 'Season 1');
      _expectNoDanglingSeparator(seasonRowLabel(season));
    });

    test('a future air date renders its year normally (D-05)', () {
      const season = Season(
        id: 5,
        seasonNumber: 5,
        name: null,
        airDate: '2031-04-01',
      );
      expect(seasonRowLabel(season), 'Season 5 · 2031');
    });
  });
}

void _expectNoDanglingSeparator(String label) {
  expect(label.endsWith('·'), isFalse);
  expect(label, label.trimRight());
}
