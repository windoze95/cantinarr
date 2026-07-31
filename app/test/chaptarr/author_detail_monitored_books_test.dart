import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_author_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

typedef _Request = ({
  String method,
  String path,
  Map<String, dynamic> query,
  dynamic body,
});

/// Serves an author and its books through the same proxied paths used by the
/// real detail screen, while recording monitor updates for interaction tests.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(
    List<Map<String, dynamic>> books, {
    this.deferMonitorPuts = false,
  }) : books = books.map(Map<String, dynamic>.from).toList();

  final List<Map<String, dynamic>> books;
  final bool deferMonitorPuts;
  final List<_Request> requests = [];
  final List<({
    Map<String, dynamic> body,
    Completer<ResponseBody> response,
  })> pendingMonitorPuts = [];

  ResponseBody _response(dynamic body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  void _applyMonitorUpdate(Map<String, dynamic> body) {
    final ids = (body['bookIds'] as List<dynamic>).cast<int>().toSet();
    final monitored = body['monitored'] as bool;
    for (final book in books) {
      if (ids.contains(book['id'])) book['monitored'] = monitored;
    }
  }

  void completeMonitorPut(int index) {
    final pending = pendingMonitorPuts[index];
    _applyMonitorUpdate(pending.body);
    pending.response.complete(_response(<String, dynamic>{}));
  }

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic requestBody;
    if (requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      if (bytes.isNotEmpty) {
        requestBody = jsonDecode(utf8.decode(bytes));
      }
    }

    final path = options.uri.path;
    requests.add((
      method: options.method,
      path: path,
      query: Map<String, dynamic>.from(options.uri.queryParameters),
      body: requestBody,
    ));

    dynamic response = <String, dynamic>{};
    if (options.method == 'GET' && path.endsWith('/author/7')) {
      response = {
        'id': 7,
        'authorName': 'Marcus Aurelius',
        'foreignAuthorId': 'author-7',
        'path': '/library/authors/marcus',
        'qualityProfileId': 31,
        'metadataProfileId': 41,
        'statistics': {
          'bookCount': books.length,
          'bookFileCount': 0,
        },
      };
    } else if (options.method == 'GET' && path.endsWith('/book')) {
      response = books;
    } else if (options.method == 'PUT' && path.endsWith('/book/monitor')) {
      // Mirror the successful server update so this fixture remains correct if
      // the screen ever chooses to reload after a toggle.
      final body = requestBody as Map<String, dynamic>;
      if (deferMonitorPuts) {
        final completer = Completer<ResponseBody>();
        pendingMonitorPuts.add((body: body, response: completer));
        return completer.future;
      }
      _applyMonitorUpdate(body);
    }

    return _response(response);
  }

  @override
  void close({bool force = false}) {}
}

Map<String, dynamic> _book({
  required int id,
  required String title,
  required String groupKey,
  required DateTime releaseDate,
  required bool monitored,
  String mediaType = 'ebook',
  bool blankForeignBookId = false,
}) =>
    {
      'id': id,
      'title': title,
      'authorId': 7,
      'foreignBookId': blankForeignBookId ? '' : groupKey,
      'releaseDate': releaseDate.toIso8601String(),
      'monitored': monitored,
      'mediaType': mediaType,
      'statistics': {
        'bookCount': 1,
        'bookFileCount': 0,
      },
    };

Future<_FakeAdapter> _pumpDetail(
  WidgetTester tester,
  List<Map<String, dynamic>> books, {
  bool deferMonitorPuts = false,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final adapter = _FakeAdapter(books, deferMonitorPuts: deferMonitorPuts);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  addTearDown(() => dio.close(force: true));

  await tester.pumpWidget(
    ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: const MaterialApp(
        home: ChaptarrAuthorDetailScreen(
          instanceId: 'inst1',
          authorId: 7,
          authorName: 'Marcus Aurelius',
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();

  expect(
    adapter.requests.where((request) =>
        request.method == 'GET' && request.path.endsWith('/author/7')),
    hasLength(1),
  );
  final bookGet = adapter.requests.singleWhere((request) =>
      request.method == 'GET' && request.path.endsWith('/book'));
  expect(bookGet.query['authorId'], '7');
  return adapter;
}

Finder _card(String groupKey) => find.byKey(ValueKey('book:$groupKey'));

Finder _formatControl(String groupKey, String tooltip) => find.descendant(
      of: _card(groupKey),
      matching: find.byTooltip(tooltip),
    );

Finder _formatControlTapTarget(String groupKey, String tooltip) =>
    find.descendant(
      of: _formatControl(groupKey, tooltip),
      matching: find.byType(InkWell),
    );

void _invokeFormatControl(
  WidgetTester tester,
  String groupKey,
  String tooltip,
) {
  final control = tester.widget<InkWell>(
      _formatControlTapTarget(groupKey, tooltip));
  expect(control.onTap, isNotNull);
  control.onTap!();
}

void _expectAbove(WidgetTester tester, Finder upper, Finder lower) {
  expect(
    tester.getTopLeft(upper).dy,
    lessThan(tester.getTopLeft(lower).dy),
  );
}

Future<void> _waitForPendingMonitorPuts(
  WidgetTester tester,
  _FakeAdapter adapter,
  int count,
) async {
  for (var attempt = 0;
      attempt < 100 && adapter.pendingMonitorPuts.length < count;
      attempt++) {
    await tester.pump(const Duration(milliseconds: 1));
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 1));
    });
  }
  expect(adapter.pendingMonitorPuts, hasLength(count),
      reason: 'requests received: ${adapter.requests}');
}

void main() {
  group('ChaptarrAuthorDetailScreen monitored books', () {
    testWidgets(
        'promotes monitored titles and keeps release-date order in both groups',
        (tester) async {
      await _pumpDetail(tester, [
        _book(
          id: 1,
          title: 'Oldest other',
          groupKey: 'other-oldest',
          releaseDate: DateTime.utc(2001),
          monitored: false,
        ),
        _book(
          id: 2,
          title: 'Older monitored',
          groupKey: 'monitored-older',
          releaseDate: DateTime.utc(2005),
          monitored: true,
        ),
        _book(
          id: 3,
          title: 'Newest other',
          groupKey: 'other-newest',
          releaseDate: DateTime.utc(2025),
          monitored: false,
        ),
        _book(
          id: 4,
          title: 'Newer monitored',
          groupKey: 'monitored-newer',
          releaseDate: DateTime.utc(2020),
          monitored: true,
        ),
      ]);

      final monitoredHeading = find.text('Tracked books');
      final otherHeading = find.text('Other books');
      expect(monitoredHeading, findsOneWidget);
      expect(otherHeading, findsOneWidget);
      for (final key in [
        'monitored-newer',
        'monitored-older',
        'other-newest',
        'other-oldest',
      ]) {
        expect(_card(key), findsOneWidget);
      }

      _expectAbove(tester, monitoredHeading, _card('monitored-newer'));
      _expectAbove(
          tester, _card('monitored-newer'), _card('monitored-older'));
      _expectAbove(tester, _card('monitored-older'), otherHeading);
      _expectAbove(tester, otherHeading, _card('other-newest'));
      _expectAbove(tester, _card('other-newest'), _card('other-oldest'));
    });

    testWidgets('one monitored format promotes one combined title card',
        (tester) async {
      await _pumpDetail(tester, [
        _book(
          id: 10,
          title: 'Combined title',
          groupKey: 'combined',
          releaseDate: DateTime.utc(2010),
          monitored: false,
        ),
        _book(
          id: 11,
          title: 'Combined title',
          groupKey: 'combined',
          releaseDate: DateTime.utc(2010),
          monitored: true,
          mediaType: 'audiobook',
        ),
        _book(
          id: 12,
          title: 'Newer unmonitored title',
          groupKey: 'newer-other',
          releaseDate: DateTime.utc(2025),
          monitored: false,
        ),
      ]);

      expect(find.text('Combined title'), findsOneWidget);
      expect(_card('combined'), findsOneWidget);
      expect(
          _formatControl('combined', 'Track eBook'), findsOneWidget);
      expect(_formatControl('combined', 'Stop tracking Audiobook'),
          findsOneWidget);
      final controlSize = tester.getSize(_formatControlTapTarget(
          'combined', 'Stop tracking Audiobook'));
      expect(controlSize.width, greaterThanOrEqualTo(48));
      expect(controlSize.height, greaterThanOrEqualTo(48));
      _expectAbove(tester, find.text('Tracked books'), _card('combined'));
      _expectAbove(tester, _card('combined'), find.text('Other books'));
      _expectAbove(tester, find.text('Other books'), _card('newer-other'));
    });

    testWidgets('zero monitored books keeps the plain newest-first catalog',
        (tester) async {
      await _pumpDetail(tester, [
        _book(
          id: 20,
          title: 'Old title',
          groupKey: 'old',
          releaseDate: DateTime.utc(2000),
          monitored: false,
        ),
        _book(
          id: 21,
          title: 'Newest title',
          groupKey: 'newest',
          releaseDate: DateTime.utc(2025),
          monitored: false,
        ),
        _book(
          id: 22,
          title: 'Middle title',
          groupKey: 'middle',
          releaseDate: DateTime.utc(2015),
          monitored: false,
        ),
      ]);

      expect(find.text('Tracked books'), findsNothing);
      expect(find.text('Other books'), findsNothing);
      expect(_card('newest'), findsOneWidget);
      expect(_card('middle'), findsOneWidget);
      expect(_card('old'), findsOneWidget);
      _expectAbove(tester, _card('newest'), _card('middle'));
      _expectAbove(tester, _card('middle'), _card('old'));
    });

    testWidgets('successful unmonitor relocates the card and records the PUT',
        (tester) async {
      final adapter = await _pumpDetail(tester, [
        _book(
          id: 30,
          title: 'Old monitored title',
          groupKey: 'old-monitored',
          releaseDate: DateTime.utc(2000),
          monitored: true,
        ),
        _book(
          id: 31,
          title: 'New unmonitored title',
          groupKey: 'new-unmonitored',
          releaseDate: DateTime.utc(2025),
          monitored: false,
        ),
      ]);

      _expectAbove(
          tester, _card('old-monitored'), _card('new-unmonitored'));
      await tester.tap(
          _formatControl('old-monitored', 'Stop tracking eBook'));
      await tester.pumpAndSettle();

      final puts = adapter.requests.where((request) =>
          request.method == 'PUT' &&
          request.path.endsWith('/book/monitor'));
      expect(puts, hasLength(1));
      expect(puts.single.body, {
        'bookIds': [30],
        'monitored': false,
      });
      expect(find.text('Tracked books'), findsNothing);
      expect(find.text('Other books'), findsNothing);
      expect(_formatControl('old-monitored', 'Track eBook'),
          findsOneWidget);
      _expectAbove(
          tester, _card('new-unmonitored'), _card('old-monitored'));
      expect(find.byType(SnackBar), findsOneWidget);
    });

    testWidgets(
        'one format control reduces duplicate records and toggles them together',
        (tester) async {
      final adapter = await _pumpDetail(tester, [
        _book(
          id: 50,
          title: 'Duplicate eBook',
          groupKey: 'duplicate-ebook',
          releaseDate: DateTime.utc(2020),
          monitored: false,
        ),
        _book(
          id: 51,
          title: 'Duplicate eBook',
          groupKey: 'duplicate-ebook',
          releaseDate: DateTime.utc(2020),
          monitored: true,
        ),
      ]);

      expect(
        _formatControl('duplicate-ebook', 'Stop tracking eBook'),
        findsOneWidget,
      );
      await tester.tap(
        _formatControl('duplicate-ebook', 'Stop tracking eBook'),
      );
      await tester.pumpAndSettle();

      final puts = adapter.requests.where((request) =>
          request.method == 'PUT' &&
          request.path.endsWith('/book/monitor'));
      expect(puts, hasLength(1));
      final body = puts.single.body as Map<String, dynamic>;
      expect((body['bookIds'] as List<dynamic>).toSet(), {50, 51});
      expect(body['monitored'], isFalse);
      expect(
        _formatControl('duplicate-ebook', 'Track eBook'),
        findsOneWidget,
      );
    });

    testWidgets('adding a format reuses the existing author configuration',
        (tester) async {
      final adapter = await _pumpDetail(tester, [
        _book(
          id: 60,
          title: 'Configured title',
          groupKey: 'configured-title',
          releaseDate: DateTime.utc(2020),
          monitored: true,
        ),
      ]);

      await tester.tap(
        _formatControl('configured-title', 'Track Audiobook'),
      );
      await tester.pumpAndSettle();

      final add = adapter.requests.singleWhere((request) =>
          request.method == 'POST' && request.path.endsWith('/book'));
      final author =
          (add.body as Map<String, dynamic>)['author'] as Map<String, dynamic>;
      expect(author['qualityProfileId'], 31);
      expect(author['metadataProfileId'], 41);
      expect(author['rootFolderPath'], '/library/authors/marcus');
      expect(
        adapter.requests.where((request) =>
            request.path.endsWith('/qualityprofile') ||
            request.path.endsWith('/metadataprofile') ||
            request.path.endsWith('/rootfolder')),
        isEmpty,
      );
    });

    testWidgets('unknown format blocks both missing-format add controls',
        (tester) async {
      final adapter = await _pumpDetail(tester, [
        _book(
          id: 70,
          title: 'Unclassified title',
          groupKey: 'unclassified-title',
          releaseDate: DateTime.utc(2020),
          monitored: true,
          mediaType: 'future-format',
        ),
      ]);

      expect(find.text('Book format: Needs attention'), findsOneWidget);
      final blocked = find.descendant(
        of: _card('unclassified-title'),
        matching: find.byTooltip('Fix unknown book format in Chaptarr first'),
      );
      expect(blocked, findsNWidgets(2));
      for (final inkWell in tester.widgetList<InkWell>(
        find.descendant(of: blocked, matching: find.byType(InkWell)),
      )) {
        expect(inkWell.onTap, isNull);
      }
      expect(
        adapter.requests.where((request) => request.method == 'POST'),
        isEmpty,
      );
    });

    testWidgets('blank metadata ID blocks only missing-format creation',
        (tester) async {
      final adapter = await _pumpDetail(tester, [
        _book(
          id: 80,
          title: 'Unmatched title',
          groupKey: 'unused',
          releaseDate: DateTime.utc(2020),
          monitored: true,
          blankForeignBookId: true,
        ),
      ]);

      final card = _card('id:80');
      final existing = find.descendant(
        of: card,
        matching: find.byTooltip('Stop tracking eBook'),
      );
      expect(existing, findsOneWidget);
      expect(
        tester.widget<InkWell>(
          find.descendant(of: existing, matching: find.byType(InkWell)),
        ).onTap,
        isNotNull,
      );

      final blocked = find.descendant(
        of: card,
        matching: find.byTooltip(
          'This book has no metadata ID. Fix it in Chaptarr before adding Audiobook',
        ),
      );
      expect(blocked, findsOneWidget);
      expect(
        tester.widget<InkWell>(
          find.descendant(of: blocked, matching: find.byType(InkWell)),
        ).onTap,
        isNull,
      );
      expect(
        find.descendant(
          of: blocked,
          matching: find.byIcon(Icons.warning_amber_rounded),
        ),
        findsOneWidget,
      );
      expect(
        adapter.requests.where((request) => request.method == 'POST'),
        isEmpty,
      );
    });

    testWidgets(
        'overlapping format toggles only claim a move on the response that moves',
        (tester) async {
      final adapter = await _pumpDetail(
        tester,
        [
          _book(
            id: 40,
            title: 'Combined title',
            groupKey: 'combined-overlap',
            releaseDate: DateTime.utc(2000),
            monitored: true,
          ),
          _book(
            id: 41,
            title: 'Combined title',
            groupKey: 'combined-overlap',
            releaseDate: DateTime.utc(2000),
            monitored: true,
            mediaType: 'audiobook',
          ),
          _book(
            id: 42,
            title: 'New unmonitored title',
            groupKey: 'new-other-overlap',
            releaseDate: DateTime.utc(2025),
            monitored: false,
          ),
        ],
        deferMonitorPuts: true,
      );

      _invokeFormatControl(
          tester, 'combined-overlap', 'Stop tracking eBook');
      await tester.pump();
      await _waitForPendingMonitorPuts(tester, adapter, 1);
      _invokeFormatControl(
          tester, 'combined-overlap', 'Stop tracking Audiobook');
      await tester.pump();
      await _waitForPendingMonitorPuts(tester, adapter, 2);

      // Both decisions were made against the same initial two-format state;
      // neither response has updated the local model yet.
      expect(
        adapter.pendingMonitorPuts.map((pending) => pending.body),
        [
          {
            'bookIds': [40],
            'monitored': false,
          },
          {
            'bookIds': [41],
            'monitored': false,
          },
        ],
      );

      adapter.completeMonitorPut(0);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      // The audiobook is still monitored, so the first successful response
      // changes one format without moving the combined title.
      expect(find.text('Tracked books'), findsOneWidget);
      expect(find.text('Stopped tracking eBook for Combined title'),
          findsOneWidget);
      expect(find.text('Combined title moved out of Tracked books'),
          findsNothing);
      _expectAbove(tester, _card('combined-overlap'),
          _card('new-other-overlap'));

      tester
          .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
          .removeCurrentSnackBar();
      await tester.pump();

      adapter.completeMonitorPut(1);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      // This response clears the group's last monitored format, so it is the
      // only one whose success message may claim a section move.
      expect(find.text('Combined title moved out of Tracked books'),
          findsOneWidget);
      expect(find.text('Tracked books'), findsNothing);
      expect(find.text('Other books'), findsNothing);
      _expectAbove(tester, _card('new-other-overlap'),
          _card('combined-overlap'));
    });
  });
}
