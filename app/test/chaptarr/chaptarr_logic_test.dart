import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/import_doctor.dart';
import 'package:flutter_test/flutter_test.dart';

ChaptarrQueueItem _item({
  String status = 'ok',
  String state = 'downloading',
  String? error,
  List<ChaptarrStatusMessage> messages = const [],
}) =>
    ChaptarrQueueItem(
      id: 1,
      title: 'release',
      status: 'downloading',
      trackedDownloadStatus: status,
      trackedDownloadState: state,
      errorMessage: error,
      statusMessageGroups: messages,
    );

ChaptarrStatusMessage _msg(String text) =>
    ChaptarrStatusMessage(title: 'release', messages: [text]);

void main() {
  group('bookFormatFromQuality', () {
    test('ebook formats classify as ebook', () {
      for (final q in ['EPUB', 'epub', 'MOBI', 'AZW3', 'PDF', 'CBZ', 'KEPUB']) {
        expect(bookFormatFromQuality(q), BookFormat.ebook, reason: q);
      }
    });

    test('audiobook formats classify as audiobook', () {
      for (final q in [
        'MP3-320',
        'M4B',
        'M4A',
        'FLAC',
        'AAC',
        'OGG',
        'Audiobook',
      ]) {
        expect(bookFormatFromQuality(q), BookFormat.audiobook, reason: q);
      }
    });

    test('null/empty/unrecognized classify as unknown', () {
      expect(bookFormatFromQuality(null), BookFormat.unknown);
      expect(bookFormatFromQuality(''), BookFormat.unknown);
      expect(bookFormatFromQuality('SomethingElse'), BookFormat.unknown);
    });
  });

  group('ChaptarrBook.fromJson', () {
    test('parses lookup results with string genres', () {
      final book = ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': '40748777',
        'genres': '',
        'author': {
          'authorName': 'Kate Stewart',
          'foreignAuthorId': 'gr:7390938',
        },
        'images': [
          {
            'url': '/MediaCoverProxy/cover.jpg',
            'coverType': 'cover',
          }
        ],
        'editions': [
          {
            'title': 'Flock (The Ravenhood, #1)',
            'isEbook': false,
            'pageCount': 354,
            'images': [],
          }
        ],
      });

      expect(book.title, 'Flock');
      expect(book.foreignBookId, '40748777');
      expect(book.genres, isEmpty);
      expect(book.author?.authorName, 'Kate Stewart');
      expect(book.editions, hasLength(1));
      expect(book.formats, contains(BookFormat.audiobook));
    });

    test('splits comma-separated genres defensively', () {
      final book = ChaptarrBook.fromJson({
        'title': 'Genre Book',
        'genres': 'Romance, Mystery',
      });

      expect(book.genres, ['Romance', 'Mystery']);
    });
  });

  // diagnoseChaptarrQueueItem bridges ChaptarrStatusMessage -> the shared neutral
  // engine; these confirm the bridge feeds the catalog correctly for books.
  group('diagnoseChaptarrQueueItem', () {
    test('healthy download is OK with no actions', () {
      final d = diagnoseChaptarrQueueItem(_item());
      expect(d.severity, DoctorSeverity.ok);
      expect(d.isHealthy, isTrue);
    });

    test('stalled torrent error → blocklist+search', () {
      final d = diagnoseChaptarrQueueItem(_item(
        status: 'error',
        error: 'The download is stalled with no connections',
      ));
      expect(d.severity, DoctorSeverity.error);
      expect(d.actions, contains(DoctorAction.blocklistSearch));
    });

    test('sample rejection (via status message bridge) → force import', () {
      final d = diagnoseChaptarrQueueItem(_item(
        status: 'warning',
        state: 'importBlocked',
        messages: [_msg('Sample')],
      ));
      expect(d.severity, DoctorSeverity.warning);
      expect(d.actions, contains(DoctorAction.forceImport));
    });

    test('stuck importPending with no messages → process', () {
      final d = diagnoseChaptarrQueueItem(_item(state: 'importPending'));
      expect(d.problem, 'Waiting to import');
      expect(d.actions, contains(DoctorAction.process));
    });
  });
}
