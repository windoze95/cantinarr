import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/season_availability.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The season card's fraction is Sonarr's own files/episodeCount — monitored
/// and aired plus anything downloaded — so unaired and unmonitored episodes
/// are not graded against the season. They still may not vanish: the suffix
/// accounts for the whole season, so a merely caught-up season reads
/// "11/11 … • 2 unaired" rather than a bare "11/11" one tap away from a list
/// of 13 episodes.
SonarrStatistics _stats({
  int files = 0,
  int obtainable = 0,
  int total = 0,
  DateTime? nextAiring,
}) =>
    SonarrStatistics(
      episodeFileCount: files,
      episodeCount: obtainable,
      totalEpisodeCount: total,
      nextAiring: nextAiring,
    );

/// One queue row per episode, the way Sonarr expands a grab.
List<SonarrQueueItem> _queue(
  int count, {
  required bool aired,
  String state = 'downloading',
  int from = 0,
}) =>
    [
      for (var i = from; i < from + count; i++)
        SonarrQueueItem(
          id: 900 + i,
          episodeId: 100 + i,
          title: 'Release $i',
          trackedDownloadState: state,
          episodeAirDateUtc: aired
              ? DateTime.utc(2003, 8, 7)
              : DateTime.now().toUtc().add(const Duration(days: 30)),
        ),
    ];

/// The line as it is read: no-break spaces collapsed back to plain ones.
String _read(String text) => text.replaceAll(' ', ' ');

/// Lays the line out at [width] and returns what lands on each visual line.
List<String> _wrapAt(String text, double width) {
  final painter = TextPainter(
    text: TextSpan(text: text, style: const TextStyle(fontSize: 13)),
    textDirection: TextDirection.ltr,
  )..layout(maxWidth: width);

  final lines = <String>[];
  var offset = 0;
  while (offset < text.length) {
    final range = painter.getLineBoundary(TextPosition(offset: offset));
    if (range.end <= offset) break;
    lines.add(_read(text.substring(range.start, range.end)).trim());
    offset = range.end;
  }
  return lines;
}

void main() {
  group('seasonAvailabilityLine', () {
    test('a caught-up airing season counts the episodes still to come', () {
      final line = seasonAvailabilityLine(
        _stats(files: 11, obtainable: 11, total: 13),
        moreToCome: true,
      );

      expect(_read(line.text), '11/11 Episodes Available • 2 unaired');
      expect(line.text, isNot(contains('100%')));
      // Caught up, but not complete: green is reserved for a full season.
      expect(line.color, AppTheme.downloading);
    });

    test('a full season reads complete and green, with no suffix', () {
      final line = seasonAvailabilityLine(
        _stats(files: 13, obtainable: 13, total: 13),
        moreToCome: false,
      );

      expect(_read(line.text), '13/13 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('an aired gap is named separately from the unaired remainder', () {
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 11, total: 13),
        moreToCome: true,
      );

      expect(_read(line.text), '9/11 Episodes Available • 2 missing, 2 unaired');
      expect(line.color, AppTheme.error);
    });

    test('with nothing left to air the remainder is unmonitored, not unaired',
        () {
      // An unmonitored season: Sonarr counts only the episodes on disk, so the
      // rest are a choice the admin made rather than a gap to hunt.
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 0, total: 13),
        moreToCome: false,
      );

      expect(_read(line.text), '0/0 Episodes Available • 13 unmonitored');
      expect(line.color, AppTheme.requested);
    });

    test('a series whose unmonitored seasons dwarf the downloaded one', () {
      // Series-level statistics for the episode_totals_test scenario: one
      // downloaded season, three unmonitored ones. The fraction reads complete
      // on purpose — the suffix carries the other 25, and the colour stays
      // amber rather than green.
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 9, total: 34),
        moreToCome: false,
      );

      expect(_read(line.text), '9/9 Episodes Available • 25 unmonitored');
      expect(line.color, AppTheme.requested);
    });

    test('falls back to the obtainable count when totalEpisodeCount is absent',
        () {
      final line = seasonAvailabilityLine(
        _stats(files: 8, obtainable: 8),
        moreToCome: false,
      );

      expect(_read(line.text), '8/8 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('falls back to the file count when episodeCount is absent', () {
      final line = seasonAvailabilityLine(
        _stats(files: 8),
        moreToCome: false,
      );

      expect(_read(line.text), '8/8 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('an empty or statistics-less season stays neutral', () {
      final line = seasonAvailabilityLine(null, moreToCome: false);

      expect(_read(line.text), '0/0 Episodes Available');
      expect(line.color, AppTheme.textSecondary);
    });
  });

  group('seasonAvailabilityLine with a queue', () {
    test('a season parked in front of a broken import is not "missing"', () {
      // Every episode downloaded and waiting to import: calling these missing
      // points the admin at the indexer when the problem is the import step.
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 13),
        moreToCome: false,
        queue: _queue(13, aired: true, state: 'importPending'),
      );

      expect(_read(line.text), '0/13 Episodes Available • 13 waiting to import');
      expect(line.color, AppTheme.downloading);
    });

    test('an import Sonarr has blocked still counts as waiting', () {
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 13),
        moreToCome: false,
        queue: _queue(13, aired: true, state: 'importBlocked'),
      );

      expect(_read(line.text), '0/13 Episodes Available • 13 waiting to import');
    });

    test('episodes still transferring read as downloading', () {
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 13, total: 13),
        moreToCome: false,
        queue: _queue(2, aired: true),
      );

      expect(_read(line.text),
          '9/13 Episodes Available • 2 missing, 2 downloading');
      // Two real holes nothing is working on: still red.
      expect(line.color, AppTheme.error);
    });

    test('a mix names both states rather than hiding the parked ones', () {
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 13),
        moreToCome: false,
        queue: [
          ..._queue(1, aired: true),
          ..._queue(3, aired: true, state: 'importPending', from: 5),
        ],
      );

      expect(
        _read(line.text),
        '0/13 Episodes Available • 9 missing, 1 downloading, '
        '3 waiting to import',
      );
    });

    test('an in-flight unaired episode is counted once, not twice', () {
      // The American Dad! case: both remaining episodes are unaired *and*
      // already downloaded, so "2 unaired, 2 waiting to import" would be one
      // pair of episodes counted twice.
      final line = seasonAvailabilityLine(
        _stats(files: 11, obtainable: 11, total: 13),
        moreToCome: true,
        queue: _queue(2, aired: false, state: 'importPending'),
      );

      expect(_read(line.text), '11/11 Episodes Available • 2 waiting to import');
      expect(line.color, AppTheme.downloading);
    });

    test('every bucket is named when the season is mid-flight', () {
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 11, total: 13),
        moreToCome: true,
        queue: [..._queue(1, aired: true), ..._queue(1, aired: false, from: 5)],
      );

      // 9 on disk + 1 missing + 2 downloading + 1 unaired = 13.
      expect(_read(line.text),
          '9/11 Episodes Available • 1 missing, 2 downloading, 1 unaired');
    });

    test('a queued upgrade for a season already on disk stays complete', () {
      final line = seasonAvailabilityLine(
        _stats(files: 13, obtainable: 13, total: 13),
        moreToCome: false,
        queue: _queue(1, aired: true),
      );

      expect(_read(line.text), '13/13 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('one grab per episode, even when Sonarr lists a season pack twice',
        () {
      final duplicated = [..._queue(2, aired: true), ..._queue(2, aired: true)];

      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 13),
        moreToCome: false,
        queue: duplicated,
      );

      expect(_read(line.text),
          '0/13 Episodes Available • 11 missing, 2 downloading');
    });

    test('a re-grab that is moving again outranks its import-pending row', () {
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 13),
        moreToCome: false,
        queue: [
          ..._queue(1, aired: true, state: 'importPending'),
          ..._queue(1, aired: true),
        ],
      );

      expect(_read(line.text),
          '0/13 Episodes Available • 12 missing, 1 downloading');
    });
  });

  group('line breaking', () {
    test('a count never leaves its own words behind', () {
      // The reported shape: All Seasons for a series with a stuck season and
      // an unmonitored Specials, which wrapped "…, 7" / "unmonitored".
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 13, total: 20),
        moreToCome: false,
        queue: _queue(13, aired: true, state: 'importPending'),
      );

      for (final width in [120.0, 180.0, 240.0, 300.0]) {
        final lines = _wrapAt(line.text, width);
        expect(lines.length, greaterThan(1), reason: 'width $width');
        for (final visual in lines) {
          expect(RegExp(r'\d,?$').hasMatch(visual), isFalse,
              reason: 'a bare count ended a line at width $width: $lines');
        }
        expect(lines.any((l) => l.startsWith('waiting')), isFalse,
            reason: 'at width $width: $lines');
        expect(lines.any((l) => l.startsWith('unmonitored')), isFalse,
            reason: 'at width $width: $lines');
      }
    });

    test('phrases are stitched with no-break spaces, and only phrases are', () {
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 13, total: 13),
        moreToCome: false,
        queue: _queue(2, aired: true, state: 'importPending'),
      );

      // No plain space is ever allowed between a digit and the word it counts.
      expect(RegExp(r'\d [A-Za-z]').hasMatch(line.text), isFalse);
      // "9/13 Episodes Available" | "• 2 missing," | "2 waiting to import"
      expect(line.text.split(' ').length, 3);
    });
  });

  group('SonarrQueueItem.episodeHasAired', () {
    test('needs an air date that has passed', () {
      expect(_queue(1, aired: true).single.episodeHasAired, isTrue);
      expect(_queue(1, aired: false).single.episodeHasAired, isFalse);
      expect(
        const SonarrQueueItem(id: 1, title: 'No date').episodeHasAired,
        isFalse,
      );
    });

    test('reads the air date off the embedded episode', () {
      final item = SonarrQueueItem.fromJson({
        'id': 5,
        'title': 'Release',
        'episode': {
          'id': 42,
          'seasonNumber': 1,
          'episodeNumber': 3,
          'airDateUtc': '2003-08-07T02:00:00Z',
        },
      });

      expect(item.episodeAirDateUtc, DateTime.utc(2003, 8, 7, 2));
      expect(item.episodeHasAired, isTrue);
    });
  });

  group('SonarrSeries.hasUpcomingEpisodes', () {
    SonarrSeason season(int number, {DateTime? nextAiring}) => SonarrSeason(
          seasonNumber: number,
          statistics: _stats(
              files: 11, obtainable: 11, total: 13, nextAiring: nextAiring),
        );

    test('true when any season is still waiting on an episode', () {
      final series = SonarrSeries(
        id: 1,
        title: 'American Dad!',
        seasons: [season(21), season(22, nextAiring: DateTime.utc(2026, 9, 13))],
      );

      expect(series.hasUpcomingEpisodes, isTrue);
    });

    test('false once nothing is left to air', () {
      final series = SonarrSeries(
        id: 2,
        title: 'Ended',
        seasons: [season(1), season(2)],
      );

      expect(series.hasUpcomingEpisodes, isFalse);
    });
  });

  group('SonarrStatistics.fromJson', () {
    test('reads nextAiring, and leaves it null when Sonarr omits it', () {
      final airing = SonarrStatistics.fromJson({
        'episodeFileCount': 11,
        'episodeCount': 11,
        'totalEpisodeCount': 13,
        'nextAiring': '2026-09-13T04:00:00Z',
      });
      final done = SonarrStatistics.fromJson({'episodeFileCount': 11});

      expect(airing.nextAiring, DateTime.utc(2026, 9, 13, 4));
      expect(airing.totalEpisodeCount, 13);
      expect(done.nextAiring, isNull);
    });
  });
}
