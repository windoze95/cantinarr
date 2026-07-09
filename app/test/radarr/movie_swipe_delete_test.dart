import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/radarr/data/radarr_api_service.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_list.dart';
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

const _movie = RadarrMovie(id: 7, title: 'Example Movie', year: 2020);

void main() {
  group('RadarrMovieList swipe-to-delete', () {
    late _FakeAdapter adapter;

    Future<void> pumpList(WidgetTester tester) async {
      adapter = _FakeAdapter();
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;
      final service = RadarrApiService(backendDio: dio, instanceId: 'inst1');
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: RadarrMovieList(
              movies: const [_movie],
              onDelete: (id, {bool deleteFiles = true}) =>
                  service.deleteMovie(id, deleteFiles: deleteFiles),
              onSearch: (_) {},
            ),
          ),
        ),
      );
    }

    List<({String method, String path, Map<String, dynamic> query})>
        deletes() =>
            adapter.requests.where((r) => r.method == 'DELETE').toList();

    Future<void> swipe(WidgetTester tester) async {
      await tester.drag(find.text('Example Movie'), const Offset(-500, 0));
      await tester.pumpAndSettle();
    }

    testWidgets('swiping shows a confirmation with delete-files pre-checked',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      expect(find.text('Delete Movie'), findsOneWidget);
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
      expect(find.text('Example Movie'), findsOneWidget);
    });

    testWidgets('confirming with the default deletes files from disk',
        (tester) async {
      await pumpList(tester);
      await swipe(tester);
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();

      final d = deletes();
      expect(d, hasLength(1));
      expect(d.single.path, endsWith('/movie/7'));
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
      expect(d.single.path, endsWith('/movie/7'));
      expect(d.single.query['deleteFiles'], 'false');
    });
  });
}
