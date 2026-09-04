import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/release_schedule.dart';

/// The reference "now" every case is written against, matching the pattern
/// in release_window_test.dart so the suite never depends on the wall clock.
final _now = DateTime(2026, 6, 20);

TmdbReleaseDateEntry _entry(int type, DateTime? date) =>
    TmdbReleaseDateEntry(type: type, date: date);

TmdbReleaseDateRegion _region(String code, List<TmdbReleaseDateEntry> entries) =>
    TmdbReleaseDateRegion(countryCode: code, entries: entries);

List<String> _labels(ReleaseSchedule? schedule) =>
    schedule == null ? const [] : schedule.milestones.map((m) => m.label).toList();

void main() {
  group('resolveReleaseSchedule', () {
    test('a US region with theatrical, digital and physical entries produces '
        'three rows, labeled and sorted by date ascending', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [
            _entry(3, DateTime(2026, 7, 10)), // Theatrical
            _entry(4, DateTime(2026, 9, 1)), // Digital
            _entry(5, DateTime(2026, 11, 5)), // Physical
          ]),
        ],
        now: _now,
      );
      expect(schedule, isNotNull);
      expect(schedule!.regionCode, 'US');
      expect(_labels(schedule), ['In cinemas', 'Digital', 'Blu-ray / DVD']);
      expect(schedule.milestones.map((m) => m.date), [
        DateTime(2026, 7, 10),
        DateTime(2026, 9, 1),
        DateTime(2026, 11, 5),
      ]);
    });

    test('type 1 Premiere and type 6 TV entries never produce a row', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [
            _entry(1, DateTime(2026, 5, 1)), // Premiere
            _entry(3, DateTime(2026, 7, 10)), // Theatrical
            _entry(6, DateTime(2026, 8, 1)), // TV
          ]),
        ],
        now: _now,
      );
      expect(_labels(schedule), ['In cinemas']);
    });

    test('a region whose only entry is a Premiere is skipped during region '
        'resolution in favour of the next candidate region with a real '
        'milestone', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(1, DateTime(2026, 5, 1))]),
          _region('GB', [_entry(3, DateTime(2026, 7, 10))]),
        ],
        preferredRegion: 'US',
        now: _now,
      );
      expect(schedule, isNotNull);
      expect(schedule!.regionCode, 'GB');
      expect(_labels(schedule), ['In cinemas']);
    });

    test('a movie whose payload contains only a Premiere anywhere returns '
        'null', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(1, DateTime(2026, 5, 1))]),
          _region('GB', [_entry(1, DateTime(2026, 5, 2))]),
        ],
        now: _now,
      );
      expect(schedule, isNull);
    });

    test('an empty region list returns null', () {
      final schedule = resolveReleaseSchedule([], now: _now);
      expect(schedule, isNull);
    });

    test('with a locale country present in the payload, that region wins '
        'over US', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(3, DateTime(2026, 7, 10))]),
          _region('GB', [_entry(3, DateTime(2026, 7, 3))]),
        ],
        preferredRegion: 'GB',
        now: _now,
      );
      expect(schedule, isNotNull);
      expect(schedule!.regionCode, 'GB');
    });

    test('with a locale country absent from the payload, US wins', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(3, DateTime(2026, 7, 10))]),
          _region('FR', [_entry(3, DateTime(2026, 7, 3))]),
        ],
        preferredRegion: 'DE',
        now: _now,
      );
      expect(schedule, isNotNull);
      expect(schedule!.regionCode, 'US');
    });

    test('with neither the locale country nor US present, the first region '
        'carrying a milestone wins, and the resolved region code is reported',
        () {
      final schedule = resolveReleaseSchedule(
        [
          _region('FR', [_entry(3, DateTime(2026, 7, 3))]),
          _region('DE', [_entry(3, DateTime(2026, 7, 5))]),
        ],
        preferredRegion: 'JP',
        now: _now,
      );
      expect(schedule, isNotNull);
      expect(schedule!.regionCode, 'FR');
    });

    test('a milestone dated before now reports isUpcoming == false', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(3, DateTime(2026, 1, 1))]),
        ],
        now: _now,
      );
      expect(schedule!.milestones.single.isUpcoming, isFalse);
    });

    test('a milestone dated after now reports isUpcoming == true', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(3, DateTime(2026, 12, 1))]),
        ],
        now: _now,
      );
      expect(schedule!.milestones.single.isUpcoming, isTrue);
    });

    test('a milestone dated exactly now reports isUpcoming == true', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [_entry(3, _now)]),
        ],
        now: _now,
      );
      expect(schedule!.milestones.single.isUpcoming, isTrue);
    });

    test('two entries of the same type in one region collapse to the '
        'earlier date', () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [
            _entry(3, DateTime(2026, 8, 1)), // Theatrical (wide)
            _entry(2, DateTime(2026, 7, 1)), // Theatrical (limited)
          ]),
        ],
        now: _now,
      );
      // Both types 2 and 3 map to distinct labels, so both should appear —
      // this exercises "earliest wins per type", not a cross-type merge.
      expect(_labels(schedule), ['In cinemas (limited)', 'In cinemas']);
    });

    test('two entries of the same exact type collapse to the earlier date',
        () {
      final schedule = resolveReleaseSchedule(
        [
          _region('US', [
            _entry(4, DateTime(2026, 9, 15)),
            _entry(4, DateTime(2026, 9, 1)),
          ]),
        ],
        now: _now,
      );
      expect(schedule!.milestones, hasLength(1));
      expect(schedule.milestones.single.date, DateTime(2026, 9, 1));
    });

    test('a 2024-06-14T00:00:00.000Z string parses to June 14 2024 and does '
        'not shift a day', () {
      final region = TmdbReleaseDateRegion.fromJson({
        'iso_3166_1': 'US',
        'release_dates': [
          {'type': 3, 'release_date': '2024-06-14T00:00:00.000Z'},
        ],
      });
      final schedule = resolveReleaseSchedule([region], now: _now);
      expect(schedule!.milestones.single.date, DateTime(2024, 6, 14));
    });
  });

  group('formatReleaseDate', () {
    test('renders MMM d, yyyy including for a date in the current year', () {
      expect(formatReleaseDate(DateTime(2026, 7, 3)), 'Jul 3, 2026');
    });

    test('renders MMM d, yyyy for a date far in the past', () {
      expect(formatReleaseDate(DateTime(1994, 9, 23)), 'Sep 23, 1994');
    });
  });
}
