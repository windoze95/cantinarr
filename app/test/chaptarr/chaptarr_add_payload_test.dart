import 'package:cantinarr/features/chaptarr/data/chaptarr_add_payload.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('chaptarrAddFormatBody', () {
    test('audiobook pins mediaType + flags and the author block', () {
      final body = chaptarrAddFormatBody(
        foreignBookId: 'fb-1',
        title: 'Pretty Furious',
        titleSlug: 'pretty-furious',
        format: BookFormat.audiobook,
        authorName: 'E.K. Johnston',
        foreignAuthorId: 'hc:240647',
        qualityProfileId: 1,
        metadataProfileId: 2,
        rootFolderPath: '/media-server/media/audiobooks',
      );

      expect(body['foreignBookId'], 'fb-1');
      expect(body['mediaType'], 'audiobook');
      expect(body['audiobookMonitored'], isTrue);
      expect(body['ebookMonitored'], isFalse);
      expect(body['monitored'], isTrue);
      final author = body['author'] as Map<String, dynamic>;
      expect(author['foreignAuthorId'], 'hc:240647');
      expect(author['qualityProfileId'], 1);
      expect(author['rootFolderPath'], '/media-server/media/audiobooks');
      expect(author['addOptions'], {'monitor': 'all', 'searchForMissingBooks': false});
      expect(body['addOptions'], {'searchForNewBook': true});
    });

    test('ebook pins the opposite flags', () {
      final body = chaptarrAddFormatBody(
        foreignBookId: 'fb-2',
        title: 'T',
        format: BookFormat.ebook,
        authorName: 'A',
        qualityProfileId: 1,
        metadataProfileId: 1,
        rootFolderPath: '/x',
      );
      expect(body['mediaType'], 'ebook');
      expect(body['ebookMonitored'], isTrue);
      expect(body['audiobookMonitored'], isFalse);
    });
  });

  group('chaptarrRootFolderFor', () {
    final folders = [
      const ChaptarrRootFolder(id: 1, path: '/media-server/media/ebooks'),
      const ChaptarrRootFolder(id: 2, path: '/media-server/media/audiobooks'),
    ];

    test('routes each format to its matching folder', () {
      expect(chaptarrRootFolderFor(BookFormat.audiobook, folders),
          '/media-server/media/audiobooks');
      expect(chaptarrRootFolderFor(BookFormat.ebook, folders),
          '/media-server/media/ebooks');
    });

    test('falls back to the first folder when nothing matches', () {
      final only = [const ChaptarrRootFolder(id: 1, path: '/books')];
      expect(chaptarrRootFolderFor(BookFormat.audiobook, only), '/books');
    });
  });
}
