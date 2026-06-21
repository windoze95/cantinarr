import 'package:cantinarr/features/dashboard/data/release_event.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('releaseEventFromRadarr', () {
    test('picks the soonest upcoming release and labels it', () {
      final event = releaseEventFromRadarr(
        {
          'title': 'Dune',
          'tmdbId': 438631,
          'hasFile': false,
          'inCinemas': '2021-10-22T00:00:00Z',
          'digitalRelease': '2099-01-15T00:00:00Z',
          'images': [
            {'coverType': 'poster', 'remoteUrl': 'http://x/p.jpg'},
          ],
        },
        now: DateTime(2099, 1, 1),
      );

      expect(event, isNotNull);
      expect(event!.title, 'Dune');
      expect(event.subtitle, 'Digital');
      expect(event.mediaType, ReleaseMediaType.movie);
      expect(event.tmdbId, 438631);
      expect(event.day, DateTime(2099, 1, 15));
      expect(event.posterUrl, 'http://x/p.jpg');
      expect(event.hasFile, isFalse);
    });

    test('falls back to the most recent date when all are in the past', () {
      final event = releaseEventFromRadarr(
        {
          'title': 'Old Movie',
          'inCinemas': '2000-01-01T00:00:00Z',
          'physicalRelease': '2000-03-01T00:00:00Z',
        },
        now: DateTime(2099, 1, 1),
      );

      expect(event, isNotNull);
      expect(event!.subtitle, 'Physical');
      expect(event.day, DateTime(2000, 3, 1));
    });

    test('returns null when there is no usable release date', () {
      expect(
        releaseEventFromRadarr({'title': 'No Date'}, now: DateTime(2099, 1, 1)),
        isNull,
      );
    });
  });

  group('releaseEventFromSonarr', () {
    const series = SonarrSeries(
      id: 5,
      title: 'Show',
      tmdbId: 99,
      images: [SonarrImage(coverType: 'poster', remoteUrl: 'http://x/s.jpg')],
    );

    test('joins the series and formats the episode subtitle', () {
      final event = releaseEventFromSonarr(
        {
          'seriesId': 5,
          'seasonNumber': 2,
          'episodeNumber': 7,
          'title': 'The Ep',
          'airDateUtc': '2099-06-20T01:00:00Z',
          'hasFile': false,
        },
        {5: series},
      );

      expect(event, isNotNull);
      expect(event!.title, 'Show');
      expect(event.subtitle, 'S02E07 • The Ep');
      expect(event.mediaType, ReleaseMediaType.tv);
      expect(event.tmdbId, 99);
      expect(event.posterUrl, 'http://x/s.jpg');
      expect(event.hasFile, isFalse);
    });

    test('falls back to the nested series title when not in the map', () {
      final event = releaseEventFromSonarr(
        {
          'seriesId': 1,
          'seasonNumber': 1,
          'episodeNumber': 1,
          'airDateUtc': '2099-01-01T00:00:00Z',
          'series': {'title': 'Nested'},
        },
        {},
      );

      expect(event, isNotNull);
      expect(event!.title, 'Nested');
      expect(event.subtitle, 'S01E01');
      expect(event.posterUrl, isNull);
      expect(event.tmdbId, isNull);
    });

    test('returns null when there is no air date', () {
      expect(releaseEventFromSonarr({'seriesId': 1}, {}), isNull);
    });
  });

  group('groupReleasesByDay', () {
    test('groups by day, ordering days and intra-day events by time', () {
      final a = ReleaseEvent(
        date: DateTime(2099, 1, 2, 9),
        title: 'A',
        mediaType: ReleaseMediaType.movie,
      );
      final b = ReleaseEvent(
        date: DateTime(2099, 1, 1, 20),
        title: 'B',
        mediaType: ReleaseMediaType.tv,
      );
      final c = ReleaseEvent(
        date: DateTime(2099, 1, 1, 8),
        title: 'C',
        mediaType: ReleaseMediaType.movie,
      );

      final groups = groupReleasesByDay([a, b, c]);

      expect(groups.length, 2);
      expect(groups[0].key, DateTime(2099, 1, 1));
      expect(groups[0].value.map((e) => e.title).toList(), ['C', 'B']);
      expect(groups[1].key, DateTime(2099, 1, 2));
      expect(groups[1].value.single.title, 'A');
    });

    test('returns an empty list for no events', () {
      expect(groupReleasesByDay([]), isEmpty);
    });
  });
}
