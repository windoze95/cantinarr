import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/logic/pending_approvals_provider.dart';
import 'package:cantinarr/features/settings/data/request_settings_service.dart';
import 'package:cantinarr/features/settings/ui/pending_requests_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('blank requester names use safe, trimmed approval copy', () {
    PendingRequestItem item(String username, int requesterCount) =>
        PendingRequestItem.fromJson({
          'username': username,
          'requester_count': requesterCount,
        });

    expect(item('  reader  ', 1).requestedByLabel, 'Requested by reader');
    expect(item('   ', 1).requestedByLabel, 'Requested by a user');
    expect(
      item('', 2).requestedByLabel,
      'Requested by a user and 1 other',
    );
  });

  test('queue rows address the content they were filed for', () {
    PendingRequestItem item(Map<String, dynamic> json) =>
        PendingRequestItem.fromJson(json);

    expect(
      item({'media_type': 'movie', 'tmdb_id': 603}).detailRoute,
      '/detail/movie/603',
    );
    expect(
      item({'media_type': 'tv', 'tmdb_id': 94997}).detailRoute,
      '/detail/tv/94997',
    );

    // Books are addressed by their Chaptarr identity, and the row's own library
    // rides along: an approval can outlive the admin switching drawers.
    final book = item({
      'media_type': 'book',
      'foreign_id': 'edition/OL123',
      'title': 'Flock',
      'instance_id': 'chaptarr-1',
    }).detailRoute;
    expect(book, isNotNull);
    expect(book, startsWith('/detail/book/edition%2FOL123?'));
    final query = Uri.parse(book!).queryParameters;
    expect(query['title'], 'Flock');
    expect(query['instance_id'], 'chaptarr-1');

    // Nothing to open: a legacy book row with no stored identity, and a row
    // whose TMDB id never made it in.
    expect(item({'media_type': 'book', 'title': 'Flock'}).detailRoute, isNull);
    expect(item({'media_type': 'movie', 'tmdb_id': 0}).detailRoute, isNull);
  });

  testWidgets('an approval row shows its artwork and opens the title',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(pending: const [
      {
        'id': 4,
        'user_id': 11,
        'username': 'josie',
        'media_type': 'tv',
        'tmdb_id': 94997,
        'title': 'House of the Dragon',
        'poster_path': '/hotd.jpg',
        'season_scope': 'latest',
      },
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    String? pushedLocation;
    final router = GoRouter(
      routes: [
        GoRoute(path: '/', builder: (_, __) => const PendingRequestsScreen()),
        GoRoute(
          path: '/detail/:type/:id',
          builder: (_, state) {
            pushedLocation = state.uri.toString();
            return const Scaffold(body: SizedBox());
          },
        ),
      ],
    );
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    // The server sends a bare TMDB path; the row composes a thumbnail-width URL.
    final image = tester.widget<CachedImage>(find.byType(CachedImage));
    expect(image.url, 'https://image.tmdb.org/t/p/w185/hotd.jpg');

    await tester.tap(find.text('House of the Dragon'));
    await tester.pumpAndSettle();
    expect(pushedLocation, '/detail/tv/94997');
  });

  testWidgets('a book row keeps its placeholder and stays inert without an id',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(pending: const [
      {
        'id': 5,
        'user_id': 12,
        'username': 'reader',
        'media_type': 'book',
        'title': 'Flock',
        'book_format': 'ebook',
      },
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    // A pending book has no cover to resolve, and without a foreign id there is
    // nothing to open — the row must not offer a dead tap.
    final image = tester.widget<CachedImage>(find.byType(CachedImage));
    expect(image.url, isNull);
    expect(image.icon, Icons.menu_book);
    expect(tester.widget<ListTile>(find.byType(ListTile).first).onTap, isNull);
  });

  testWidgets(
      'empty approvals list keeps its menu visibility control available',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter();
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('No pending requests.'), findsOneWidget);
    final toggle = find.byKey(
      const ValueKey('approvals-conditional-menu-visibility'),
    );
    expect(toggle, findsOneWidget);
    expect(tester.widget<Switch>(toggle).value, isFalse);

    await tester.tap(toggle);
    await tester.pumpAndSettle();

    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isTrue);
    expect(tester.widget<Switch>(toggle).value, isTrue);
  });

  testWidgets(
      'requests the server is retrying get their own section, with no actions',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(
      pending: const [
        {
          'id': 3,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'movie',
          'title': 'Dune',
        },
      ],
      waiting: [
        {
          'id': 112,
          'user_id': 9,
          'username': 'yana',
          'media_type': 'book',
          'title': 'The Body Keeps the Score',
          'book_format': 'ebook',
          'foreign_id': 'gr:40738778',
          'instance_id': 'chaptarr-1',
          'instance_name': 'Yana’s Books',
          'requester_count': 1,
          'wait_reason': 'author_import',
          'requested_at': DateTime.now()
              .subtract(const Duration(hours: 6))
              .toUtc()
              .toIso8601String(),
          'last_attempt_at': DateTime.now()
              .subtract(const Duration(minutes: 3))
              .toUtc()
              .toIso8601String(),
        },
      ],
    );
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Needs approval'), findsOneWidget);
    expect(find.text('Waiting for library'), findsOneWidget);
    expect(find.text('The Body Keeps the Score'), findsOneWidget);
    // The facts an admin asked "where is Yana's book?" actually needs.
    expect(find.text('Library: Yana’s Books'), findsOneWidget);
    expect(find.text('The library is still importing this author'),
        findsOneWidget);
    expect(find.text('Waiting since 6h ago'), findsOneWidget);
    expect(find.text('last tried 3m ago'), findsOneWidget);

    // One approve and one deny — the pending movie's. The waiting row offers
    // neither, because the server refuses an early approval on it.
    expect(find.byIcon(Icons.check_circle_outline), findsOneWidget);
    expect(find.byIcon(Icons.cancel_outlined), findsOneWidget);

    // A badge claims someone must act. Nobody must act on a wait.
    expect(container.read(pendingApprovalsProvider), 1);
  });

  testWidgets('a row whose add already failed says so, and how to fix it',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(pending: const [
      {
        'id': 3,
        'user_id': 2,
        'username': 'reader',
        'media_type': 'movie',
        'title': 'Dune',
      },
      {
        'id': 8,
        'user_id': 9,
        'username': 'yana',
        'media_type': 'book',
        'title': 'A Book The Provider Forgot',
        'book_format': 'ebook',
        'foreign_id': 'ghost-1',
        'add_failure_reason': 'metadata_unresolved',
      },
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    // Both rows are real decisions and keep their buttons — this is not the
    // waiting section. The difference is that one of them stops pretending to
    // be a routine yes/no.
    expect(find.text('Waiting for library'), findsNothing);
    expect(find.byIcon(Icons.check_circle_outline), findsNWidgets(2));
    expect(find.byIcon(Icons.cancel_outlined), findsNWidgets(2));
    expect(container.read(pendingApprovalsProvider), 2);

    expect(find.text('The library couldn’t match this book'), findsOneWidget);
    expect(
      find.textContaining('Add it in the library first, then approve'),
      findsOneWidget,
    );
    // The ordinary decision says nothing extra.
    expect(find.textContaining('The automatic add already failed'), findsNothing);
  });

  testWidgets('an ended author-import wait offers try again, not approve',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    final adapter = _ApprovalsAdapter(pending: const [
      {
        'id': 12,
        'user_id': 9,
        'username': 'yana',
        'media_type': 'book',
        'title': 'The Body Keeps the Score',
        'book_format': 'ebook',
        'foreign_id': 'gr:40738778',
        'add_failure_reason': 'import_failed',
      },
    ]);
    dio.httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(
      find.text('The library gave up importing this author'),
      findsOneWidget,
    );
    expect(find.textContaining('Try again to reopen it'), findsOneWidget);
    // Approving would just replay an add the library refused, so the verb is
    // replaced, not merely discouraged; deny stays as the close.
    expect(find.byIcon(Icons.check_circle_outline), findsNothing);
    expect(find.byIcon(Icons.replay), findsOneWidget);
    expect(find.byIcon(Icons.cancel_outlined), findsOneWidget);

    await tester.tap(find.byIcon(Icons.replay));
    await tester.pumpAndSettle();

    expect(adapter.waitPaths, ['/api/admin/requests/12/wait']);
    expect(find.text('Waiting resumed.'), findsOneWidget);
  });

  test('import-wait reasons carry the try-again verbs', () {
    for (final reason in [
      'import_abandoned',
      'import_failed',
      'import_cancelled',
    ]) {
      final item = PendingRequestItem.fromJson({'add_failure_reason': reason});
      expect(item.isImportWait, isTrue, reason: reason);
      expect(item.addFailure?.action, contains('Try again'), reason: reason);
    }
    // The other failure kind keeps its own instruction, and an unknown reason
    // stays a non-routine row without inventing a wait to resume.
    final unresolved =
        PendingRequestItem.fromJson({'add_failure_reason': 'metadata_unresolved'});
    expect(unresolved.isImportWait, isFalse);
    expect(
      PendingRequestItem.fromJson({'add_failure_reason': 'some_future_reason'})
          .isImportWait,
      isFalse,
    );
  });

  test('an unfamiliar failure reason is still not a routine decision', () {
    // A reason a newer server invents must not silently become a plain yes/no,
    // which is the state this whole surface exists to stop rendering.
    final unknown = PendingRequestItem.fromJson({
      'add_failure_reason': 'some_future_reason',
    });
    expect(unknown.addFailure?.reason, 'The automatic add already failed');
    expect(unknown.addFailure?.action, contains('Check the library first'));

    // And an ordinary row still says nothing.
    expect(PendingRequestItem.fromJson({'title': 'Dune'}).addFailure, isNull);
  });

  testWidgets('an unreadable waiting list says so instead of showing silence',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(
      pending: const [
        {
          'id': 3,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'movie',
          'title': 'Dune',
        },
      ],
      waitingStatusCode: 500,
    );
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    // Blindness, named. An empty section here would say "nothing is waiting",
    // which is the exact answer that let the original defect hide.
    expect(
      find.text('Couldn’t check what the server is retrying. Pull to refresh.'),
      findsOneWidget,
    );
    // And the half of the screen that has buttons still works.
    expect(find.text('Dune'), findsOneWidget);
    expect(find.byIcon(Icons.check_circle_outline), findsOneWidget);
  });

  testWidgets('a server without the waiting endpoint loses nothing else',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(waitingStatusCode: 404);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    // An older server simply has no such section; Approvals reads as it always
    // did rather than reporting an error nobody can act on.
    expect(find.text('No pending requests.'), findsOneWidget);
    expect(find.text('Waiting for library'), findsNothing);
    expect(find.textContaining('Couldn’t check'), findsNothing);
  });

  testWidgets('an unknown book format is visible and cannot be approved',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(pending: [
      {
        'id': 7,
        'user_id': 2,
        'username': 'reader',
        'media_type': 'book',
        'title': 'Flock',
        'book_format': 'future-format',
      },
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Unsupported format'), findsOneWidget);
    final approve = tester.widget<IconButton>(
      find.ancestor(
        of: find.byIcon(Icons.check_circle_outline),
        matching: find.byType(IconButton),
      ),
    );
    expect(approve.onPressed, isNull);
  });

  testWidgets(
      'book approval preserves the requested format and names its library',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final adapter = _ApprovalsAdapter(
      pending: [
        {
          'id': 7,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'both',
          'instance_name': 'Family Books',
          'requester_count': 3,
        },
      ],
      approvalResponse: const {
        'status': 'partial',
        'book_formats': {
          'ebook': 'pending',
          'audiobook': 'requested',
        },
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Library: Family Books'), findsOneWidget);
    expect(find.text('Requested by reader and 2 others'), findsOneWidget);
    await tester.tap(find.byIcon(Icons.check_circle_outline));
    await tester.pumpAndSettle();

    final dialog = find.byType(AlertDialog);
    expect(dialog, findsOneWidget);
    expect(find.descendant(of: dialog, matching: find.text('Requested format')),
        findsOneWidget);
    expect(find.descendant(
            of: dialog, matching: find.text('eBook + Audiobook')),
        findsOneWidget);
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Library: Family Books'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Requested by reader and 2 others'),
      ),
      findsOneWidget,
    );
    expect(find.byType(DropdownButtonFormField<BookRequestFormat>),
        findsNothing);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();

    expect(adapter.approvalBodies, hasLength(1));
    expect(adapter.approvalBodies.single, isEmpty);
    expect(
      find.text('Audiobook approved. eBook still needs attention.'),
      findsOneWidget,
    );
  });

  testWidgets('book approval errors use safe guidance from response JSON',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final adapter = _ApprovalsAdapter(
      pending: const [
        {
          'id': 7,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'ebook',
        },
      ],
      approvalStatusCode: 409,
      approvalResponse: const {
        'error': 'pending book request has no pinned Chaptarr instance',
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    Future<void> approve() async {
      await tester.tap(find.byIcon(Icons.check_circle_outline));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
      await tester.pumpAndSettle();
    }

    await approve();
    expect(
      find.text(
        'This older request doesn’t identify a book library; deny it and ask the requester to submit it again.',
      ),
      findsOneWidget,
    );
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .removeCurrentSnackBar();
    await tester.pumpAndSettle();

    adapter.approvalResponse = {
      'error': 'quality profile selection is ambiguous',
    };
    await approve();
    expect(
      find.text('Check this book library’s paths and profiles, then try again.'),
      findsOneWidget,
    );
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .removeCurrentSnackBar();
    await tester.pumpAndSettle();

    // Approving replayed an add that had already failed the same way. The old
    // generic "Something went wrong. Try again." read as a transient glitch and
    // invited another Approve, which cannot work until the library has the
    // record.
    adapter.approvalResponse = {
      'error':
          'book not found for foreign id — add this book in the library first, then approve',
    };
    await approve();
    expect(
      find.text(
        'The library still can’t find this book. Add it in the library first, '
        'then approve — retrying here won’t help.',
      ),
      findsOneWidget,
    );
    expect(find.text('Something went wrong. Try again.'), findsNothing);
  });
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
      );
}

class _ApprovalsAdapter implements HttpClientAdapter {
  final List<Map<String, dynamic>> pending;
  Map<String, dynamic> approvalResponse;
  final int approvalStatusCode;
  final List<Map<String, dynamic>> approvalBodies = [];

  /// Rows the server is retrying itself, and the status it answers the waiting
  /// endpoint with. 404 stands in for a server that predates the endpoint.
  final List<Map<String, dynamic>> waiting;
  final int waitingStatusCode;

  /// Every POST …/wait the adapter answered, and the body it returns for them.
  final List<String> waitPaths = [];
  Map<String, dynamic> waitResponse = const {'message': 'Waiting resumed.'};

  _ApprovalsAdapter({
    this.pending = const [],
    this.approvalResponse = const {},
    this.approvalStatusCode = 200,
    this.waiting = const [],
    this.waitingStatusCode = 200,
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST' &&
        options.uri.path == '/api/admin/requests/7/approve') {
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      approvalBodies.add(
        bytes.isEmpty
            ? <String, dynamic>{}
            : jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>,
      );
    }
    if (options.method == 'POST' && options.uri.path.endsWith('/wait')) {
      waitPaths.add(options.uri.path);
      return ResponseBody.fromString(
        jsonEncode(waitResponse),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final body = switch (options.uri.path) {
      '/api/admin/requests' => pending,
      '/api/admin/requests/waiting' =>
        waitingStatusCode == 200 ? waiting : const <String, dynamic>{},
      '/api/admin/requests/7/approve' => approvalResponse,
      '/api/admin/request-settings' => {
          'settings': const <String, dynamic>{},
          'radarr_profiles': const <dynamic>[],
          'sonarr_profiles': const <dynamic>[],
        },
      _ => const <String, dynamic>{},
    };
    final statusCode = switch (options.uri.path) {
      '/api/admin/requests/7/approve' => approvalStatusCode,
      '/api/admin/requests/waiting' => waitingStatusCode,
      _ => 200,
    };
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
