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
      expect(bookFormatFromQuality('Kindle Edition'), BookFormat.ebook);
      expect(bookFormatFromQuality('eBook'), BookFormat.ebook);
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
        'Audible Audio',
        'Audio CD',
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

    test('leaves unclassified editions unknown when isEbook is omitted', () {
      final book = ChaptarrBook.fromJson({
        'title': 'Physical Book',
        'editions': [
          {'title': 'Hardcover', 'format': 'Hardcover'}
        ],
      });

      expect(book.formats, isEmpty);
    });

    test('format derives from mediaType', () {
      ChaptarrBook book(String? mediaType) =>
          ChaptarrBook.fromJson({'title': 'T', 'mediaType': mediaType});
      expect(book('ebook').format, BookFormat.ebook);
      expect(book('audiobook').format, BookFormat.audiobook);
      expect(book(null).format, BookFormat.unknown);
    });

    test('groupKey groups records by foreignBookId, else falls back to id', () {
      final ebook = ChaptarrBook.fromJson(
          {'id': 1, 'title': 'T', 'foreignBookId': 'fb-1', 'mediaType': 'ebook'});
      final audio = ChaptarrBook.fromJson({
        'id': 2,
        'title': 'T',
        'foreignBookId': 'fb-1',
        'mediaType': 'audiobook'
      });
      final noForeign = ChaptarrBook.fromJson({'id': 3, 'title': 'T'});
      // The two formats of one title share a key; a foreignId-less record gets
      // its own (id-based) key so unrelated books never merge.
      expect(ebook.groupKey, audio.groupKey);
      expect(noForeign.groupKey, 'id:3');
      expect(noForeign.groupKey, isNot(ebook.groupKey));
    });
  });

  group('format context outside the library', () {
    test('queue items prefer the embedded book mediaType', () {
      final item = ChaptarrQueueItem.fromJson({
        'id': 1,
        'title': 'Ambiguous release title',
        'quality': {
          'quality': {'name': 'EPUB'}
        },
        'book': {'id': 3, 'title': 'Flock', 'mediaType': 'audiobook'},
      });

      expect(item.format, BookFormat.audiobook);
    });

    test('wanted and history records expose format when supplied', () {
      final wanted = ChaptarrWantedRecord.fromJson({
        'id': 3,
        'title': 'Flock',
        'mediaType': 'audiobook',
      });
      final history = ChaptarrHistoryRecord.fromJson({
        'id': 4,
        'sourceTitle': 'Flock',
        'quality': {
          'quality': {'name': 'EPUB'}
        },
      });

      expect(wanted.format, BookFormat.audiobook);
      expect(history.format, BookFormat.ebook);
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
