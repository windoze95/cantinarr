import 'package:cantinarr/features/dashboard/logic/library_rows.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

RadarrMovie _movie({
  required int id,
  required bool hasFile,
  DateTime? added,
  DateTime? fileDateAdded,
  bool withFile = true,
}) =>
    RadarrMovie(
      id: id,
      title: 'Movie $id',
      year: 2020,
      tmdbId: id,
      hasFile: hasFile,
      monitored: true,
      added: added,
      movieFile: hasFile && withFile
          ? RadarrMovieFile(id: id, dateAdded: fileDateAdded)
          : null,
    );

SonarrSeries _series({
  required int id,
  int episodeFileCount = 0,
  int episodeCount = 0,
}) =>
    SonarrSeries(
      id: id,
      title: 'Series $id',
      tmdbId: id,
      statistics: SonarrStatistics(
        episodeFileCount: episodeFileCount,
        episodeCount: episodeCount,
      ),
    );

SonarrHistoryRecord _event({
  required int? seriesId,
  required DateTime? date,
  String eventType = SonarrHistoryRecord.importedEventType,
}) =>
    SonarrHistoryRecord(
      id: 0,
      seriesId: seriesId,
      date: date,
      eventType: eventType,
    );

Map<String, dynamic> _airing(int? seriesId, String? airDateUtc) => {
      'seriesId': seriesId,
      'airDateUtc': airDateUtc,
    };

void main() {
  final now = DateTime(2026, 7, 25);

  test('orders by when the file landed, not when the movie was added', () {
    // The classic case: requested long ago, downloaded today. Ordering by the
    // movie record's `added` buries it under the back catalogue on exactly the
    // day the user wants to see it.
    final requestedLongAgo = _movie(
      id: 1,
      hasFile: true,
      added: DateTime(2020, 1, 1),
      fileDateAdded: now,
    );
    final addedRecently = _movie(
      id: 2,
      hasFile: true,
      added: now.subtract(const Duration(days: 7)),
      fileDateAdded: now.subtract(const Duration(days: 7)),
    );

    final row = recentlyDownloadedMovies([addedRecently, requestedLongAgo]);

    expect(row.map((m) => m.id), [1, 2]);
  });

  test('excludes movies with no file', () {
    final row = recentlyDownloadedMovies([
      _movie(id: 1, hasFile: false, added: now),
      _movie(id: 2, hasFile: true, fileDateAdded: now),
    ]);

    expect(row.map((m) => m.id), [2]);
  });

  test('keeps a downloaded movie whose file record was omitted', () {
    // The movie list endpoint sometimes omits movieFile even when hasFile is
    // true; keying off the file alone would delete these from the row.
    final noFileRecord = _movie(
      id: 1,
      hasFile: true,
      added: now,
      withFile: false,
    );
    final older = _movie(
      id: 2,
      hasFile: true,
      fileDateAdded: now.subtract(const Duration(days: 30)),
    );

    final row = recentlyDownloadedMovies([older, noFileRecord]);

    expect(row.map((m) => m.id), [1, 2]);
  });

  test('caps the row length', () {
    final movies = List.generate(
      25,
      (i) => _movie(
        id: i,
        hasFile: true,
        fileDateAdded: now.subtract(Duration(days: i)),
      ),
    );

    expect(recentlyDownloadedMovies(movies).length, 10);
    expect(recentlyDownloadedMovies(movies, limit: 3).map((m) => m.id),
        [0, 1, 2]);
  });

  test('decodes the file import timestamp from Radarr', () {
    final file = RadarrMovieFile.fromJson({
      'id': 5,
      'dateAdded': '2026-07-20T10:30:00Z',
    });

    expect(file.dateAdded, DateTime.parse('2026-07-20T10:30:00Z'));
  });

  group('recentlyDownloadedSeries', () {
    test('orders by when episodes imported, not by how complete a series is',
        () {
      // The bug this replaced: sorting by percentComplete ranked the *most
      // complete* series, so a finished show whose last episode landed a year
      // ago sat permanently above the show that downloaded an hour ago.
      final finishedLongAgo =
          _series(id: 1, episodeFileCount: 100, episodeCount: 100);
      final barelyStarted =
          _series(id: 2, episodeFileCount: 1, episodeCount: 20);

      final row = recentlyDownloadedSeries([finishedLongAgo, barelyStarted], [
        _event(seriesId: 2, date: now),
        _event(seriesId: 1, date: DateTime(2025, 7, 25)),
      ]);

      expect(row.map((s) => s.id), [2, 1]);
    });

    test('dates a season pack by its newest episode, and shows it once', () {
      // Sonarr writes one import record per episode, newest first. A twelve
      // episode pack is one arrival of one show, and it has to take the newest
      // of those records or the series sorts below its own older episodes.
      final pack = [
        for (var i = 0; i < 12; i++)
          _event(seriesId: 1, date: now.subtract(Duration(minutes: i))),
      ];

      final row = recentlyDownloadedSeries([
        _series(id: 1),
        _series(id: 2),
      ], [
        ...pack,
        _event(seriesId: 2, date: now.subtract(const Duration(minutes: 5))),
      ]);

      expect(row.map((s) => s.id), [1, 2]);
    });

    test('counts only imports, not grabs or deletions', () {
      final row = recentlyDownloadedSeries([
        _series(id: 1),
        _series(id: 2),
      ], [
        _event(seriesId: 1, date: now, eventType: 'grabbed'),
        _event(seriesId: 1, date: now, eventType: 'episodeFileDeleted'),
        _event(seriesId: 2, date: now.subtract(const Duration(days: 1))),
      ]);

      expect(row.map((s) => s.id), [2]);
    });

    test('leaves out a series with files but no import on record', () {
      // A list ordered by date can only hold records that have one. Appending
      // the undated ones would put "no idea when" in a recency ranking.
      final row = recentlyDownloadedSeries(
        [
          _series(id: 1, episodeFileCount: 50, episodeCount: 50),
          _series(id: 2),
        ],
        [_event(seriesId: 2, date: now)],
      );

      expect(row.map((s) => s.id), [2]);
    });

    test('is empty when Sonarr has no import history', () {
      // Cleared history, or a library older than it: an empty row is the honest
      // answer to "what arrived lately", where the old sort always showed ten.
      final row = recentlyDownloadedSeries(
        [_series(id: 1, episodeFileCount: 9, episodeCount: 9)],
        [],
      );

      expect(row, isEmpty);
    });

    test('ignores history for a series no longer in the library', () {
      final row = recentlyDownloadedSeries(
        [_series(id: 1)],
        [
          _event(seriesId: 99, date: now),
          _event(seriesId: 1, date: now.subtract(const Duration(days: 2))),
        ],
      );

      expect(row.map((s) => s.id), [1]);
    });

    test('skips records missing a series or a date', () {
      final row = recentlyDownloadedSeries(
        [_series(id: 1)],
        [
          _event(seriesId: null, date: now),
          _event(seriesId: 1, date: null),
        ],
      );

      expect(row, isEmpty);
    });

    test('breaks timestamp ties deterministically and caps the row', () {
      // A bulk import stamps every record within the same second; without a
      // tie-break the row would reshuffle between fetches.
      final series = List.generate(25, (i) => _series(id: i));
      final history = [
        for (var i = 0; i < 25; i++) _event(seriesId: i, date: now),
      ];

      final row = recentlyDownloadedSeries(series, history);

      expect(row.map((s) => s.id), [24, 23, 22, 21, 20, 19, 18, 17, 16, 15]);
      expect(
        recentlyDownloadedSeries(series, history, limit: 3).map((s) => s.id),
        [24, 23, 22],
      );
    });
  });

  group('airingNextSeries', () {
    test('orders by soonest air date, not by library order', () {
      // Sonarr returns the library sorted by title, so reusing that order made
      // "Airing Next" alphabetical — a row that never answered its own title.
      final row = airingNextSeries(
        [_series(id: 1), _series(id: 2)],
        [
          _airing(1, '2026-08-01T01:00:00Z'),
          _airing(2, '2026-07-26T01:00:00Z'),
        ],
      );

      expect(row.map((s) => s.id), [2, 1]);
    });

    test('dates a series by its earliest upcoming episode, and shows it once',
        () {
      final row = airingNextSeries(
        [_series(id: 1), _series(id: 2)],
        [
          _airing(1, '2026-07-31T01:00:00Z'),
          _airing(1, '2026-07-26T01:00:00Z'),
          _airing(1, '2026-07-29T01:00:00Z'),
          _airing(2, '2026-07-28T01:00:00Z'),
        ],
      );

      expect(row.map((s) => s.id), [1, 2]);
    });

    test('keeps the series airing soonest when the row is capped', () {
      // The user-visible half of the bug: in library order the cap dropped
      // whatever sorted last, even when it was the next thing to air.
      final series = List.generate(5, (i) => _series(id: i));
      final calendar = [
        _airing(0, '2026-07-28T01:00:00Z'),
        _airing(1, '2026-07-29T01:00:00Z'),
        _airing(2, '2026-07-30T01:00:00Z'),
        _airing(3, '2026-07-31T01:00:00Z'),
        _airing(4, '2026-07-26T01:00:00Z'),
      ];

      expect(
        airingNextSeries(series, calendar, limit: 2).map((s) => s.id),
        [4, 0],
      );
    });

    test('drops entries with no series or an unusable air date', () {
      final row = airingNextSeries(
        [_series(id: 1), _series(id: 2)],
        [
          _airing(null, '2026-07-26T01:00:00Z'),
          _airing(1, 'not a date'),
          _airing(2, '2026-07-27T01:00:00Z'),
        ],
      );

      expect(row.map((s) => s.id), [2]);
    });

    test('leaves out series with nothing on the calendar', () {
      final row = airingNextSeries(
        [_series(id: 1), _series(id: 2)],
        [_airing(2, '2026-07-27T01:00:00Z')],
      );

      expect(row.map((s) => s.id), [2]);
    });
  });
}
