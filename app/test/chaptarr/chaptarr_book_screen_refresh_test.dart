import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_book_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({
    List<Map<String, dynamic>>? records,
    this.failingHistoryBookIds = const {},
  }) : books = records ?? _defaultBooks();

  final List<RequestOptions> requests = [];
  final List<dynamic> requestBodies = [];
  final Set<int> failingHistoryBookIds;

  final List<Map<String, dynamic>> books;

  static List<Map<String, dynamic>> _defaultBooks() => [
    {
      'id': 1,
      'title': 'Flock',
      'authorId': 7,
      'foreignBookId': 'flock',
      'mediaType': 'ebook',
      'monitored': true,
      'overview': '<b>Can you keep a secret?</b><br/>Second line &amp; more',
      'statistics': {'bookCount': 1, 'bookFileCount': 0},
    },
    {
      'id': 2,
      'title': 'Flock',
      'authorId': 7,
      'foreignBookId': 'flock',
      'mediaType': 'audiobook',
      'monitored': true,
      'statistics': {'bookCount': 1, 'bookFileCount': 0},
    },
  ];

  ResponseBody _response(dynamic body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic requestBody;
    if (requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      if (bytes.isNotEmpty) requestBody = jsonDecode(utf8.decode(bytes));
    }
    requests.add(options);
    requestBodies.add(requestBody);
    final path = options.uri.path;
    if (path.endsWith('/book')) return _response(books);
    if (path.endsWith('/bookfile')) return _response([]);
    for (final book in books) {
      if (path.endsWith('/book/${book['id']}')) return _response(book);
    }
    if (path.endsWith('/release')) return _response([]);
    if (path.endsWith('/history')) {
      final bookId = options.uri.queryParameters['bookId'];
      if (failingHistoryBookIds.contains(int.parse(bookId!))) {
        return ResponseBody.fromString('{}', 500);
      }
      return _response({
        'records': [
          {
            'id': bookId == '1' ? 101 : 102,
            'bookId': int.parse(bookId),
            'sourceTitle': bookId == '1' ? 'Flock EPUB' : 'Flock M4B',
            'eventType': 'grabbed',
            'date': '2026-07-21T12:00:00Z',
          }
        ]
      });
    }
    return _response({});
  }

  List<Map<String, dynamic>> get commandBodies => [
        for (var i = 0; i < requests.length; i++)
          if (requests[i].uri.path.endsWith('/command'))
            Map<String, dynamic>.from(requestBodies[i] as Map),
      ];

  @override
  void close({bool force = false}) {}
}

Future<void> _pumpScreen(
  WidgetTester tester,
  _FakeAdapter adapter,
) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  addTearDown(() => dio.close(force: true));
  final stale = adapter.books
      .map((book) => ChaptarrBook.fromJson({...book, 'monitored': false}))
      .toList();

  await tester.pumpWidget(ProviderScope(
    overrides: [backendClientProvider.overrideWithValue(dio)],
    child: MaterialApp(
      home: ChaptarrBookScreen(
        instanceId: 'inst1',
        records: stale,
      ),
    ),
  ));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('refreshes live records and loads history for both formats',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpScreen(tester, adapter);

    expect(find.text('eBook: Requested • Audiobook: Requested'),
        findsOneWidget);
    expect(
      adapter.requests.where((request) => request.path.endsWith('/book')),
      hasLength(1),
    );

    await tester.tap(find.text('eBook: Requested • Audiobook: Requested'));
    await tester.pumpAndSettle();

    final historyBookIds = adapter.requests
        .where((request) => request.path.endsWith('/history'))
        .map((request) => request.queryParameters['bookId'])
        .toSet();
    expect(historyBookIds, {1, 2});
    expect(find.text('Flock EPUB'), findsOneWidget);
    expect(find.text('Flock M4B'), findsOneWidget);
    expect(find.text('Can you keep a secret?\n\nSecond line & more'),
        findsOneWidget);
    expect(find.textContaining('<b>'), findsNothing);
  });

  testWidgets('keeps fresh status and successful history after one failure',
      (tester) async {
    final adapter = _FakeAdapter(failingHistoryBookIds: {2});
    await _pumpScreen(tester, adapter);

    await tester.tap(find.text('eBook: Requested • Audiobook: Requested'));
    await tester.pumpAndSettle();

    final sheet = find.byType(BottomSheet);
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('eBook: Requested'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('Audiobook: Requested'),
      ),
      findsOneWidget,
    );
    expect(find.text('Flock EPUB'), findsOneWidget);
    expect(find.text('Flock M4B'), findsNothing);
    expect(find.text('Could not load Audiobook history.'), findsOneWidget);
    expect(find.text('No history yet.'), findsNothing);
  });

  testWidgets('detail only offers status for formats its actions can target',
      (tester) async {
    final audiobook = _FakeAdapter._defaultBooks()[1];
    final adapter = _FakeAdapter(records: [audiobook]);
    await _pumpScreen(tester, adapter);

    await tester
        .tap(find.text('eBook: Not requested • Audiobook: Requested'));
    await tester.pumpAndSettle();

    final sheet = find.byType(BottomSheet);
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('Audiobook: Requested'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('eBook: Not requested'),
      ),
      findsNothing,
    );
  });

  testWidgets('unknown sibling stays visible in header and detail status',
      (tester) async {
    final adapter = _FakeAdapter(records: [
      {..._FakeAdapter._defaultBooks()[0], 'id': 11},
      {
        ..._FakeAdapter._defaultBooks()[1],
        'id': 22,
        'mediaType': 'future-format',
      },
    ]);
    await _pumpScreen(tester, adapter);

    final grouped = find.text(
      'Book format: Needs attention • eBook: Requested',
    );
    expect(grouped, findsOneWidget);
    await tester.tap(grouped);
    await tester.pumpAndSettle();

    final sheet = find.byType(BottomSheet);
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('Book format: Needs attention'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: sheet,
        matching: find.text('eBook: Requested'),
      ),
      findsOneWidget,
    );
  });

  testWidgets('screen automatic search sends every duplicate format ID',
      (tester) async {
    final adapter = _FakeAdapter(records: [
      {..._FakeAdapter._defaultBooks()[0], 'id': 11},
      {..._FakeAdapter._defaultBooks()[0], 'id': 12},
      {..._FakeAdapter._defaultBooks()[1], 'id': 21},
    ]);
    await _pumpScreen(tester, adapter);

    await tester.tap(find.text('Find automatically'));
    await tester.pumpAndSettle();
    final ebookChoice = find.descendant(
      of: find.byType(BottomSheet),
      matching: find.text('eBook'),
    );
    expect(ebookChoice, findsOneWidget);
    expect(find.text('2 records'), findsOneWidget);
    await tester.tap(ebookChoice);
    await tester.pumpAndSettle();

    expect(adapter.commandBodies, [
      {'name': 'BookSearch', 'bookIds': [11, 12]},
    ]);
  });

  testWidgets('detail automatic search sends every duplicate format ID',
      (tester) async {
    final adapter = _FakeAdapter(records: [
      {..._FakeAdapter._defaultBooks()[0], 'id': 11},
      {..._FakeAdapter._defaultBooks()[0], 'id': 12},
      {..._FakeAdapter._defaultBooks()[1], 'id': 21},
    ]);
    await _pumpScreen(tester, adapter);

    await tester.tap(find.text('eBook: Requested • Audiobook: Requested'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Find automatically').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook').last);
    await tester.pumpAndSettle();

    expect(adapter.commandBodies, [
      {'name': 'BookSearch', 'bookIds': [11, 12]},
    ]);
  });

  testWidgets('screen interactive search chooses one duplicate record ID',
      (tester) async {
    final adapter = _FakeAdapter(records: [
      {..._FakeAdapter._defaultBooks()[0], 'id': 11},
      {..._FakeAdapter._defaultBooks()[0], 'id': 12},
      {..._FakeAdapter._defaultBooks()[1], 'id': 21},
    ]);
    await _pumpScreen(tester, adapter);

    await tester.tap(find.text('Choose a download'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Record #12'));
    await tester.pumpAndSettle();

    final releaseRequest = adapter.requests.singleWhere(
      (request) => request.uri.path.endsWith('/release'),
    );
    expect(releaseRequest.uri.queryParameters['bookId'], '12');
  });

  testWidgets('detail interactive search chooses one duplicate record ID',
      (tester) async {
    final adapter = _FakeAdapter(records: [
      {..._FakeAdapter._defaultBooks()[0], 'id': 11},
      {..._FakeAdapter._defaultBooks()[0], 'id': 12},
      {..._FakeAdapter._defaultBooks()[1], 'id': 21},
    ]);
    await _pumpScreen(tester, adapter);

    await tester.tap(find.text('eBook: Requested • Audiobook: Requested'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose a download').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Record #11'));
    await tester.pumpAndSettle();

    final releaseRequest = adapter.requests.singleWhere(
      (request) => request.uri.path.endsWith('/release'),
    );
    expect(releaseRequest.uri.queryParameters['bookId'], '11');
  });
}
