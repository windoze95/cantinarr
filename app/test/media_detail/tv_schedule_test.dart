import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/tv_schedule.dart';
import 'package:flutter_test/flutter_test.dart';

import 'tv_schedule_fixtures.dart';

final _now = DateTime(2026, 9, 12, 23, 59);

String? _label(Map<String, dynamic> json, {DateTime? now}) =>
    upcomingTVDateLabel(TVDetail.fromJson({'id': 123, ...json}), now: now ?? _now);

Map<String, dynamic> _season(int number, String? date) => {
  'id': number + 1, 'season_number': number, 'air_date': date,
};

void main() {
  group('upcomingTVDateLabel', () {
    test('Carrie premieres on the fixed October date', () {
      expect(_label(carrieTVFixture), 'Premieres Oct 7, 2026');
    });

    test('series premiere wins over episode and season dates', () {
      expect(_label({
        ...carrieTVFixture,
        'next_episode_to_air': {
          'season_number': 1, 'episode_number': 2, 'air_date': '2026-10-14',
        },
        'seasons': [_season(1, '2026-10-08'), _season(2, '2027-10-07')],
      }), 'Premieres Oct 7, 2026');
    });

    for (final date in [null, '', '2026', '2026-02-30']) {
      test('Season 1 supplies the premiere when first date is $date', () {
        expect(_label({...carrieTVFixture, 'first_air_date': date}),
            'Premieres Oct 7, 2026');
      });
    }

    test('returning season first episode is a season premiere', () {
      expect(_label({
        ...returningTVFixture,
        'next_episode_to_air': {
          'season_number': 2, 'episode_number': 1, 'air_date': '2026-10-07',
        },
      }), 'Season 2 premieres Oct 7, 2026');
    });

    test('an explicitly scheduled first episode can supply a new premiere', () {
      expect(_label({
        'next_episode_to_air': {
          'season_number': 1, 'episode_number': 1, 'air_date': '2026-10-07',
        },
      }), 'Premieres Oct 7, 2026');
    });

    test('a regular next episode wins over an upcoming season', () {
      expect(_label({
        ...returningTVFixture,
        'seasons': [_season(3, '2026-10-01')],
      }), 'Next episode S2 E3 · Oct 14, 2026');
    });

    for (final nextDate in [null, '', 'soon', '2026-02-30', '2026-09-11']) {
      test('earliest real season replaces an unusable next date $nextDate', () {
        expect(_label({
          ...returningTVFixture,
          'next_episode_to_air': {'id': 12, 'air_date': nextDate},
          'seasons': [
            _season(0, '2026-09-12'),
            _season(4, '2027-10-07'),
            _season(3, '2026-10-07'),
            _season(2, '2026-10-07'),
            _season(1, '2024-10-02'),
          ],
        }), 'Season 2 premieres Oct 7, 2026');
      });
    }

    test('season fallback also works when no next episode is listed', () {
      expect(_label({
        ...returningTVFixture,
        'next_episode_to_air': null,
        'seasons': [_season(2, '2026-10-07')],
      }), 'Season 2 premieres Oct 7, 2026');
    });

    test('Specials never supply the series or season premiere fallback', () {
      expect(_label({'seasons': [_season(0, '2026-10-07')]}), isNull);
      expect(_label({
        'status': 'Planned', 'seasons': [_season(0, '2026-10-07')],
      }), 'Premiere date TBA');
    });

    test('an explicitly scheduled Special retains its episode date', () {
      expect(_label({
        'next_episode_to_air': {
          'season_number': 0, 'episode_number': 1, 'air_date': '2026-10-07',
        },
      }), 'Next episode S0 E1 · Oct 7, 2026');
    });

    for (final numbers in <Map<String, dynamic>>[
      {}, {'season_number': 2}, {'episode_number': 1},
      {'season_number': -1, 'episode_number': 3},
      {'season_number': 2, 'episode_number': 0},
    ]) {
      test('partial or invalid episode numbers use a generic label: $numbers', () {
        expect(_label({
          'next_episode_to_air': {...numbers, 'air_date': '2026-10-14'},
        }), 'Next episode · Oct 14, 2026');
      });
    }

    for (final status in ['Planned', 'Pilot', 'In Production']) {
      test('an unaired $status title has premiere TBA', () {
        expect(_label({'status': status}), 'Premiere date TBA');
        expect(_label({'status': status, 'first_air_date': '2026-02-30'}),
            'Premiere date TBA');
      });
      test('an aired $status title does not invent another premiere', () {
        for (final evidence in <Map<String, dynamic>>[
          {'first_air_date': '2024-10-02'},
          {'last_episode_to_air': {'id': 11}},
          {'seasons': [_season(2, '2024-10-02')]},
        ]) {
          expect(_label({'status': status, ...evidence}), isNull);
        }
      });
    }

    test('returning series with no scheduled episode has next episode TBA', () {
      expect(_label({...returningTVFixture, 'next_episode_to_air': null}),
          'Next episode date TBA');
    });

    test('an explicitly listed episode with no usable date has next TBA', () {
      for (final date in [null, '', 'soon', '2026', '2026-02-30']) {
        expect(_label({'next_episode_to_air': {'id': 12, 'air_date': date}}),
            'Next episode date TBA');
      }
    });

    for (final status in ['Ended', 'Canceled', 'Cancelled']) {
      test('$status does not imply TBA, even with an undated next episode', () {
        expect(_label({'status': status}), isNull);
        expect(_label({
          'status': status, 'next_episode_to_air': {'id': 12},
        }), isNull);
      });
      test('$status still shows a future dated event', () {
        expect(_label({...returningTVFixture, 'status': status}),
            'Next episode S2 E3 · Oct 14, 2026');
        expect(_label({
          'status': status, 'seasons': [_season(2, '2026-10-07')],
        }), 'Season 2 premieres Oct 7, 2026');
      });
    }

    test('missing metadata alone never promises another episode', () {
      expect(_label({}), isNull);
      expect(_label({'first_air_date': '2024-10-02'}), isNull);
      expect(_label({'last_episode_to_air': {'id': 11}}), isNull);
      expect(_label({'next_episode_to_air': {}}), isNull);
    });

    test('a stale past episode is hidden; returning status can still say TBA', () {
      final json = <String, dynamic>{
        'next_episode_to_air': {
          'season_number': 2, 'episode_number': 3, 'air_date': '2026-09-11',
        },
      };
      expect(_label(json), isNull);
      expect(_label({...json, 'status': 'Returning Series'}),
          'Next episode date TBA');
    });

    test('premiere includes today but disappears at the next calendar day', () {
      expect(_label(carrieTVFixture, now: DateTime(2026, 10, 7, 23, 59)),
          'Premieres today');
      expect(_label(carrieTVFixture, now: DateTime(2026, 10, 8)), isNull);
    });

    test('regular episodes and season premieres use today too', () {
      expect(_label(returningTVFixture, now: DateTime(2026, 10, 14, 23)),
          'Next episode S2 E3 · today');
      expect(_label({
        'seasons': [_season(2, '2026-10-07')],
      }, now: DateTime(2026, 10, 7, 23)), 'Season 2 premieres today');
    });

    test('local and UTC clocks use their calendar components without shifting', () {
      for (final now in [
        DateTime(2026, 10, 7, 0, 1), DateTime(2026, 10, 7, 23, 59),
        DateTime.utc(2026, 10, 7, 0, 1), DateTime.utc(2026, 10, 7, 23, 59),
      ]) {
        expect(_label(carrieTVFixture, now: now), 'Premieres today');
      }
    });

    test('dates always include the year, across a year boundary too', () {
      expect(_label({'first_air_date': '2027-01-01'},
          now: DateTime(2026, 12, 31, 23, 59)), 'Premieres Jan 1, 2027');
    });

    test('a leap-day premiere lasts through February 29, then becomes past', () {
      final json = {'first_air_date': '2028-02-29'};
      expect(_label(json, now: DateTime(2028, 2, 28)), 'Premieres Feb 29, 2028');
      expect(_label(json, now: DateTime(2028, 2, 29, 23, 59)), 'Premieres today');
      expect(_label(json, now: DateTime(2028, 3, 1)), isNull);
    });
  });

  group('parseTVDate', () {
    test('complete dates preserve their components, including leap days', () {
      for (final value in ['2026-10-07', '2028-02-29', '2000-02-29']) {
        final date = parseTVDate(value)!;
        expect(date.toIso8601String(), '${value}T00:00:00.000Z');
      }
    });
    for (final value in [
      null, '', 'soon', '2026', '2026-10', '20261007', '2026-1-07',
      '2026-10-7', '2026-00-07', '2026-13-07', '2026-10-00',
      '2026-10-32', '2026-04-31', '2026-02-29', '1900-02-29',
      '2100-02-29', '0000-10-07', ' 2026-10-07', '2026-10-07 ',
      '2026-10-07\n',
      '2026-10-07T00:00:00Z', '2026-10-07T23:00:00-07:00',
    ]) {
      test('rejects malformed or impossible date $value', () {
        expect(parseTVDate(value), isNull);
      });
    }
  });
}
