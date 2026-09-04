import 'package:cantinarr/features/dashboard/data/release_event.dart';
import 'package:cantinarr/features/lidarr/data/lidarr_calendar.dart';
import 'package:cantinarr/features/lidarr/data/lidarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

LidarrAlbum _album({
  int id = 9,
  String title = 'Fear Inoculum',
  String? foreignId = '1f4a9e6b',
  String? releaseDate = '2026-09-05T00:00:00Z',
  String artist = 'Tool',
  List<Map<String, dynamic>> images = const [],
  String? remoteCover,
  int trackFiles = 0,
}) =>
    LidarrAlbum.fromJson({
      'id': id,
      'title': title,
      'artistId': 4,
      if (foreignId != null) 'foreignAlbumId': foreignId,
      if (releaseDate != null) 'releaseDate': releaseDate,
      'artist': {'id': 4, 'artistName': artist},
      'images': images,
      if (remoteCover != null) 'remoteCover': remoteCover,
      'statistics': {'trackFileCount': trackFiles, 'trackCount': 10},
    });

void main() {
  group('lidarrCalendarReleases', () {
    test('dates are read by components, never zone-shifted', () {
      // A midnight-UTC calendar date must stay on its own day even when the
      // viewer sits west of UTC — converting zones would shift it to the 4th.
      final rows = lidarrCalendarReleases(
        [_album(releaseDate: '2026-09-05T00:00:00Z')],
        start: DateTime(2026, 9, 1),
        end: DateTime(2026, 9, 30),
      );
      expect(rows, hasLength(1));
      expect(rows.first.date, DateTime(2026, 9, 5));
    });

    test('sorts ascending and skips only dateless albums', () {
      final rows = lidarrCalendarReleases(
        [
          _album(id: 2, releaseDate: '2026-09-20T00:00:00Z'),
          _album(id: 1, releaseDate: '2026-09-05T00:00:00Z'),
          _album(id: 3, releaseDate: null),
        ],
        start: DateTime(2026, 9, 1),
        end: DateTime(2026, 9, 30),
      );
      expect(rows.map((r) => r.album.id), [1, 2]);
    });

    test('an out-of-window date still yields its row', () {
      // Nothing Lidarr returned is silently dropped: window edges are the
      // arr's business, not a second filter here.
      final rows = lidarrCalendarReleases(
        [_album(releaseDate: '2026-10-15T00:00:00Z')],
        start: DateTime(2026, 9, 1),
        end: DateTime(2026, 9, 30),
      );
      expect(rows, hasLength(1));
    });
  });

  group('releaseEventFromLidarr', () {
    test('builds a calendar-date music event with the album identity', () {
      final event =
          releaseEventFromLidarr(_album(trackFiles: 10), instanceId: 'music-1');
      expect(event, isNotNull);
      expect(event!.mediaType, ReleaseMediaType.music);
      expect(event.date, DateTime(2026, 9, 5));
      expect(event.title, 'Fear Inoculum');
      expect(event.subtitle, 'Tool');
      expect(event.foreignId, '1f4a9e6b');
      expect(event.instanceId, 'music-1');
      expect(event.hasFile, isTrue);
    });

    test('needs a release date and an addressable MusicBrainz id', () {
      expect(releaseEventFromLidarr(_album(releaseDate: null),
              instanceId: 'music-1'),
          isNull);
      expect(
          releaseEventFromLidarr(_album(foreignId: null), instanceId: 'music-1'),
          isNull);
    });

    test('poster is the external cover only — arr-relative paths are dropped',
        () {
      final external = releaseEventFromLidarr(
        _album(images: const [
          {'coverType': 'cover', 'url': '/MediaCover/Albums/9/cover.jpg'},
          {
            'coverType': 'cover',
            'url': '/MediaCover/Albums/9/cover.jpg',
            'remoteUrl': 'https://covers.example.org/9.jpg',
          },
        ]),
        instanceId: 'music-1',
      );
      expect(external!.posterUrl, 'https://covers.example.org/9.jpg');

      final relativeOnly = releaseEventFromLidarr(
        _album(
          images: const [
            {'coverType': 'cover', 'url': '/MediaCover/Albums/9/cover.jpg'},
          ],
          remoteCover: '/MediaCover/Albums/9/cover.jpg',
        ),
        instanceId: 'music-1',
      );
      expect(relativeOnly!.posterUrl, isNull,
          reason: 'an instance-origin path must never reach the image loader');
    });
  });
}
