import 'dart:convert';
import 'dart:async';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_book_screen.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/book_link_chips.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/dashboard/ui/requester_author_detail_screen.dart';
import 'package:cantinarr/features/dashboard/ui/requester_series_detail_screen.dart';
import 'package:cantinarr/features/media_detail/logic/title_links.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The requester book detail surface, exercised through the real router with a
/// faked backend: an owned digest row resolves the presentation, the push
/// payload's title names an unresolvable book, and a dead id degrades to a
/// graceful not-found state that points back to the Books tab.
void main() {
  testWidgets('a route refresh without its search payload keeps loaded details',
      (tester) async {
    final adapter = _BooksAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);
    const location = '/detail/book/555?instance_id=books';
    router.go(location,
        extra: ChaptarrBook(
          id: 0,
          title: 'Dune Messiah',
          foreignBookId: '555',
          foreignEditionId: 'gr:501',
          releaseDate: DateTime(1969),
          pageCount: 336,
          overview: 'The desert planet has a new emperor.',
        ));
    await tester.pumpAndSettle();
    expect(find.text('1969 · 336 pages'), findsOneWidget);
    final panel = find.byType(BookFormatPanel);
    final panelState = tester.state(panel);
    final panelTop = tester.getTopLeft(panel);
    final checks = adapter.statusForeignIds.length;
    router.go(location);
    await tester.pumpAndSettle();
    expect(find.text('1969 · 336 pages'), findsOneWidget);
    expect(find.text('The desert planet has a new emperor.'), findsOneWidget);
    expect(tester.state(panel), same(panelState));
    expect(tester.getTopLeft(panel), panelTop);
    expect(adapter.lookupTerms, isEmpty);
    expect(adapter.statusForeignIds, hasLength(checks));
  });

  for (final width in [320.0, 390.0, 1280.0]) {
    for (final scale in [1.0, 2.0]) {
      testWidgets(
          'full synopsis keeps request state while scrolling at $width, scale $scale',
          (tester) async {
        final adapter = _BooksAdapter();
        final (:router, container: _) = await _pumpRouter(tester,
            adapter: adapter,
            size: Size(width, 844),
            textScale: scale,
            themed: true);
        final full = List.generate(
            24,
            (i) => 'Paragraph $i follows the next part of this long adventure. '
                'The desert planet has a new emperor, and the story continues.').join(
            '\n\n');
        router.go('/detail/book/555?instance_id=books&q=dune',
            extra: ChaptarrBook(
                id: 0,
                title: 'Dune Messiah',
                foreignBookId: '555',
                overview: full,
                goodreadsBookId: 'gr:5907',
                genres: const ['Science Fiction']));
        await tester.pumpAndSettle();
        final synopsis = find.text(full);
        expect(synopsis, findsOneWidget);
        expect(tester.widget<Text>(synopsis).maxLines, isNull);
        expect(find.text('Read more'), findsNothing);
        expect(find.text('Read less'), findsNothing);
        expect(tester.getTopLeft(find.text('Science Fiction')).dy,
            greaterThan(tester.getBottomLeft(synopsis).dy));
        final panel = find.byType(BookFormatPanel);
        final state = tester.state(panel);
        final checks = adapter.statusForeignIds.length;
        await tester.scrollUntilVisible(find.text('Links'), 600,
            maxScrolls: 100, scrollable: _detailScrollable());
        expect(tester.state(panel), same(state));
        expect(adapter.statusForeignIds, hasLength(checks));
        final position =
            tester.state<ScrollableState>(_detailScrollable()).position;
        final offset = position.pixels;
        router.go('/detail/book/555?instance_id=books&q=dune');
        await tester.pumpAndSettle();
        expect(position.pixels, offset);
        expect(find.text(full), findsOneWidget);
        await tester.scrollUntilVisible(panel, -600,
            maxScrolls: 100, scrollable: _detailScrollable());
        expect(tester.state(panel), same(state));
        expect(find.text('Requested'), findsNWidgets(2));
        expect(adapter.statusForeignIds, hasLength(checks));
        expect(adapter.lookupTerms, isEmpty);
        expect(tester.takeException(), isNull);
      });
      testWidgets(
          'late download actions retain request state at $width, scale $scale',
          (tester) async {
        final files = Completer<void>();
        final adapter = _BooksAdapter(
            verifiedIdentity: true, bookFiles: true, filesReady: files.future);
        final (:router, container: _) = await _pumpRouter(tester,
            adapter: adapter,
            size: Size(width, 844),
            textScale: scale,
            themed: true,
            authState: _adminDownloadBooksState);
        router.go('/detail/book/29749107?instance_id=books&title=Ahsoka');
        await tester.pumpAndSettle();
        final ebook = find.byKey(const ValueKey('book-format-row:ebook'));
        final panel = find.byType(BookFormatPanel);
        final panelState = tester.state(panel);
        final checks = adapter.statusForeignIds.length;
        expect(find.text('Available'), findsOneWidget);
        files.complete();
        await tester.pumpAndSettle();
        expect(find.descendant(of: ebook,
            matching: find.byTooltip('Download eBook')), findsOneWidget);
        expect(find.text('Available'), findsOneWidget);
        expect(tester.state(panel), same(panelState));
        expect(adapter.statusForeignIds, hasLength(checks));
        expect(find.text('About this book'), findsOneWidget);
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets(
      'a cold link keeps its library year through a sparse lookup and route refresh',
      (tester) async {
    final reply = Completer<List<Map<String, dynamic>>>();
    final adapter = _BooksAdapter(lookupOverride: (_) => reply.future);
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);
    router.go('/detail/book/29749107?instance_id=books&title=Ahsoka');
    // Finish navigation while the metadata response is deliberately pending.
    await tester.pumpAndSettle();
    expect(find.text('2016'), findsOneWidget);
    final panel = find.byType(BookFormatPanel);
    final panelState = tester.state(panel);
    final panelTop = tester.getTopLeft(panel);
    router.go('/detail/book/29749107?instance_id=books&title=Ahsoka');
    await tester.pumpAndSettle();
    expect(adapter.lookupTerms, ['29749107']);
    reply.complete([
      for (final format in ['ebook', 'audiobook'])
        {
          'foreignBookId': '29749107',
          'title': 'Ahsoka',
          'mediaType': format,
          'overview': '',
        },
    ]);
    await tester.pumpAndSettle();
    expect(find.text('2016'), findsOneWidget);
    expect(tester.getTopLeft(panel), panelTop);
    expect(tester.state(panel), same(panelState));
  });

  testWidgets(
      'a supplied source preview stays intact without expansion or metadata controls',
      (tester) async {
    final adapter = _BooksAdapter(verifiedIdentity: true);
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);
    router.go('/detail/book/gr:101?instance_id=books&q=ahso',
        extra: const ChaptarrBook(
            id: 0,
            title: 'Ahsoka (Star Wars)',
            foreignBookId: 'gr:101',
            foreignEditionId: 'gr:501',
            pageCount: 400,
            overview: 'An alternative cover for this ASIN can be found here\n\n'
                'A former Jedi searches for a new path…'));
    await tester.pumpAndSettle();
    expect(find.text('A former Jedi searches for a new path…'), findsOneWidget);
    expect(find.textContaining('alternative cover'), findsNothing);
    expect(find.text('2016 · 400 pages'), findsOneWidget);
    expect(find.textContaining('Catalog details'), findsNothing);
    expect(find.textContaining('Library edition'), findsNothing);
    expect(find.text('Read more'), findsNothing);
    expect(find.text('Read less'), findsNothing);
    expect(find.text('Loading more details…'), findsNothing);
    expect(find.text('Retry'), findsNothing);
    expect(adapter.lookupTerms, isEmpty);
  });

  testWidgets('a successful cold title fallback clears an exact-lookup error',
      (tester) async {
    final adapter = _BooksAdapter(lookupOverride: (term) async {
      if (term == '555') throw StateError('lookup unavailable');
      return [
        {
          'foreignBookId': '555',
          'title': 'Dune Messiah',
          'overview': 'The complete description.'
        }
      ];
    });
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);
    router.go('/detail/book/555?instance_id=books&title=Dune%20Messiah');
    await tester.pumpAndSettle();
    expect(adapter.lookupTerms, ['555', 'Dune Messiah']);
    expect(find.text('The complete description.'), findsOneWidget);
    expect(find.text('Couldn’t load more details'), findsNothing);
    expect(find.text('Retry'), findsNothing);
  });

  for (final size in [const Size(390, 844), const Size(1280, 900)]) {
    testWidgets(
        'verified catalog edition keeps metadata and library actions at ${size.width}',
        (tester) async {
      final adapter = _BooksAdapter(verifiedIdentity: true);
      final (:router, container: _) =
          await _pumpRouter(tester, adapter: adapter, size: size, themed: true);
      router.go('/detail/book/gr:101?instance_id=books&q=ahso',
          extra: ChaptarrBook.fromJson({
            'foreignBookId': 'gr:101',
            'title': 'Ahsoka (Star Wars)',
            'author': {'authorName': 'E. K. Johnston'},
            'releaseDate': '2016-10-11T00:00:00Z',
            'pageCount': 400,
            'editions': [
              {
                'id': 1,
                'publisher': 'Disney Lucasfilm Press',
                'format': 'Paperback',
                'pageCount': 400
              }
            ],
          }));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(find.text('Ahsoka (Star Wars)'), findsOneWidget);
      expect(find.text('2016 · 400 pages'), findsOneWidget);
      expect(find.text('Disney Lucasfilm Press · Paperback'), findsOneWidget);
      expect(find.textContaining('Catalog details'), findsNothing);
      expect(find.textContaining('Library edition'), findsNothing);
      expect(find.textContaining('223 pages'), findsNothing);
      expect(find.byKey(const ValueKey('book-author-link')), findsOneWidget);
      expect(find.byKey(const ValueKey('book-series-link')), findsOneWidget);
      expect(adapter.statusForeignIds, everyElement('gr:101'));
      expect(router.routeInformationProvider.value.uri.path,
          '/detail/book/gr:101');
      final ebook = find.byKey(const ValueKey('book-format-row:ebook'));
      await tester.scrollUntilVisible(ebook, 150,
          scrollable: _detailScrollable());
      expect(find.text('Available'), findsOneWidget);
      expect(tester.widget<InkWell>(ebook).onTap, isNull);
      // The available eBook is bound to the library. The missing audio request
      // still carries the original catalog selection, title and search term.
      final audio = find.byKey(const ValueKey('book-format-row:audiobook'));
      await tester.scrollUntilVisible(audio, 150,
          scrollable: _detailScrollable());
      await tester.tap(audio);
      await tester.pumpAndSettle();
      expect(adapter.requestBodies.single['foreign_id'], 'gr:101');
      expect(adapter.requestBodies.single['title'], 'Ahsoka (Star Wars)');
      expect(adapter.requestBodies.single['search_term'], 'ahso');
      expect(adapter.requestBodies.single['book_format'], 'audiobook');
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('an owned digest row resolves the full book presentation',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Ahsoka'), findsOneWidget);
    expect(find.text('E. K. Johnston'), findsOneWidget);
    expect(find.text('2016'), findsOneWidget);
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    // Flock-style mixed truth: a monitored audiobook is Requested while the
    // untouched eBook row is itself the one exact action.
    expect(find.text('Request'), findsOneWidget);
    expect(find.text('Not requested'), findsNothing);
    expect(
      tester
          .widget<InkWell>(find.byKey(const ValueKey('book-format-row:ebook')))
          .onTap,
      isNotNull,
    );
    expect(
      tester
          .widget<InkWell>(
              find.byKey(const ValueKey('book-format-row:audiobook')))
          .onTap,
      isNull,
    );
    expect(find.text('Manage book'), findsNothing);
  });

  testWidgets('a deep link resolves rich metadata and both requested formats',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Frank Herbert'), findsOneWidget);
    expect(find.text('1969 · 336 pages'), findsOneWidget);
    expect(
      find.text(
          'The desert planet has a new emperor.\n\nA second chapter & more.'),
      findsOneWidget,
    );
    expect(find.textContaining('<b>'), findsNothing);
    expect(find.text('Science Fiction'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
  });

  testWidgets('a native deep link rejects metadata from a different ID',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(mismatchedLookupId: true),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Frank Herbert'), findsOneWidget);
    expect(find.text('1969 · 336 pages'), findsNothing);
  });

  testWidgets(
      'a provider-id mismatch rejects same-title metadata from another author',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(
        mismatchedLookupId: true,
        mismatchedLookupAuthor: true,
      ),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Brian Herbert'), findsNothing);
    expect(find.text('1969 · 336 pages'), findsNothing);
    expect(find.text('Science Fiction'), findsNothing);
    expect(find.textContaining('desert planet'), findsNothing);
  });

  testWidgets('tapping a still-open format row requests exactly that format',
      (tester) async {
    final adapter = _BooksAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('book-format-row:ebook')));
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['book_format'], 'ebook');
    expect(adapter.requestBodies.single['foreign_id'], '29749107');
    // The requester is told what happened and sees the row settle into it.
    expect(find.text('eBook requested.'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
    expect(find.text('Request'), findsNothing);
  });

  testWidgets(
      'a re-keyed record updates ownership while the selected ID stays fixed',
      (tester) async {
    final adapter = _BooksAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/book/lookup-29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    // The first read goes out under the routed lookup id; the server resolves
    // the stored record and answers with the library's canonical id. Later
    // status reads retain the original selected id.
    expect(adapter.statusForeignIds.first, 'lookup-29749107');
    expect(adapter.statusForeignIds.last, 'lookup-29749107');
    // The owned digest row (canonical id) binds: the monitored audiobook reads
    // Requested while the untouched eBook row remains the open action.
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);
    expect(find.text('Not requested'), findsNothing);
  });

  testWidgets('a known format stays visible beside an unknown sibling',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(partiallyUnknownStatus: true),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Format needs attention'), findsOneWidget);
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
  });

  testWidgets('an admin can open both exact-format records in Chaptarr',
      (tester) async {
    final (:router, container: _) =
        await _pumpRouter(tester, authState: _adminBooksState);

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.text('Manage book'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.text('Manage book'), findsOneWidget);
    await tester.tap(find.text('Manage book'));
    await tester.pumpAndSettle();

    expect(find.byType(ChaptarrBookScreen), findsOneWidget);
    final screen = tester.widget<ChaptarrBookScreen>(
      find.byType(ChaptarrBookScreen),
    );
    expect(screen.instanceId, 'books');
    expect(screen.records, hasLength(2));
    expect(screen.records.map((book) => book.mediaType),
        orderedEquals(['ebook', 'audiobook']));
  });

  testWidgets('a requester can download each exact available book format',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      authState: _downloadBooksState,
      adapter: _BooksAdapter(bookFiles: true),
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.byTooltip('Download eBook'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download audiobook'), findsOneWidget);
    expect(find.textContaining('/library/'), findsNothing);
    expect(find.textContaining(r'Z:\'), findsNothing);
  });

  testWidgets('a lost binding clears downloads even when the next read fails',
      (tester) async {
    final adapter = _BooksAdapter(bookFiles: true);
    final (:router, :container) = await _pumpRouter(
      tester,
      authState: _downloadBooksState,
      adapter: adapter,
    );
    router.go('/detail/book/lookup-29749107?title=Ahsoka');
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.byTooltip('Download eBook'), 250,
        scrollable: _detailScrollable());
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download audiobook'), findsOneWidget);

    adapter.statusOverride = {
      'status': 'unavailable',
      'status_known': false,
      'status_unknown_reason': 'identity_ambiguous',
    };
    adapter.failBookRead = true;
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();

    expect(find.byTooltip('Download eBook'), findsNothing);
    expect(find.byTooltip('Download audiobook'), findsNothing);
    expect(find.text('Library match needs attention'), findsNWidgets(2));
    expect(tester.takeException(), isNull);
  });

  testWidgets(
      'a download affordance is withheld for files outside the mappings',
      (tester) async {
    final adapter = _BooksAdapter(
      bookFiles: true,
      coverage: {
        '/library/E. K. Johnston/Ahsoka.epub': true,
        r'Z:\Audiobooks\E. K. Johnston\Ahsoka.m4b': false,
      },
    );
    final (:router, container: _) = await _pumpRouter(
      tester,
      authState: _downloadBooksState,
      adapter: adapter,
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.byTooltip('Download eBook'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    // The audiobook file exists in Chaptarr but no mapping covers it, so no
    // affordance that could only ever fail is offered.
    expect(find.byTooltip('Download audiobook'), findsNothing);
    expect(
      adapter.coveragePaths.toSet(),
      containsAll({
        '/library/E. K. Johnston/Ahsoka.epub',
        r'Z:\Audiobooks\E. K. Johnston\Ahsoka.m4b',
      }),
    );
  });

  testWidgets('enabling this instance refreshes downloads on an open detail',
      (tester) async {
    final (:router, :container) = await _pumpRouter(
      tester,
      authState: _exactDownloadBooksState(false),
      adapter: _BooksAdapter(bookFiles: true),
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();
    expect(find.byTooltip('Download eBook'), findsNothing);

    final notifier = container.read(authProvider.notifier);
    (notifier as _FakeAuthNotifier).replace(_exactDownloadBooksState(true));
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.byTooltip('Download eBook'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download audiobook'), findsOneWidget);
  });

  testWidgets('admin Chaptarr detail has the same per-format downloads',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      authState: _adminDownloadBooksState,
      adapter: _BooksAdapter(bookFiles: true),
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('Manage book'),
      250,
      scrollable: _detailScrollable(),
    );
    await tester.tap(find.text('Manage book'));
    await tester.pumpAndSettle();

    expect(find.byType(ChaptarrBookScreen), findsOneWidget);
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download Audiobook'), findsOneWidget);
  });

  testWidgets('an absolute owned cover origin is never sent to the client',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      ownedCover: 'http://chaptarr:8787/MediaCover/Books/42/cover.jpg',
    );

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    final cover = tester.widget<CachedImage>(
      find.descendant(
        of: find.byType(RequesterBookDetailScreen),
        matching: find.byType(CachedImage),
      ),
    );
    expect(cover.url, isNull);
  });

  testWidgets('an admin link requires an exact live foreign id match',
      (tester) async {
    final (:router, container: _) =
        await _pumpRouter(tester, authState: _adminBooksState);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    // The live list contains Ahsoka, but not this metadata-only Dune result.
    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Manage book'), findsNothing);
  });

  testWidgets('a pinned detail ignores ownership from the active instance',
      (tester) async {
    final adapter = _BooksAdapter(divergentLibraries: true);
    final (:router, :container) = await _pumpRouter(
      tester,
      authState: _adminBooksState,
      adapter: adapter,
    );

    router.go(
      '/detail/book/29749107?title=Ahsoka&instance_id=books',
    );
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);

    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books-two');
    await tester.pumpAndSettle();

    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);
    expect(find.text('Available'), findsNothing);
    expect(adapter.libraryInstanceIds, everyElement('books'));
    expect(adapter.statusInstanceIds, everyElement('books'));
  });

  testWidgets('the series line renders with its stated position',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(find.text('Discworld #13'), findsOneWidget);
    expect(find.byKey(const ValueKey('book-series-link')), findsOneWidget);
  });

  testWidgets('a book the library states no series for shows no series line',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(noSeries: true),
    );

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(find.textContaining('Discworld'), findsNothing);
    expect(find.byKey(const ValueKey('book-series-link')), findsNothing);
  });

  testWidgets(
      'tapping the series link leaves the book detail for the series route',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('book-series-link')));
    await tester.pumpAndSettle();

    // GoRouter's routeInformationProvider does not observably update for an
    // imperative push nested under this route pattern in this harness, so the
    // navigation is asserted structurally instead: the book detail screen is
    // gone and the pushed series screen's own constructor arguments (set
    // before any network call resolves) carry the exact name and instance —
    // this is the destination *route*, not its fetched contents.
    expect(find.byType(RequesterBookDetailScreen), findsNothing);
    final seriesScreen = tester.widget<RequesterSeriesDetailScreen>(
      find.byType(RequesterSeriesDetailScreen),
    );
    expect(seriesScreen.seriesName, 'Discworld');
    expect(seriesScreen.instanceId, 'books');
  });

  testWidgets('the author line is tappable when the digest states its identity',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(find.text('E. K. Johnston'), findsOneWidget);
    expect(find.byKey(const ValueKey('book-author-link')), findsOneWidget);
  });

  testWidgets('an author with no stated library identity renders as plain text',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(noAuthorLink: true),
    );

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    // The name still renders — only the tap target is withheld, because
    // /detail/author/{id} has no id it could resolve for this row.
    expect(find.text('E. K. Johnston'), findsOneWidget);
    expect(find.byKey(const ValueKey('book-author-link')), findsNothing);
  });

  testWidgets(
      'tapping the author link leaves the book detail for the author route',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('book-author-link')));
    await tester.pumpAndSettle();

    // Asserted structurally for the same reason as the series link above: the
    // pushed screen's constructor arguments are the destination route, set
    // before any network call resolves.
    expect(find.byType(RequesterBookDetailScreen), findsNothing);
    final authorScreen = tester.widget<RequesterAuthorDetailScreen>(
      find.byType(RequesterAuthorDetailScreen),
    );
    expect(authorScreen.foreignAuthorId, 'hc:auth-ekj');
    expect(authorScreen.nameHint, 'E. K. Johnston');
    expect(authorScreen.instanceId, 'books');
  });

  testWidgets('a resolved book links out to Goodreads and Open Library',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.widgetWithText(ActionChip, 'Open Library'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.text('Links'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Goodreads'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Open Library'), findsOneWidget);
    expect(find.byTooltip('Open on Goodreads'), findsOneWidget);
    // Hardcover pages are slug-addressed, so a chip needs a declared link and
    // the fixture declares none.
    expect(find.widgetWithText(ActionChip, 'Hardcover'), findsNothing);
  });

  testWidgets('a book whose record names no outside page has no Links line',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(noLinkIds: true),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Links', skipOffstage: false), findsNothing);
    expect(find.byType(ActionChip, skipOffstage: false), findsNothing);
  });

  testWidgets('a page opened by id and title resolves by the exact id first',
      (tester) async {
    final adapter = _BooksAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    // The Books tab rows navigate with the id and title only, no record
    // riding along, and this provider answers nothing for a full title.
    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    // The id fetch answered, so the title search was never needed.
    expect(adapter.lookupTerms, ['29749107']);
    await tester.scrollUntilVisible(
      find.widgetWithText(ActionChip, 'Open Library'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.text('Links'), findsOneWidget);
    // The edition's ISBN came through: the list endpoint never carries
    // editions, so only the exact-id fetch could have named it.
    final chips = tester.widget<BookLinkChips>(find.byType(BookLinkChips));
    expect(chips.links, const [
      TitleLink('Goodreads', 'https://www.goodreads.com/book/show/29749107'),
      TitleLink('Open Library', 'https://openlibrary.org/isbn/9781484705667'),
    ]);
  });

  testWidgets('an id fetch answering another id is not adopted',
      (tester) async {
    final adapter = _BooksAdapter(mismatchedLookupId: true);
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    // The id fetch answered the work under the provider's current id
    // (lookup-555): the alias-to-canonical case, and not this page's record,
    // so the title path and its strong-match rule still had to decide.
    expect(adapter.lookupTerms, ['555', 'Dune Messiah']);
    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Frank Herbert'), findsOneWidget);
    expect(find.text('1969 · 336 pages'), findsNothing);
  });

  testWidgets('an unresolvable id shows a graceful state with a Books tab exit',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/does-not-exist');
    await tester.pumpAndSettle();

    expect(
      find.text(
        'This book could not be found. It may have been removed from '
        'the library.',
      ),
      findsOneWidget,
    );

    await tester.tap(find.text('Browse Books'));
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
  });
}

const _booksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _adminBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'books-two',
        serviceType: 'chaptarr',
        name: 'Other Books',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

const _downloadBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, mediaDownloads: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _adminDownloadBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, mediaDownloads: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

AuthState _exactDownloadBooksState(bool enabled) => AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(
          chaptarr: true,
          mediaDownloads: enabled,
        ),
        instances: [
          ServiceInstance(
            id: 'books',
            serviceType: 'chaptarr',
            name: 'Books',
            isDefault: true,
            mediaDownloads: enabled,
          ),
        ],
      ),
      user: const UserProfile(id: 1, username: 'tester', role: 'user'),
    );

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
  WidgetTester tester, {
  AuthState authState = _booksState,
  String ownedCover = '',
  _BooksAdapter? adapter,
  Size size = const Size(390, 844),
  bool themed = false,
  double textScale = 1,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState)),
      backendClientProvider.overrideWithValue(_fakeDio(
        ownedCover: ownedCover,
        adapter: adapter,
      )),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await container.pump();
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(
          routerConfig: router,
          theme: themed ? AppTheme.dark : null,
          builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context).copyWith(
                  textScaler: TextScaler.linear(textScale),
                ),
                child: child!,
              )),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, router: router);
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;

  void replace(AuthState next) {
    state = AsyncData(next);
  }
}

Dio _fakeDio({String ownedCover = '', _BooksAdapter? adapter}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter ?? _BooksAdapter(ownedCover: ownedCover);
  return dio;
}

/// Serves requester metadata/status plus the live Chaptarr records an admin
/// resolves before showing the internal module link.
class _BooksAdapter implements HttpClientAdapter {
  final Future<List<Map<String, dynamic>>> Function(String)? lookupOverride;
  final Future<void>? filesReady;
  final bool verifiedIdentity;
  final String ownedCover;
  final bool divergentLibraries;
  final bool mismatchedLookupId;
  final bool mismatchedLookupAuthor;
  final bool partiallyUnknownStatus;
  final bool bookFiles;
  bool failBookRead = false;
  Map<String, dynamic>? statusOverride;

  /// Suppresses the `series`/`series_position` keys on the Ahsoka digest row,
  /// defaulted so every other test keeps its current behaviour unchanged.
  final bool noSeries;

  /// Suppresses the `author_foreign_id` key on the Ahsoka digest row, so the
  /// author name is stated without the library identity that makes it tappable.
  final bool noAuthorLink;

  /// Suppresses the provider ids (and the edition ISBN) on the lookup
  /// records, so a page has no outside page to link to.
  final bool noLinkIds;

  /// Coverage verdicts by reported path; unlisted paths count as covered.
  final Map<String, bool> coverage;
  final coveragePaths = <String>[];
  final libraryInstanceIds = <String>[];
  final statusInstanceIds = <String>[];
  final statusForeignIds = <String>[];
  final lookupTerms = <String>[];
  final requestBodies = <Map<String, dynamic>>[];
  final _requestedFormats = <String, String>{};

  _BooksAdapter({
    this.lookupOverride,
    this.filesReady,
    this.verifiedIdentity = false,
    this.ownedCover = '',
    this.divergentLibraries = false,
    this.mismatchedLookupId = false,
    this.mismatchedLookupAuthor = false,
    this.partiallyUnknownStatus = false,
    this.bookFiles = false,
    this.noSeries = false,
    this.noAuthorLink = false,
    this.noLinkIds = false,
    this.coverage = const {},
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.method == 'POST' &&
        options.path == '/api/media-files/coverage') {
      final data = Map<String, dynamic>.from(options.data as Map);
      final paths = (data['paths'] as List).cast<String>();
      coveragePaths.addAll(paths);
      body = {
        'covered': [for (final path in paths) coverage[path] ?? true],
      };
    } else if (options.method == 'POST' && options.path == '/api/requests') {
      final data = Map<String, dynamic>.from(options.data as Map);
      requestBodies.add(data);
      final format = data['book_format'] as String;
      _requestedFormats[format] = 'requested';
      body = {
        'status': 'requested',
        'book_formats': {format: 'requested'},
      };
    } else if (options.path == '/api/requests/book-library') {
      final instanceId = options.queryParameters['instance_id'].toString();
      libraryInstanceIds.add(instanceId);
      final otherLibrary = divergentLibraries && instanceId == 'books-two';
      final ahsokaRow = <String, dynamic>{
        'title': 'Ahsoka',
        'author': 'E. K. Johnston',
        'author_foreign_id': 'hc:auth-ekj',
        'year': 2016,
        'series': 'Discworld',
        'series_position': '13',
        // Empty by default so most tests never start a real image fetch.
        'cover': ownedCover,
        'foreign_book_id': '29749107',
        'ebook': {
          'monitored': otherLibrary,
          'downloaded': otherLibrary,
        },
        'audiobook': {
          'monitored': !otherLibrary,
          'downloaded': false,
        },
      };
      if (verifiedIdentity) {
        ahsokaRow['identity_keys'] = ['gr-work:101'];
        ahsokaRow['series'] = 'Star Wars Disney Canon Novel';
        ahsokaRow['series_position'] = '';
        ahsokaRow['ebook'] = {'downloaded': true, 'monitored': true};
        ahsokaRow['audiobook'] = {'downloaded': false, 'monitored': false};
      }
      if (noSeries) {
        ahsokaRow.remove('series');
        ahsokaRow.remove('series_position');
      }
      if (noAuthorLink) {
        ahsokaRow.remove('author_foreign_id');
      }
      body = {
        'titles': [
          ahsokaRow,
          if (mismatchedLookupId)
            {
              'title': 'Dune Messiah',
              'author': 'Frank Herbert',
              'year': 0,
              'cover': '',
              'foreign_book_id': '555',
              'ebook': {
                'monitored': true,
                'downloaded': false,
              },
              'audiobook': {
                'monitored': true,
                'downloaded': false,
              },
            },
        ],
      };
    } else if (options.path == '/api/requests/book-status') {
      statusInstanceIds.add(
        options.queryParameters['instance_id'].toString(),
      );
      statusForeignIds.add(
        options.queryParameters['foreign_id'].toString(),
      );
      body = statusOverride ??
          switch (options.queryParameters['foreign_id']) {
            'gr:101' => {
                'status': 'partial',
                'canonical_foreign_id': '29749107',
                'book_formats': {'ebook': 'available', ..._requestedFormats},
              },
            '29749107' => {
                'status': 'requested',
                'book_formats': {
                  'audiobook': 'requested',
                  ..._requestedFormats,
                },
              },
            // A request logged under a metadata lookup id whose created record
            // Chaptarr filed under the canonical library id above.
            'lookup-29749107' => {
                'status': 'requested',
                'book_formats': {'audiobook': 'requested'},
                'canonical_foreign_id': '29749107',
              },
            '555' => {
                'status': partiallyUnknownStatus ? 'partial' : 'requested',
                'status_known': !partiallyUnknownStatus,
                'book_formats': partiallyUnknownStatus
                    ? {
                        'ebook': 'requested',
                        'audiobook': 'future-status',
                      }
                    : {
                        'ebook': 'requested',
                        'audiobook': 'requested',
                      },
              },
            _ => {'status': 'unavailable'},
          };
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      final term = options.queryParameters['term'].toString();
      lookupTerms.add(term);
      body = lookupOverride != null
          ? await lookupOverride!(term)
          : switch (term) {
              // An id term is an exact fetch. For a book the library tracks that
              // is the record itself, editions included; an alias id resolves to
              // the same canonical record.
              '29749107' || 'lookup-29749107' => [_ahsokaLookup()],
              // The metadata work, under the id the provider currently keys it by
              // (the mismatch variant models an older id it has since re-keyed).
              // Its title search hits; Ahsoka's, a full title, answers nothing,
              // as this provider routinely does.
              '555' || 'Dune Messiah' => [_duneLookup()],
              _ => <Object>[],
            };
    } else if (options.path.endsWith('/api/v1/book/42')) {
      body = _liveBook(id: 42, mediaType: 'ebook');
    } else if (options.path.endsWith('/api/v1/book/43')) {
      body = _liveBook(id: 43, mediaType: 'audiobook');
    } else if (options.path.endsWith('/api/v1/book')) {
      if (failBookRead) {
        return ResponseBody.fromString('{}', 503);
      }
      body = [
        _liveBook(id: 42, mediaType: 'ebook'),
        _liveBook(id: 43, mediaType: 'audiobook'),
      ];
    } else if (options.path.endsWith('/api/v1/bookfile')) {
      await filesReady;
      final bookId = options.queryParameters['bookId'] as int?;
      body = bookFiles && (bookId == 42 || bookId == 43)
          ? [
              {
                'id': bookId! + 100,
                'bookId': bookId,
                'path': bookId == 42
                    ? '/library/E. K. Johnston/Ahsoka.epub'
                    : r'Z:\Audiobooks\E. K. Johnston\Ahsoka.m4b',
                'size': bookId == 42 ? 4200000 : 420000000,
                'quality': {
                  'quality': {'name': bookId == 42 ? 'EPUB' : 'M4B'},
                },
              },
            ]
          : <Object>[];
    } else if (options.path == '/api/trakt/anticipated') {
      body = <Object>[];
    } else {
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 0,
        'total_results': 0,
      };
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  /// The library's own Ahsoka record as `book/lookup` returns it for its id:
  /// unlike the list endpoint's copy, it carries the editions.
  Map<String, dynamic> _ahsokaLookup() => {
        'title': 'Ahsoka',
        'foreignBookId': '29749107',
        'releaseDate': '2016-10-11T00:00:00Z',
        'overview': 'A former Jedi searches for a new path.',
        if (!noLinkIds) 'goodreadsBookId': 'gr:29749107',
        'editions': [
          {
            'id': 1,
            'bookId': 42,
            'monitored': true,
            if (!noLinkIds) 'isbn13': '9781484705667',
          },
        ],
        'author': {
          'id': 7,
          'authorName': 'E. K. Johnston',
          'foreignAuthorId': 'hc:auth-ekj',
        },
      };

  Map<String, dynamic> _duneLookup() => {
        'title': 'Dune Messiah',
        'foreignBookId': mismatchedLookupId ? 'lookup-555' : '555',
        'year': 1969,
        'pageCount': 336,
        'overview': '<b>The desert planet has a new emperor.</b><br/><br/>'
            'A second chapter &amp; more.',
        'genres': ['Science Fiction'],
        if (!noLinkIds) ...{
          'goodreadsBookId': 'gr:5907',
          'openLibraryWorkId': 'ol:OL262758W',
        },
        'author': {
          'id': 0,
          'authorName':
              mismatchedLookupAuthor ? 'Brian Herbert' : 'Frank Herbert',
          'foreignAuthorId': 'author-2',
        },
      };

  @override
  void close({bool force = false}) {}
}

Map<String, dynamic> _liveBook({
  required int id,
  required String mediaType,
}) =>
    {
      'id': id,
      'title': 'Ahsoka',
      'foreignBookId': '29749107',
      'mediaType': mediaType,
      'monitored': true,
      'releaseDate': '2016-10-11T00:00:00Z',
      'overview': 'A former Jedi searches for a new path.',
      'author': {
        'id': 7,
        'authorName': 'E. K. Johnston',
        'foreignAuthorId': 'author-1',
      },
      'statistics': {'bookFileCount': 0, 'bookCount': 1},
    };

Finder _detailScrollable() => find.descendant(
      of: find.byType(RequesterBookDetailScreen),
      matching: find.byType(Scrollable),
    );
