import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/chaptarr/data/chaptarr_api_service.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_author_list.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: records every request (method, path, query) so the swipe
/// tests can assert the DELETE and its deleteFiles flag. No GETs are issued.
class _FakeAdapter implements HttpClientAdapter {
  final List<({String method, String path, Map<String, dynamic> query})>
      requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add((
      method: options.method,
      path: options.uri.path,
      query: options.uri.queryParameters,
    ));
    return ResponseBody.fromString(
      jsonEncode(<String, dynamic>{}),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

const _author = ChaptarrAuthor(id: 7, authorName: 'Example Author');

void main() {
  group('ChaptarrAuthorList swipe-to-delete', () {
    late _FakeAdapter adapter;

    Future<void> pumpList(WidgetTester tester) async {
      adapter = _FakeAdapter();
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;
      final service = ChaptarrApiService(backendDio: dio, instanceId: 'inst1');
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ChaptarrAuthorList(
              authors: const [_author],
              onTap: (_) {},
              onDelete: (author, {bool deleteFiles = true}) =>
                  service.deleteAuthor(author.id, deleteFiles: deleteFiles),
            ),
          ),
        ),
      );
    }

    List<({String method, String path, Map<String, dynamic> query})>
        deletes() =>
            adapter.requests.where((r) => r.method == 'DELETE').toList();

    Future<void> swipe(WidgetTester tester) async {
      await tester.drag(find.text('Example Author'), const Offset(-500, 0));
      await tester.pumpAndSettle();
    }

    testWidgets('swiping shows a confirmation with delete-files pre-checked',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      expect(find.text('Delete Author'), findsOneWidget);
      expect(find.text('Also delete files from disk'), findsOneWidget);
      final box = tester.widget<CheckboxListTile>(find.byType(CheckboxListTile));
      expect(box.value, isTrue);
    });

    testWidgets('cancelling sends no DELETE and keeps the tile',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(deletes(), isEmpty);
      expect(find.text('Example Author'), findsOneWidget);
    });

    testWidgets('confirming with the default deletes files from disk',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();

      final d = deletes();
      expect(d, hasLength(1));
      expect(d.single.path, endsWith('/author/7'));
      expect(d.single.query['deleteFiles'], 'true');
    });

    testWidgets('unchecking the box confirms without deleting files',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      await tester.tap(find.text('Also delete files from disk'));
      await tester.pump();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();

      final d = deletes();
      expect(d, hasLength(1));
      expect(d.single.path, endsWith('/author/7'));
      expect(d.single.query['deleteFiles'], 'false');
    });
  });
}
