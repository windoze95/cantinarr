import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_book_detail_sheet.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/book_link_chips.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The Chaptarr book sheet's outbound chips: Goodreads and Open Library from
/// the ids the record carries, Hardcover only when Chaptarr declares its page,
/// and no chips at all for a record that names no outside page.
void main() {
  testWidgets('a record with provider ids shows Goodreads and Open Library',
      (tester) async {
    await _openSheet(
      tester,
      _record(
        goodreadsBookId: 'gr:5907',
        openLibraryWorkId: 'ol:OL262758W',
        hardcoverBookId: 'hc:12345',
      ),
    );

    final sheet = find.byType(BottomSheet);
    final chips = tester.widgetList<ActionChip>(
      find.descendant(of: sheet, matching: find.byType(ActionChip)),
    );
    // In this order, and with no Hardcover chip: a bare Hardcover id is not a
    // page, only a declared link is.
    expect(
      chips.map((chip) => (chip.label as Text).data).toList(),
      ['Goodreads', 'Open Library'],
    );
    expect(find.byTooltip('Open on Goodreads'), findsOneWidget);
    expect(find.byTooltip('Open on Open Library'), findsOneWidget);
  });

  testWidgets('a declared hardcover.app link adds the Hardcover chip',
      (tester) async {
    await _openSheet(
      tester,
      _record(
        goodreadsBookId: 'gr:5907',
        links: [
          {'name': 'Hardcover', 'url': 'https://hardcover.app/books/ahsoka'},
        ],
      ),
    );

    expect(find.widgetWithText(ActionChip, 'Goodreads'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Hardcover'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Open Library'), findsNothing);
  });

  testWidgets('a record without ids shows no chips', (tester) async {
    await _openSheet(tester, _record());

    expect(find.byType(BookLinkChips), findsNothing);
    expect(find.byType(ActionChip), findsNothing);
    // The rest of the sheet is untouched.
    expect(find.text('Ahsoka'), findsOneWidget);
    expect(find.text('History'), findsOneWidget);
  });
}

Map<String, dynamic> _record({
  String? goodreadsBookId,
  String? openLibraryWorkId,
  String? hardcoverBookId,
  List<Map<String, String>> links = const [],
}) =>
    {
      'id': 42,
      'title': 'Ahsoka',
      'authorId': 7,
      'foreignBookId': '29749107',
      'mediaType': 'ebook',
      'monitored': true,
      'overview': 'A former Jedi searches for a new path.',
      'statistics': {'bookCount': 1, 'bookFileCount': 0},
      if (goodreadsBookId != null) 'goodreadsBookId': goodreadsBookId,
      if (openLibraryWorkId != null) 'openLibraryWorkId': openLibraryWorkId,
      if (hardcoverBookId != null) 'hardcoverBookId': hardcoverBookId,
      'links': links,
    };

Future<void> _openSheet(
  WidgetTester tester,
  Map<String, dynamic> record,
) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _FakeAdapter(record);
  addTearDown(() => dio.close(force: true));

  await tester.pumpWidget(ProviderScope(
    overrides: [backendClientProvider.overrideWithValue(dio)],
    child: MaterialApp(
      home: Scaffold(
        body: Builder(
          builder: (context) => Center(
            child: TextButton(
              onPressed: () => showChaptarrBookDetailSheet(
                context,
                instanceId: 'inst1',
                records: [ChaptarrBook.fromJson(record)],
              ),
              child: const Text('Open sheet'),
            ),
          ),
        ),
      ),
    ),
  ));
  await tester.tap(find.text('Open sheet'));
  await tester.pumpAndSettle();
}

/// Answers the sheet's refresh of the record with the same record, and its
/// history read with nothing.
class _FakeAdapter implements HttpClientAdapter {
  final Map<String, dynamic> record;

  _FakeAdapter(this.record);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    final Object body;
    if (path.endsWith('/book/${record['id']}')) {
      body = record;
    } else if (path.endsWith('/history')) {
      body = <Object>[];
    } else {
      body = <String, dynamic>{};
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
