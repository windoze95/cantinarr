import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/ui/library_authors_row.dart';
import 'package:cantinarr/features/dashboard/ui/library_series_row.dart';
import 'package:cantinarr/features/dashboard/ui/recently_added_books_row.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/discover/ui/book_search_results_view.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('a fully requested search row still opens rich book detail',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, :container, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    expect(searchField, findsOneWidget);
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Meditations'), findsOneWidget);
    // Both formats are covered, so no redundant aggregate status/action sits
    // beside the row. Per-format truth is on the detail surface.
    expect(find.text('Requested'), findsNothing);
    expect(find.byIcon(Icons.chevron_right), findsWidgets);

    expect(adapter.statusRequests, 0);
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(adapter.statusRequests, 0);

    await tester.tap(
      find.byKey(const ValueKey('book-result:book-1:book-1:lookup:0')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(adapter.statusRequests, greaterThan(0));
    expect(find.text('Marcus Aurelius'), findsOneWidget);
    expect(find.text('2002 · 304 pages'), findsOneWidget);
    expect(find.text('A practical guide to Stoic philosophy.'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
  });

  testWidgets('request controls live on book detail, not search results',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Request'), findsNothing);

    final secondResult =
        find.byKey(const ValueKey('book-result:book-2:book-2:lookup:1'));
    await tester.tap(secondResult);
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Letters from a Stoic'), findsOneWidget);
    // Both formats are still open, so each row carries its own request action.
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('Request'), findsNWidgets(2));

    final libraryRequestsBefore = adapter.libraryRequests;
    await tester.tap(find.byKey(const ValueKey('book-format-row:ebook')));
    await tester.pumpAndSettle();
    expect(adapter.libraryRequests, greaterThan(libraryRequestsBefore));
  });

  testWidgets(
      'fuzzy ownership keeps lookup metadata but uses the canonical library id',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, mismatchedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isEmpty);
    expect(
      find.byKey(
        const ValueKey('book-result:lookup-flock:library-flock:lookup:0'),
      ),
      findsOneWidget,
    );
    // The normal-row test above proves the tile gesture. Continue this case
    // through the exact route/extra the mismatched row owns so the remainder
    // can assert detail identity and mutation payload end to end.
    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(router.routeInformationProvider.value.uri.path,
        '/detail/book/library-flock');
    expect(
      router.routeInformationProvider.value.uri.queryParameters['instance_id'],
      'books',
    );
    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, 'library-flock');
    expect(screen.initialBook?.foreignBookId, 'lookup-flock');

    final ebookRow = find.byKey(const ValueKey('book-format-row:ebook'));
    await tester.scrollUntilVisible(
      ebookRow,
      250,
      scrollable: find.descendant(
        of: find.byType(RequesterBookDetailScreen),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.tap(ebookRow);
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'library-flock');
    expect(adapter.requestBodies.single['instance_id'], 'books');
    expect(adapter.requestBodies.single['book_format'], 'ebook');
  });

  testWidgets(
      'an unresolved fuzzy match keeps its canonical id and blocks requests',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(
      const ValueKey('book-result:lookup-flock:library-flock:lookup:0'),
    );
    expect(row, findsOneWidget);
    expect(adapter.statusForeignIds, isEmpty);
    expect(
      find.descendant(
        of: row,
        matching: find.text('Ask an admin to check this book’s format'),
      ),
      findsNothing,
    );
    expect(find.text('Request'), findsNothing);

    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Format needs attention'), findsNWidgets(2));
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
    expect(find.text('Request'), findsNothing);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('a mixed available and requested ownership chip stays requested',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, mixedOwnership: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final chip = tester.widget<Text>(
      find.text('eBook available · Audiobook requested'),
    );
    expect(chip.style?.color, AppTheme.requested);
  });

  testWidgets(
      'two lookup rows cannot silently bind to one canonical library record',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, ambiguousLookup: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(
      find.text('May be the same as a book listed above'),
      findsNWidgets(2),
    );
    final firstAmbiguous = find.byKey(
      const ValueKey('book-result:lookup-flock:lookup-flock:lookup:0'),
    );
    expect(firstAmbiguous, findsOneWidget);
    expect(
      find.byKey(
          const ValueKey('book-result:lookup-flock:lookup-flock:lookup:1')),
      findsOneWidget,
    );
    // The record the guidance points at is on screen, above both lookup rows.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:library:0')),
      findsOneWidget,
    );
    expect(adapter.statusForeignIds, isEmpty);

    // An unbindable row is still a real metadata record: it opens, addressed by
    // its own lookup id rather than a guessed library one.
    await tester.tap(firstAmbiguous);
    await tester.pumpAndSettle();

    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, 'lookup-flock');
    expect(adapter.statusForeignIds, everyElement('lookup-flock'));

    // The page that could not bind to its own library record points at the
    // record it may duplicate — in requester words, with the record's real
    // state — before any Request can be tapped.
    expect(
      find.text('Your library may already have this book'),
      findsOneWidget,
    );
    final lookalike =
        find.byKey(const ValueKey('book-lookalike:library-flock'));
    expect(lookalike, findsOneWidget);
    expect(
      find.descendant(
          of: lookalike, matching: find.text('Audiobook requested')),
      findsOneWidget,
    );

    // Tapping it lands on the record whose request state is real.
    await tester.tap(lookalike);
    await tester.pumpAndSettle();
    final opened = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen).last,
    );
    expect(opened.foreignId, 'library-flock');
    // A page bound to its own record needs no pointer.
    expect(
      find.text('Your library may already have this book'),
      findsNothing,
    );
  });

  testWidgets('an exact library id outranks a same-title sibling row',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, aliasSibling: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    // The row carrying the library's own id binds to it and reports its state;
    // the sibling's resemblance no longer cancels that identity.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:lookup:0')),
      findsOneWidget,
    );
    expect(find.text('Audiobook requested'), findsOneWidget);
    // With the record spoken for, the sibling is simply a book they don't have.
    expect(
      find.byKey(
          const ValueKey('book-result:lookup-flock:lookup-flock:lookup:1')),
      findsOneWidget,
    );
    expect(find.text('May be the same as a book listed above'), findsNothing);
    // The bound record is not repeated as a library row of its own.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:library:0')),
      findsNothing,
    );
    expect(adapter.statusForeignIds, isEmpty);
  });

  testWidgets('same-title library records are surfaced separately',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, duplicateLibraryRecords: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(
      find.text('May be the same as a book listed above'),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('book-result:library-a:library-a:library:0')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('book-result:library-b:library-b:library:1')),
      findsOneWidget,
    );
    expect(adapter.statusForeignIds, isEmpty);
  });

  testWidgets('a lookup row without a canonical id explains why it is blocked',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, blankIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(const ValueKey('book-result:::lookup:0'));
    expect(row, findsOneWidget);
    expect(
      find.descendant(
        of: row,
        matching: find.text('Ask an admin to check this book’s library record'),
      ),
      findsOneWidget,
    );
    expect(tester.widget<ListTile>(row).onTap, isNull);
    expect(adapter.statusForeignIds, isEmpty);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('book status and guidance fit a narrow phone at 200 percent text',
      (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
      tester.platformDispatcher.clearTextScaleFactorTestValue();
    });
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);

    final searchField = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Ask an admin to check this book’s format'), findsNothing);

    router.go('/detail/book/library-flock?title=Flock&instance_id=books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Format needs attention'), findsNWidgets(2));
  });

  testWidgets(
      'the Books tab browse rows still refresh on resume, library-changed '
      'events and an instance switch', (tester) async {
    _usePhoneSize(tester);
    final events = StreamController<WsEvent>.broadcast();
    addTearDown(events.close);

    final (:router, :container, :adapter) = await _pumpRouter(
      tester,
      events: events.stream,
      authState: _twoInstanceBooksState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    // Baseline after the idle tab's initial mount, before any trigger fires.
    var libraryBefore = adapter.libraryRequests;
    var recentBefore = adapter.recentRequests;
    var authorBefore = adapter.authorRequests;
    var seriesBefore = adapter.seriesRequests;

    // Trigger 1: app resume -> didChangeAppLifecycleState -> _refreshBookTruth().
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (app resume): owned-books truth re-pulls on app resume',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (app resume): recently-added row re-pulls on resume',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (app resume): authors row re-pulls on resume',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (app resume): series row re-pulls on resume',
    );

    libraryBefore = adapter.libraryRequests;
    recentBefore = adapter.recentRequests;
    authorBefore = adapter.authorRequests;
    seriesBefore = adapter.seriesRequests;

    // Trigger 2: a library-changed websocket event ->
    // libraryChangedEventsProvider listener -> _refreshBookTruth().
    events.add(const WsEvent(type: 'request_status_changed', data: {}));
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (library-changed event): owned-books truth re-pulls',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (library-changed event): recently-added row re-pulls',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (library-changed event): authors row re-pulls',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (library-changed event): series row re-pulls',
    );

    libraryBefore = adapter.libraryRequests;
    recentBefore = adapter.recentRequests;
    authorBefore = adapter.authorRequests;
    seriesBefore = adapter.seriesRequests;

    // Trigger 3: switching the active Chaptarr instance. The browse-row
    // providers (ownedBooksProvider/recentBooksProvider/bookAuthorsProvider/
    // bookSeriesProvider) each `ref.watch(instanceProvider)` directly, so a
    // state change re-runs all four against the new instance even though
    // DashboardBooksTab's own instance-switch listener only explicitly
    // invalidates ownedBooksProvider.
    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books-2');
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (instance switch): owned-books truth re-pulls against '
          'the new instance',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (instance switch): recently-added row re-pulls '
          'against the new instance',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (instance switch): authors row re-pulls against the '
          'new instance',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (instance switch): series row re-pulls against the '
          'new instance',
    );
  });

  testWidgets(
      'switching the active Chaptarr instance re-runs the book search '
      'against the new library', (tester) async {
    _usePhoneSize(tester);
    final (:router, :container, :adapter) = await _pumpRouter(
      tester,
      authState: _twoInstanceBooksState,
      perInstanceBooks: true,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    expect(toolbar, findsOneWidget);
    await tester.enterText(toolbar, 'library book');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('First Library Book'),
      ),
      findsOneWidget,
      reason: 'BOOK-07: the first instance\'s book is on screen before the '
          'switch',
    );

    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books-2');
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('First Library Book'),
      ),
      findsNothing,
      reason:
          'BOOK-07: the old instance\'s results are discarded, not merged',
    );
    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('Second Library Book'),
      ),
      findsOneWidget,
      reason: 'BOOK-07: the new instance\'s results replace them',
    );
    expect(
      adapter.bookLookupPaths.length,
      2,
      reason: 'BOOK-07: a second book/lookup call was issued for the switch',
    );
    expect(
      adapter.bookLookupPaths.last,
      contains('/instances/books-2/'),
      reason: 'BOOK-07: the second call targeted the new instance',
    );

    // Second scenario: with the toolbar empty, switching instances discards
    // nothing and issues no lookup at all.
    await tester.enterText(toolbar, '');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    final lookupsBeforeEmptySwitch = adapter.bookLookupPaths.length;

    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books');
    await tester.pumpAndSettle();

    expect(
      adapter.bookLookupPaths.length,
      lookupsBeforeEmptySwitch,
      reason: 'BOOK-07: switching instances with an empty toolbar issues no '
          'book lookup',
    );
    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'BOOK-07: the overlay is not on screen with an empty query',
    );
  });

  testWidgets(
      'a book typed in the top bar opens the requester book detail carrying '
      'the term that found it', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, mixedOwnership: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    // Locate the shell toolbar specifically via its CantinarrSearchBar
    // ancestor, naming the widget the locator targets, rather than relying
    // on there being exactly one TextField on the route — that stays correct
    // even if another text field is ever added elsewhere on this page.
    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    expect(toolbar, findsOneWidget);
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('Meditations'),
      ),
      findsOneWidget,
    );
    expect(
      find.text('eBook available · Audiobook requested'),
      findsOneWidget,
    );

    await tester.tap(
      find.byKey(const ValueKey('book-result:book-1:book-1:lookup:0')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    // context.push (not router.go) doesn't update
    // router.routeInformationProvider in this harness, so read the pushed
    // route's own resolved q= param off the screen it built instead — the
    // same signal BOOK-04 promises the request carries.
    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.searchTerm, 'meditations');
  });

  const noInstanceMessage = 'No Chaptarr instance is available.';
  const forbiddenMessage =
      'You do not have access to search this book library.';
  const requestFailedMessage =
      'Books could not be searched. Check the connection and try again.';
  // Now that the same search covers authors (SEARCH-01), the empty state can
  // no longer say only "no books" — it names everything that was searched.
  const noBooksMessage = 'No books or authors found. Try a different search.';

  Finder overlayText(String message) => find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text(message),
      );

  testWidgets(
      'a book search with no Chaptarr instance says so instead of showing '
      'an empty list', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      authState: _noInstanceBooksState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noInstanceMessage), findsOneWidget);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets(
      'a book search rejected for access says the user has no access to '
      'that book library', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, forbidden: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(forbiddenMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets('a book search that fails for any other reason invites a retry',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, serverError: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(requestFailedMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets(
      'a book search that ran and matched nothing says no books were found',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, emptyLookup: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noBooksMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
  });

  // GAP-SC3: the same four contracted messages, re-proven reachable at the
  // AvailableServices(chaptarr: true, ai: true) fixture combination — the
  // condition under which the CR-01 gate previously suppressed all four
  // (and the ordinary success case) behind the "Ask AI" takeover.

  testWidgets(
      'GAP-SC3: no Chaptarr instance still says so with AI available, for '
      'an AI-prompt-shaped term', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      authState: _noInstanceBooksWithAiState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'books like meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noInstanceMessage), findsOneWidget);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
    expect(find.byType(BookSearchResultsView), findsOneWidget,
        reason: 'GAP-SC3: the overlay carrying the message is on screen at '
            'all');
    expect(find.text('Type anything, then press send'), findsNothing);
    expect(find.text('Press send to ask AI'), findsNothing);
  });

  testWidgets(
      'GAP-SC3: a forbidden book search still says so with AI available',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      forbidden: true,
      authState: _booksWithAiState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(forbiddenMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
    expect(find.byType(BookSearchResultsView), findsOneWidget,
        reason: 'GAP-SC3: the overlay carrying the message is on screen at '
            'all');
    expect(find.text('Type anything, then press send'), findsNothing);
    expect(find.text('Press send to ask AI'), findsNothing);
  });

  testWidgets(
      'GAP-SC3: a generic book-search failure still invites a retry with '
      'AI available, idempotently', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      serverError: true,
      authState: _booksWithAiState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(requestFailedMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
    expect(find.byType(BookSearchResultsView), findsOneWidget,
        reason: 'GAP-SC3: the overlay carrying the message is on screen at '
            'all');
    expect(find.text('Type anything, then press send'), findsNothing);
    expect(find.text('Press send to ask AI'), findsNothing);

    // FAIL-02 idempotency: re-entering the identical failing term must
    // still yield exactly one message, never a duplicated or accumulated
    // one.
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(requestFailedMessage), findsOneWidget);
  });

  testWidgets(
      'GAP-SC3: a book search that matched nothing still says so with AI '
      'available', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      emptyLookup: true,
      authState: _booksWithAiState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noBooksMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(find.byType(BookSearchResultsView), findsOneWidget,
        reason: 'GAP-SC3: the overlay carrying the message is on screen at '
            'all');
    expect(find.text('Type anything, then press send'), findsNothing);
    expect(find.text('Press send to ask AI'), findsNothing);
  });

  testWidgets(
      'FAIL-02 concurrency: entering AI mode mid-lookup drops the '
      'in-flight Chaptarr response', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    // Only 200ms — the 400ms AppConfig.searchDebounce has not fired yet, so
    // the Chaptarr lookup is still pending.
    await tester.pump(const Duration(milliseconds: 200));
    // Arm the pause detector and open the explicit Ask AI door while the
    // lookup is in flight.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    // The shimmer border repeats while aiReady, so a bounded pump only —
    // pumpAndSettle would never settle.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    // Exit AI mode (the text is still non-empty, so the clear affordance's
    // tooltip is 'Clear message', not 'Exit AI mode') and give the debounce
    // window a chance to fire, if the dropped response were ever going to
    // land.
    await tester.tap(find.byTooltip('Clear message'));
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'FAIL-02 concurrency: the response in flight when AI mode was '
          'entered was dropped by the generation bump and never lands',
    );
  });

  testWidgets('the Books tab body has no search field of its own',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(
      find.byType(TextField),
      findsOneWidget,
      reason: 'TAB-01: the shell toolbar is the only text field on the '
          'Books tab route — the in-page search field is gone',
    );
    expect(
      find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byType(TextField),
      ),
      findsOneWidget,
      reason: 'TAB-01: the one remaining text field is the shell toolbar',
    );

    expect(find.byType(RecentlyAddedBooksRow), findsOneWidget,
        reason: 'TAB-01: the tab body is its browse rows');
    expect(find.byType(LibraryAuthorsRow), findsOneWidget,
        reason: 'TAB-01: the tab body is its browse rows');
    expect(find.byType(LibrarySeriesRow), findsOneWidget,
        reason: 'TAB-01: the tab body is its browse rows');
  });

  testWidgets(
    'the browse rows stay in the tree underneath an active book-search overlay',
    (tester) async {
      _usePhoneSize(tester);
      final (:router, container: _, adapter: _) = await _pumpRouter(tester);
      router.go('/dashboard/books');
      await tester.pumpAndSettle();

      expect(find.byType(RecentlyAddedBooksRow), findsOneWidget);
      expect(find.byType(LibraryAuthorsRow), findsOneWidget);
      expect(find.byType(LibrarySeriesRow), findsOneWidget);

      final toolbar = find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byType(TextField),
      );
      await tester.enterText(toolbar, 'meditations');
      await tester.pump(const Duration(milliseconds: 500));
      await tester.pumpAndSettle();

      expect(
        find.byType(BookSearchResultsView),
        findsOneWidget,
        reason: 'the shell overlay is on screen while the search is active',
      );
      expect(
        find.descendant(
          of: find.byType(BookSearchResultsView),
          matching: find.text('Meditations'),
        ),
        findsOneWidget,
      );

      expect(
        find.byType(RecentlyAddedBooksRow),
        findsOneWidget,
        reason: 'the row stays in the tree, covered by the overlay rather '
            'than removed — a future change that re-hides it during an '
            'active search must fail here',
      );
      expect(
        find.byType(LibraryAuthorsRow),
        findsOneWidget,
        reason: 'the row stays in the tree, covered by the overlay rather '
            'than removed — a future change that re-hides it during an '
            'active search must fail here',
      );
      expect(
        find.byType(LibrarySeriesRow),
        findsOneWidget,
        reason: 'the row stays in the tree, covered by the overlay rather '
            'than removed — a future change that re-hides it during an '
            'active search must fail here',
      );
    },
  );

  testWidgets(
      'GAP-SC1: an AI-prompt-shaped book term still brings up book results '
      'on the Books tab', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'books like dune');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.byType(BookSearchResultsView),
      findsOneWidget,
      reason: 'GAP-SC1: the book overlay is on screen, not the AI takeover',
    );
    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('Meditations'),
      ),
      findsOneWidget,
      reason: 'GAP-SC1: the Chaptarr lookup result renders even though the '
          'typed text matches the TMDB AI-intent heuristic',
    );
    expect(
      find.text('Type anything, then press send'),
      findsNothing,
      reason: 'GAP-SC1: no AI takeover covers the Books tab',
    );
    expect(
      find.text('Press send to ask AI'),
      findsNothing,
      reason: 'GAP-SC1: no AI takeover covers the Books tab',
    );
  });

  testWidgets(
      'GAP-SC1: an ordinary book term brings up book results with AI '
      'available', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('Meditations'),
      ),
      findsOneWidget,
      reason: 'GAP-SC1: the empty-TMDB auto-escalation must not hijack an '
          'ordinary Books-tab term either',
    );
  });

  testWidgets(
      'one keystroke on the Books tab reaches exactly one search notifier',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: adapter) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      adapter.tmdbSearchRequests,
      0,
      reason: 'a Books-tab keystroke must never reach the TMDB notifier',
    );
    expect(
      adapter.bookLookupPaths.length,
      1,
      reason: 'a Books-tab keystroke reaches the Chaptarr notifier exactly '
          'once',
    );
  });

  testWidgets('the explicit Ask AI door still opens on the Books tab',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.byType(BookSearchResultsView),
      findsOneWidget,
      reason: 'GAP-SC1 desync: book results are on screen before the '
          'explicit AI door is opened',
    );

    // Arm the pause detector behind the Ask AI pill.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(
      find.text('Ask AI').hitTestable(),
      findsOneWidget,
      reason: 'GAP-SC1 desync: the explicit Ask AI door still opens on the '
          'Books tab',
    );

    // The shimmer border repeats while aiReady, so bounded pumps only from
    // here — pumpAndSettle would never settle.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    expect(
      find.text('Press send to ask AI'),
      findsOneWidget,
      reason: 'GAP-SC1 desync: the takeover copy reads the typed text from '
          'the controller, not the (now unfed) TMDB notifier',
    );
    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'GAP-SC1 desync: AI mode was explicitly requested, so the '
          'book overlay is suppressed',
    );
  });

  testWidgets('clearing out of AI mode leaves no stale book results',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    // The shimmer border repeats while aiReady, so a bounded pump only —
    // pumpAndSettle would never settle.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    // Reach the pre-clear state the SC1 gap describes: AI mode entered from
    // a Books-tab query with a previously-hidden book result underneath.
    expect(find.byType(BookSearchResultsView), findsNothing);

    final clearIcon = find.byTooltip('Clear message');
    if (clearIcon.evaluate().isNotEmpty) {
      await tester.tap(clearIcon);
    } else {
      // Fallback entry into _exitAiMode if no clear icon is hit-testable in
      // this widget-test harness.
      await tester.enterText(toolbar, '');
    }
    await tester.pumpAndSettle();

    expect(
      tester.widget<TextField>(toolbar).controller!.text,
      isEmpty,
      reason: 'GAP-SC1 desync: clearing empties the toolbar',
    );
    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'GAP-SC1 desync: no stale book overlay reappears after the '
          'clear',
    );
    expect(
      find.text('Meditations'),
      findsNothing,
      reason: 'GAP-SC1 desync: no stale lookup result reappears after the '
          'clear',
    );

    // The exact re-appearance the gap describes must not happen on a later
    // frame either.
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      tester.widget<TextField>(toolbar).controller!.text,
      isEmpty,
      reason: 'GAP-SC1 desync: still empty after a further pump',
    );
    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'GAP-SC1 desync: the overlay does not reappear on a later '
          'frame',
    );
    expect(
      find.text('Meditations'),
      findsNothing,
      reason: 'GAP-SC1 desync: the stale result does not reappear on a '
          'later frame',
    );
  });

  testWidgets(
      'WR-01 parity: emptying the toolbar by typing leaves AI mode on the '
      'Books tab, as it does on every other discovery tab', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    // The shimmer border repeats while aiReady, so bounded pumps only —
    // pumpAndSettle would never settle.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    expect(
      tester.widget<TextField>(toolbar).decoration!.hintText,
      'Ask the AI anything...',
      reason: 'precondition: the explicit pill put the Books tab in AI mode',
    );

    // Backspace to empty by TYPING — deliberately not the clear button, which
    // routes through _exitAiMode and was never the broken path.
    await tester.enterText(toolbar, '');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));

    expect(
      tester.widget<TextField>(toolbar).decoration!.hintText,
      isNot('Ask the AI anything...'),
      reason: 'WR-01: an emptied field leaves AI mode on the Books tab too. '
          'Only ShellSearchNotifier.updateSearch("") clears _manualAiMode, so '
          'the empty string must still reach it while the rest of exclusive '
          'dispatch keeps every non-empty Books query away from that notifier.',
    );
    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'WR-01: leaving AI mode via an emptied field surfaces no book '
          'overlay — an empty query is not a search',
    );
  });

  testWidgets(
      'SEARCH-02/SEARCH-03: the Books tab names itself in the toolbar '
      'placeholder and prefix badge, on the real router', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    expect(
      tester.widget<TextField>(toolbar).decoration!.hintText,
      'Search books or authors...',
      reason: 'SEARCH-02: an empty, unfocused Books-tab toolbar names '
          'itself as a book search',
    );
    expect(
      find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byIcon(Icons.menu_book),
      ),
      findsOneWidget,
      reason: 'SEARCH-03: the prefix badge carries the Books tab\'s own '
          'icon outside AI mode',
    );
  });

  testWidgets(
      'SEARCH-03: an AI-capable server does not show the sparkle on the '
      'Books tab outside AI mode', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, authState: _booksWithAiState);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byIcon(Icons.auto_awesome_rounded),
      ),
      findsNothing,
      reason: 'SEARCH-03 criterion 3: a plain title search on an '
          'AI-capable server no longer advertises AI in the prefix badge',
    );
    expect(
      find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byIcon(Icons.menu_book),
      ),
      findsOneWidget,
      reason: 'the Books icon still renders even when the server has AI',
    );
  });

  testWidgets(
      'AI-01/D-03: a Books-tab question reaches the assistant framed while '
      'the bubble would still show the raw text', (tester) async {
    _usePhoneSize(tester);
    final chatNotifier = _FakeAiChatNotifier();
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      authState: _booksWithAiState,
      aiChatNotifier: chatNotifier,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'what should I read next?');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    // Arm the pause detector behind the Ask AI pill; the shimmer border
    // repeats forever while aiReady, so bounded pumps only from here —
    // pumpAndSettle would never return.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    await tester.tap(find.byIcon(Icons.send_rounded));
    await tester.pumpAndSettle();

    expect(
      chatNotifier.sentMessage,
      'what should I read next?',
      reason: 'D-03: the display value the bubble renders carries no '
          'framing — whole-string equality',
    );
    expect(chatNotifier.sentWireContent, isNotNull);
    expect(
      chatNotifier.sentWireContent,
      startsWith('Context: this question was asked from the Books tab'),
    );
    expect(
      chatNotifier.sentWireContent,
      endsWith('what should I read next?'),
      reason: 'the user\'s own words are appended unchanged, never reworded',
    );
    expect(
      find.byType(AiChatScreen),
      findsOneWidget,
      reason: 'AI-01: the assistant still opens with the prompt in flight',
    );
  });

  // ---------------------------------------------------------------------
  // SEARCH-01, author half: the same query returns authors as well as books.
  // ---------------------------------------------------------------------

  /// Types [term] into the shell toolbar on the Books tab and settles.
  Future<_BooksSearchAdapter> searchBooksTab(
    WidgetTester tester, {
    String term = 'le guin',
    bool authorMatches = false,
    bool authorLookupFails = false,
    bool unkeyedAuthor = false,
    bool duplicateAuthorRecords = false,
    bool emptyLookup = false,
    bool authorLibraryFails = false,
    bool emptyAuthorLibrary = false,
  }) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) = await _pumpRouter(
      tester,
      authorMatches: authorMatches,
      authorLookupFails: authorLookupFails,
      unkeyedAuthor: unkeyedAuthor,
      duplicateAuthorRecords: duplicateAuthorRecords,
      emptyLookup: emptyLookup,
      authorLibraryFails: authorLibraryFails,
      emptyAuthorLibrary: emptyAuthorLibrary,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();
    await tester.enterText(
      find.descendant(
        of: find.byType(CantinarrSearchBar),
        matching: find.byType(TextField),
      ),
      term,
    );
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    return adapter;
  }

  testWidgets(
      'SEARCH-01: an author search returns author rows, and they render above '
      'the book rows', (tester) async {
    final adapter = await searchBooksTab(tester, authorMatches: true);

    expect(
      adapter.authorLookupPaths,
      isNotEmpty,
      reason: 'SEARCH-01: the query must actually reach author/lookup',
    );
    expect(find.text('Tracked Author'), findsOneWidget);
    expect(find.text('Metadata Only Author'), findsOneWidget);

    // Authors sit above books on screen. Compare against a book row known to
    // be in this fixture's lookup response.
    final firstAuthorY = tester.getTopLeft(find.text('Tracked Author')).dy;
    final firstBookY = tester.getTopLeft(find.text('Meditations')).dy;
    expect(
      firstAuthorY,
      lessThan(firstBookY),
      reason: 'authors render above books',
    );

    // Both groups are labelled once both kinds are present.
    expect(find.text('AUTHORS'), findsOneWidget);
    expect(find.text('BOOKS'), findsOneWidget);
  });

  testWidgets(
      'an author the library holds sorts above a metadata-only match, and '
      'only the library one claims a count', (tester) async {
    await searchBooksTab(tester, authorMatches: true);

    final trackedY = tester.getTopLeft(find.text('Tracked Author')).dy;
    final metaY = tester.getTopLeft(find.text('Metadata Only Author')).dy;
    expect(
      trackedY,
      lessThan(metaY),
      reason: 'the record the requester can already act on comes first, '
          'even though the fixture returned it second',
    );

    // The count comes from the LIBRARY record, in requester vocabulary, and
    // says what the number counted.
    expect(find.text('Author · 2 of 4 books available'), findsOneWidget);
    // The metadata-only author is named as such and claims no count — never
    // "0 books", which would assert an empty shelf where nothing was counted.
    expect(find.text('Author · not in your library'), findsOneWidget);
    expect(find.textContaining('0 books'), findsNothing);
  });

  testWidgets(
      'an author resolves to their library record even though the lookup and '
      'the library report different provider ids', (tester) async {
    await searchBooksTab(tester, authorMatches: true);

    // The fixture's lookup returns `hc:author-tracked` while the library holds
    // `gr:tracked-library-id` for the same person — the real Chaptarr
    // behaviour (ForeignAuthorId is derived by provider priority) that made
    // author links 404. Resolution is by name, so the row must still open, and
    // must open with the LIBRARY id: that is the only one the server's
    // exact-string search over its own records can find.
    final tile = find.widgetWithText(ListTile, 'Tracked Author');
    expect(
      find.descendant(of: tile, matching: find.byIcon(Icons.chevron_right)),
      findsOneWidget,
      reason: 'a library author is openable',
    );

    await tester.tap(find.text('Tracked Author'));
    await tester.pumpAndSettle();

    expect(
      find.byType(BookSearchResultsView),
      findsNothing,
      reason: 'the author page opened, leaving the search overlay',
    );
  });

  testWidgets(
      'tapping a metadata-only author searches for their books instead of '
      'opening a page that could not show them', (tester) async {
    final adapter = await searchBooksTab(tester, authorMatches: true);
    final lookupsBefore = adapter.bookLookupPaths.length;

    // No chevron: there is no page for an author the library does not hold.
    final tile = find.widgetWithText(ListTile, 'Metadata Only Author');
    expect(
      find.descendant(of: tile, matching: find.byIcon(Icons.chevron_right)),
      findsNothing,
    );
    expect(
      find.descendant(of: tile, matching: find.byIcon(Icons.search)),
      findsOneWidget,
      reason: 'the row advertises a search, not a navigation',
    );
    expect(find.text('Tap to see their books'), findsOneWidget);

    await tester.tap(find.text('Metadata Only Author'));
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    // Still in the overlay, now showing a fresh search for that author.
    expect(find.byType(BookSearchResultsView), findsOneWidget);
    expect(
      tester
          .widget<TextField>(find.descendant(
            of: find.byType(CantinarrSearchBar),
            matching: find.byType(TextField),
          ))
          .controller!
          .text,
      'Metadata Only Author',
      reason: 'the toolbar shows the search that is now running, so the bar '
          'never disagrees with the results under it',
    );
    expect(
      adapter.bookLookupPaths.length,
      greaterThan(lookupsBefore),
      reason: 'a book lookup actually fired for the author name',
    );
  });

  testWidgets(
      'two library authors sharing a name leave the row unopened rather than '
      'guessing which record was meant', (tester) async {
    await searchBooksTab(tester, duplicateAuthorRecords: true);

    expect(
      find.text('Ursula K. Le Guin'),
      findsNWidgets(2),
      reason: 'AGENTS.md: never silently dedupe or merge distinct records',
    );
    // Both library records normalize to the same name key, so neither can be
    // claimed as "the" record for these rows.
    expect(
      find.text('Two authors in your library share this name — open the one '
          'you want from the Authors row'),
      findsNWidgets(2),
    );
    // Scoped to the author rows: the book rows below legitimately keep their
    // own chevrons, so a bare byIcon check would assert nothing useful.
    expect(
      find.descendant(
        of: find.widgetWithText(ListTile, 'Ursula K. Le Guin').first,
        matching: find.byIcon(Icons.chevron_right),
      ),
      findsNothing,
      reason: 'an ambiguous author is not openable',
    );
  });

  testWidgets(
      'when the library author list cannot be read, book results still render '
      'and no author claims to be in the library', (tester) async {
    await searchBooksTab(tester, authorMatches: true, authorLibraryFails: true);

    expect(
      find.text('Meditations'),
      findsOneWidget,
      reason: 'author linking failing must not break the book search',
    );
    // Unreadable library => nothing may claim library membership. Every author
    // falls back to metadata-only, which is the safe direction.
    expect(
      find.text('Author · not in your library'),
      findsNWidgets(2),
      reason: 'both lookup authors fall back to metadata-only',
    );
    expect(find.text('Author · in your library'), findsNothing);
    expect(find.textContaining('books available'), findsNothing);
    expect(
      find.descendant(
        of: find.widgetWithText(ListTile, 'Tracked Author'),
        matching: find.byIcon(Icons.chevron_right),
      ),
      findsNothing,
      reason: 'nothing may be opened as a library author when the library '
          'author list could not be read',
    );
  });

  testWidgets(
      'an author lookup failure keeps the book results and says authors '
      'could not be searched', (tester) async {
    await searchBooksTab(tester, authorLookupFails: true);

    expect(
      find.text('Meditations'),
      findsOneWidget,
      reason: 'a failed author lookup must not throw away usable book rows',
    );
    expect(
      find.text('Authors could not be searched.'),
      findsOneWidget,
      reason: 'absence vs blindness: an author section that could not be '
          'read must not render as one that matched nobody',
    );
    // The book lookup succeeded, so none of the book failure messages apply.
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
  });

  testWidgets(
      'a search that matched no books and no authors says both were searched',
      (tester) async {
    await searchBooksTab(tester, emptyLookup: true);

    expect(overlayText(noBooksMessage), findsOneWidget);
  });

  testWidgets(
      'a library author whose record carries no id stays visible but is not '
      'openable', (tester) async {
    await searchBooksTab(tester, unkeyedAuthor: true);

    expect(
      find.text('Unkeyed Author'),
      findsOneWidget,
      reason: 'a real match is never dropped for lacking an id',
    );
    expect(
      find.text('Ask an admin to check this author\u2019s library record'),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: find.widgetWithText(ListTile, 'Unkeyed Author'),
        matching: find.byIcon(Icons.chevron_right),
      ),
      findsNothing,
      reason: 'no chevron on a row that cannot be opened',
    );
  });
}void _usePhoneSize(WidgetTester tester) {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
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

/// CR-01 fixture: the AvailableServices(chaptarr: true, ai: true) combination
/// that no test in the repo had before this task. With AI available, the
/// shell toolbar's TMDB AI-intent heuristic (`isAiPromptQuery`) and its
/// empty-TMDB auto-escalation are both live — this is the fixture that
/// proves neither can hijack a Books-tab search once dispatch is exclusive.
const _booksWithAiState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, ai: true),
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

/// GAP-SC3 fixture: the books grant is present (so the route stays
/// reachable — app_router.dart:155-165 keys off the grant, not the instance
/// list) but zero Chaptarr instances are configured, with AI also
/// available — proves FAIL-01's message is still reachable at the CR-01
/// fixture combination.
const _noInstanceBooksWithAiState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, ai: true),
    instances: [],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// TAB-02 instance-switch fixture: two Chaptarr instances so
/// `setActiveChaptarrInstance` has a second instance to switch to.
const _twoInstanceBooksState = AuthState(
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
        id: 'books-2',
        serviceType: 'chaptarr',
        name: 'Books 2',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// FAIL-01 fixture: the books grant is present (so the route stays
/// reachable — app_router.dart:155-165 keys off the grant, not the instance
/// list) but zero Chaptarr instances are configured.
const _noInstanceBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Future<
    ({
      ProviderContainer container,
      GoRouter router,
      _BooksSearchAdapter adapter,
    })> _pumpRouter(
  WidgetTester tester, {
  bool mismatchedIdentity = false,
  bool unresolvedIdentity = false,
  bool mixedOwnership = false,
  bool ambiguousLookup = false,
  bool aliasSibling = false,
  bool duplicateLibraryRecords = false,
  bool blankIdentity = false,
  bool forbidden = false,
  bool serverError = false,
  bool emptyLookup = false,
  bool perInstanceBooks = false,
  bool authorMatches = false,
  bool authorLookupFails = false,
  bool unkeyedAuthor = false,
  bool duplicateAuthorRecords = false,
  bool authorLibraryFails = false,
  bool emptyAuthorLibrary = false,
  Stream<WsEvent>? events,
  AuthState? authState,
  AiChatNotifier? aiChatNotifier,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _BooksSearchAdapter(
    mismatchedIdentity: mismatchedIdentity,
    unresolvedIdentity: unresolvedIdentity,
    mixedOwnership: mixedOwnership,
    ambiguousLookup: ambiguousLookup,
    aliasSibling: aliasSibling,
    duplicateLibraryRecords: duplicateLibraryRecords,
    blankIdentity: blankIdentity,
    forbidden: forbidden,
    serverError: serverError,
    emptyLookup: emptyLookup,
    perInstanceBooks: perInstanceBooks,
    authorMatches: authorMatches,
    authorLookupFails: authorLookupFails,
    unkeyedAuthor: unkeyedAuthor,
    duplicateAuthorRecords: duplicateAuthorRecords,
    authorLibraryFails: authorLibraryFails,
    emptyAuthorLibrary: emptyAuthorLibrary,
  );
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(
        () => _FakeAuthNotifier(authState ?? _booksState),
      ),
      backendClientProvider.overrideWithValue(dio),
      realtimeEventsProvider
          .overrideWithValue(events ?? const Stream<WsEvent>.empty()),
      if (aiChatNotifier != null) ...[
        aiChatProvider.overrideWith((ref) {
          ref.keepAlive();
          return aiChatNotifier;
        }),
        // `/assistant` renders AiChatScreen, which reads this provider; the
        // real Codex OAuth flow it drives isn't under test here.
        codexConnectionStatusProvider.overrideWith(
          (_) => const CodexConnectionStatus(
            selected: false,
            available: false,
            connected: false,
          ),
        ),
      ],
    ],
  );

  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await container.pump();
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, router: router, adapter: adapter);
}

/// Captures the arguments a Books-tab AI hand-off passes to
/// [AiChatNotifier.sendMessage] — modelled on
/// `app_shell_test.dart`'s `_FakeAiChatNotifier`.
class _FakeAiChatNotifier extends AiChatNotifier {
  String? sentMessage;
  String? sentWireContent;

  _FakeAiChatNotifier() : super(chatService: AiChatService(backendDio: Dio()));

  @override
  Future<void> sendMessage(String text, {String? wireContent}) async {
    sentMessage = text;
    sentWireContent = wireContent;
  }
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _BooksSearchAdapter implements HttpClientAdapter {
  _BooksSearchAdapter({
    this.mismatchedIdentity = false,
    this.unresolvedIdentity = false,
    this.mixedOwnership = false,
    this.ambiguousLookup = false,
    this.aliasSibling = false,
    this.duplicateLibraryRecords = false,
    this.blankIdentity = false,
    this.forbidden = false,
    this.serverError = false,
    this.emptyLookup = false,
    this.perInstanceBooks = false,
    this.authorMatches = false,
    this.authorLookupFails = false,
    this.unkeyedAuthor = false,
    this.duplicateAuthorRecords = false,
    this.authorLibraryFails = false,
    this.emptyAuthorLibrary = false,
  });

  /// BOOK-07 fixture: book/lookup answers with a title that depends on which
  /// instance the request's `/api/instances/{id}/...` path segment names, so
  /// a test can prove an instance switch discards the old library's rows and
  /// re-runs against the new one, rather than merging or ignoring them.
  final bool perInstanceBooks;

  /// Every `book/lookup` request path served, in order — lets a test assert
  /// exactly how many lookups fired and which instance each one targeted.
  final bookLookupPaths = <String>[];

  /// SEARCH-01: `author/lookup` answers with two authors — one the library
  /// also holds and one metadata-only match.
  final bool authorMatches;

  /// `author/lookup` answers 500 while `book/lookup` still succeeds — the
  /// half-failure that must keep the book rows and say authors could not be
  /// searched, rather than rendering an empty author section.
  final bool authorLookupFails;

  /// A library author record carrying no `foreignAuthorId`: visible, but with
  /// nothing to open it by.
  final bool unkeyedAuthor;

  /// Two distinct library author records whose names normalize to the same key
  /// — neither may be merged away, and neither may be claimed as "the" record.
  final bool duplicateAuthorRecords;

  /// Every `author/lookup` request path served, in order.
  final authorLookupPaths = <String>[];

  /// `GET /author` (the library's own author list) answers 500, so author
  /// linking cannot be resolved. The book search must still work.
  final bool authorLibraryFails;

  /// The library holds no authors at all — every lookup match is metadata-only.
  final bool emptyAuthorLibrary;

  /// Every library-author-list request path served, in order.
  final authorLibraryPaths = <String>[];

  final bool mismatchedIdentity;
  final bool unresolvedIdentity;
  final bool mixedOwnership;
  final bool ambiguousLookup;

  /// FAIL-02 (access): book/lookup answers 403.
  final bool forbidden;

  /// FAIL-02 (retry): book/lookup answers 500.
  final bool serverError;

  /// FAIL-03: book/lookup succeeds with an empty list.
  final bool emptyLookup;

  /// Two lookup rows for one title where the first carries the library's own
  /// foreignBookId and the second is a same-title provider sibling.
  final bool aliasSibling;
  final bool duplicateLibraryRecords;
  final bool blankIdentity;
  int statusRequests = 0;
  int libraryRequests = 0;
  int recentRequests = 0;
  int authorRequests = 0;
  int seriesRequests = 0;

  /// Count of `/api/search` (TMDB multi-search) requests served. Proves the
  /// CR-01 fix: with dispatch exclusive, no Books-tab keystroke should ever
  /// reach this counter.
  int tmdbSearchRequests = 0;
  bool ebookSubmitted = false;
  final statusForeignIds = <String>[];
  final requestBodies = <Map<String, dynamic>>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    if (options.path == '/api/requests' && options.method == 'POST') {
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      final request = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      requestBodies.add(request);
      ebookSubmitted = true;
      body = {
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      };
    } else if (options.path == '/api/requests/book-library') {
      libraryRequests++;
      body = duplicateLibraryRecords
          ? {
              'titles': [
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-a',
                  'ebook': {'monitored': true, 'downloaded': true},
                  'audiobook': {'monitored': false, 'downloaded': false},
                },
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-b',
                  'ebook': {'monitored': false, 'downloaded': false},
                  'audiobook': {'monitored': true, 'downloaded': false},
                },
              ],
            }
          : unresolvedIdentity
              ? {
                  'titles': [
                    {
                      'title': 'Flock',
                      'author': 'Kate Stewart',
                      'foreign_book_id': 'library-flock',
                      'status_known': false,
                      'ebook': {'monitored': false, 'downloaded': false},
                      'audiobook': {'monitored': false, 'downloaded': false},
                    },
                  ],
                }
              : (mismatchedIdentity || ambiguousLookup || aliasSibling)
                  ? {
                      'titles': [
                        {
                          'title': 'Flock',
                          'author': 'Kate Stewart',
                          'foreign_book_id': 'library-flock',
                          'ebook': {
                            'monitored': ebookSubmitted,
                            'downloaded': false,
                          },
                          'audiobook': {'monitored': true, 'downloaded': false},
                        },
                      ],
                    }
                  : mixedOwnership
                      ? {
                          'titles': [
                            {
                              'title': 'Meditations',
                              'author': 'Marcus Aurelius',
                              'foreign_book_id': 'book-1',
                              'ebook': {'monitored': true, 'downloaded': true},
                              'audiobook': {
                                'monitored': true,
                                'downloaded': false
                              },
                            },
                          ],
                        }
                      : {'titles': <Object>[]};
    } else if (options.path == '/api/requests/book-status') {
      statusRequests++;
      final foreignId = options.queryParameters['foreign_id'].toString();
      statusForeignIds.add(foreignId);
      body = unresolvedIdentity && foreignId == 'library-flock'
          ? {
              'status': 'unavailable',
              'book_formats': {
                'ebook': 'unavailable',
                'audiobook': 'unavailable',
              },
            }
          : (mismatchedIdentity || ambiguousLookup) &&
                  foreignId == 'library-flock'
              ? {
                  'status': 'requested',
                  'book_formats': {
                    if (ebookSubmitted) 'ebook': 'requested',
                    'audiobook': 'requested',
                  },
                }
              : foreignId == 'book-1'
                  ? {
                      'status': 'requested',
                      'book_formats': {
                        'ebook': 'requested',
                        'audiobook': 'requested',
                      },
                    }
                  : {'status': 'unavailable'};
    } else if (options.path.endsWith('/api/v1/author')) {
      // The library's OWN author records. Their foreignAuthorId deliberately
      // uses a different provider prefix than author/lookup returns for the
      // same person (`gr:` here vs `hc:` there) — that is the real Chaptarr
      // behaviour that broke author links: `AuthorResource.ForeignAuthorId` is
      // derived by provider priority, so a library record and a fresh metadata
      // search can report different strings for one author. Resolution has to
      // work anyway, by name.
      authorLibraryPaths.add(options.path);
      if (authorLibraryFails) {
        return ResponseBody.fromString('server error', 500);
      }
      body = [
        if (!emptyAuthorLibrary && !unkeyedAuthor && !duplicateAuthorRecords)
          {
            'id': 7,
            'authorName': 'Tracked Author',
            'foreignAuthorId': 'gr:tracked-library-id',
            'statistics': {'bookCount': 4, 'bookFileCount': 2},
          },
        if (duplicateAuthorRecords) ...[
          {
            'id': 41,
            'authorName': 'Ursula K. Le Guin',
            'foreignAuthorId': 'gr:lg-a',
            'statistics': {'bookCount': 2, 'bookFileCount': 2},
          },
          {
            'id': 42,
            'authorName': 'Ursula K Le Guin',
            'foreignAuthorId': 'gr:lg-b',
            'statistics': {'bookCount': 1, 'bookFileCount': 0},
          },
        ],
        if (unkeyedAuthor)
          {
            'id': 51,
            'authorName': 'Unkeyed Author',
            'foreignAuthorId': '',
          },
      ];
    } else if (options.path.endsWith('/api/v1/author/lookup')) {
      authorLookupPaths.add(options.path);
      if (authorLookupFails) {
        return ResponseBody.fromString('server error', 500);
      }
      if (unkeyedAuthor) {
        body = [
          {
            'id': 0,
            'authorName': 'Unkeyed Author',
            'foreignAuthorId': 'hc:unkeyed-meta',
          },
        ];
      } else if (duplicateAuthorRecords) {
        body = [
          {
            'id': 0,
            'authorName': 'Ursula K. Le Guin',
            'foreignAuthorId': 'hc:lg-meta-a',
          },
          {
            'id': 0,
            'authorName': 'Ursula K. Le Guin',
            'foreignAuthorId': 'hc:lg-meta-b',
          },
        ];
      } else if (authorMatches) {
        // Metadata-only FIRST, so a test asserting the library author renders
        // above it proves the sort rather than echoing Chaptarr's order back.
        // Note both carry id 0 and `hc:` ids: a lookup author is a metadata
        // object, never the library's record.
        body = [
          {
            'id': 0,
            'authorName': 'Metadata Only Author',
            'foreignAuthorId': 'hc:author-meta',
          },
          {
            'id': 0,
            'authorName': 'Tracked Author',
            'foreignAuthorId': 'hc:author-tracked',
          },
        ];
      } else {
        body = <Object>[];
      }
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      bookLookupPaths.add(options.path);
      // FAIL-02/FAIL-03 widget cases: these short-circuit with their own
      // status code rather than falling through to the shared 200 return
      // below.
      if (forbidden) {
        return ResponseBody.fromString('forbidden', 403);
      }
      if (serverError) {
        return ResponseBody.fromString('server error', 500);
      }
      if (emptyLookup) {
        return ResponseBody.fromString(
          '[]',
          200,
          headers: {
            'content-type': ['application/json'],
          },
        );
      }
      if (perInstanceBooks) {
        body = options.path.contains('/instances/books-2/')
            ? [
                {
                  'title': 'Second Library Book',
                  'foreignBookId': 'book-2i',
                  'year': 2021,
                  'author': {
                    'id': 0,
                    'authorName': 'Author Two',
                    'foreignAuthorId': 'author-2i',
                  },
                },
              ]
            : [
                {
                  'title': 'First Library Book',
                  'foreignBookId': 'book-1i',
                  'year': 2020,
                  'author': {
                    'id': 0,
                    'authorName': 'Author One',
                    'foreignAuthorId': 'author-1i',
                  },
                },
              ];
      } else {
        body = (mismatchedIdentity ||
              unresolvedIdentity ||
              ambiguousLookup ||
              aliasSibling ||
              duplicateLibraryRecords ||
              blankIdentity)
          ? [
              {
                'title': 'Flock',
                'foreignBookId': blankIdentity
                    ? ''
                    : aliasSibling
                        ? 'library-flock'
                        : 'lookup-flock',
                'year': 2024,
                'author': {
                  'id': 0,
                  'authorName': 'Kate Stewart',
                  'foreignAuthorId': 'author-flock',
                },
              },
              if (ambiguousLookup || aliasSibling)
                {
                  'title': 'Flock',
                  'foreignBookId': 'lookup-flock',
                  'year': 2024,
                  'author': {
                    'id': 0,
                    'authorName': 'Kate Stewart',
                    'foreignAuthorId': 'author-flock',
                  },
                },
            ]
          : [
              {
                'title': 'Meditations',
                'foreignBookId': 'book-1',
                'year': 2002,
                'pageCount': 304,
                'overview': 'A practical guide to Stoic philosophy.',
                'genres': ['Philosophy'],
                'author': {
                  'id': 0,
                  'authorName': 'Marcus Aurelius',
                  'foreignAuthorId': 'author-1',
                },
              },
              {
                'title': 'Letters from a Stoic',
                'foreignBookId': 'book-2',
                'year': 1965,
                'pageCount': 254,
                'overview': 'Seneca on living with wisdom and courage.',
                'genres': ['Philosophy'],
                'author': {
                  'id': 0,
                  'authorName': 'Seneca',
                  'foreignAuthorId': 'author-2',
                },
              },
            ];
      }
    } else if (options.path == '/api/requests/book-recent') {
      recentRequests++;
      body = {'items': <Object>[]};
    } else if (options.path == '/api/requests/book-authors') {
      authorRequests++;
      body = {'authors': <Object>[], 'total': 0};
    } else if (options.path == '/api/requests/book-series') {
      seriesRequests++;
      body = {'series': <Object>[], 'total': 0};
    } else if (options.path == '/api/trakt/anticipated') {
      body = <Object>[];
    } else if (options.path == '/api/search') {
      // Assumption A-05 (dual dispatch) is retired: no TMDB call reaches
      // this route any more. This counter exists to prove that — it must
      // stay 0 across every Books-tab keystroke this file drives.
      tmdbSearchRequests++;
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 1,
        'total_results': 0,
      };
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

  @override
  void close({bool force = false}) {}
}
