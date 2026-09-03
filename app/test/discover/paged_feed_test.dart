import 'dart:async';

import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/paged_feed.dart';
import 'package:flutter_test/flutter_test.dart';

MediaItem _item(int id) =>
    MediaItem(id: id, title: 'Title $id', mediaType: MediaType.movie);

TmdbPage<MediaItem> _page(int page, List<int> ids, {int totalPages = 10}) =>
    TmdbPage(
      page: page,
      totalPages: totalPages,
      totalResults: 0,
      results: [for (final id in ids) _item(id)],
    );

List<int> _ids(List<MediaItem>? items) => [for (final i in items!) i.id];

void main() {
  group('PagedFeed', () {
    test('hands out each id once across overlapping pages', () async {
      final feed = PagedFeed();
      final requested = <int>[];
      Future<TmdbPage<MediaItem>> fetch(int page) async {
        requested.add(page);
        return _page(page, page == 1 ? [1, 2, 3] : [3, 4]);
      }

      expect(_ids(await feed.nextPage(fetch)), [1, 2, 3]);
      expect(_ids(await feed.nextPage(fetch)), [4]);
      expect(requested, [1, 2]);
    });

    test('drops entries with no usable id', () async {
      final feed = PagedFeed();
      final fresh = await feed.nextPage((page) async => _page(page, [0, 5]));
      expect(_ids(fresh), [5]);
    });

    test('walks past pages that add nothing, up to the limit', () async {
      final feed = PagedFeed();
      final requested = <int>[];
      Future<TmdbPage<MediaItem>> fetch(int page) async {
        requested.add(page);
        return _page(page, page == 1 || page == 4 ? [page] : const []);
      }

      expect(_ids(await feed.nextPage(fetch)), [1]);
      // Pages 2 and 3 arrive empty (the English-only filter kept nothing),
      // page 4 has a title: one call crosses the gap.
      expect(_ids(await feed.nextPage(fetch)), [4]);
      expect(requested, [1, 2, 3, 4]);

      // Three empty pages in a row end the call, not the feed.
      expect(await feed.nextPage(fetch), isEmpty);
      expect(requested, [1, 2, 3, 4, 5, 6, 7]);
      expect(feed.hasMore, isTrue);
    });

    test('stops asking once the feed reports its last page', () async {
      final feed = PagedFeed();
      var fetches = 0;
      Future<TmdbPage<MediaItem>> fetch(int page) async {
        fetches++;
        return _page(page, [page], totalPages: 1);
      }

      expect(_ids(await feed.nextPage(fetch)), [1]);
      expect(feed.hasMore, isFalse);
      expect(await feed.nextPage(fetch), isEmpty);
      expect(fetches, 1);
    });

    test("never asks past TMDB's page cap", () async {
      final feed = PagedFeed();
      var last = 0;
      Future<TmdbPage<MediaItem>> fetch(int page) async {
        last = page;
        return _page(page, [page], totalPages: 1000);
      }

      for (var i = 0; i < PagedFeed.maxPage; i++) {
        expect(await feed.nextPage(fetch), isNotEmpty);
      }
      expect(last, PagedFeed.maxPage);
      expect(feed.hasMore, isFalse);
      expect(await feed.nextPage(fetch), isEmpty);
      expect(last, PagedFeed.maxPage);
    });

    test('a reset during a fetch drops that page and starts over', () async {
      final feed = PagedFeed();
      final stalled = Completer<TmdbPage<MediaItem>>();
      final pending = feed.nextPage((_) => stalled.future);

      feed.reset();
      stalled.complete(_page(1, [1, 2]));
      expect(await pending, isNull);

      final requested = <int>[];
      final fresh = await feed.nextPage((page) async {
        requested.add(page);
        return _page(page, [1, 2]);
      });
      // The stale page never marked its ids as seen.
      expect(_ids(fresh), [1, 2]);
      expect(requested, [1]);
    });

    test('a failed fetch retries the same page next time', () async {
      final feed = PagedFeed();
      final requested = <int>[];
      var fail = true;
      Future<TmdbPage<MediaItem>> fetch(int page) async {
        requested.add(page);
        if (fail) throw StateError('offline');
        return _page(page, [page]);
      }

      expect(await feed.nextPage(fetch), isEmpty);
      expect(feed.isLoading, isFalse);
      fail = false;
      expect(_ids(await feed.nextPage(fetch)), [1]);
      expect(requested, [1, 1]);
    });
  });

  group('openEndedPage', () {
    test('keeps going while pages come back with titles', () {
      expect(openEndedPage(3, [_item(1)]).hasMore, isTrue);
    });

    test('ends on the first empty page', () {
      expect(openEndedPage(3, const []).hasMore, isFalse);
    });
  });
}
