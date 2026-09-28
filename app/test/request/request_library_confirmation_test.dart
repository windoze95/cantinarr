import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/logic/request_quota_provider.dart';
import 'package:cantinarr/features/request/ui/album_request_panel.dart';
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
import 'package:cantinarr/features/request/ui/request_library_picker.dart';
import 'package:cantinarr/features/request/ui/request_options_sheet.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const libraries = [
  LibraryChoice(id: 'a', name: 'Library A'),
  LibraryChoice(id: 'b', name: 'Library B')
];

class Capture implements HttpClientAdapter {
  final posts = <Map<String, dynamic>>[];
  final reads = <String?>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    Map<String, dynamic> data;
    if (options.method == 'POST') {
      posts.add(jsonDecode(utf8
              .decode(await requestStream!.expand((chunk) => chunk).toList()))
          as Map<String, dynamic>);
      data = {
        'status': 'pending',
        'book_formats': {'ebook': 'pending', 'audiobook': 'unavailable'},
        'delivery': <dynamic>[]
      };
    } else {
      reads.add(options.queryParameters['instance_id'] as String?);
      data = {
        'status': 'unavailable',
        'book_formats': {'ebook': 'unavailable', 'audiobook': 'unavailable'},
        'delivery': <dynamic>[]
      };
    }
    return ResponseBody.fromString(jsonEncode(data), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  test('personal request choices exclude administrator-only navigation entries',
      () {
    const connection = BackendConnection(
        serverUrl: 'http://example',
        accessToken: '',
        refreshToken: '',
        instances: [
          ServiceInstance(
              id: 'a',
              serviceType: 'chaptarr',
              name: 'All users',
              isDefault: true,
              assigned: false,
              requestDefault: false),
          ServiceInstance(
              id: 'b',
              serviceType: 'chaptarr',
              name: 'Mine',
              assigned: true,
              requestDefault: true),
        ]);
    expect(connection.chaptarrInstances, hasLength(2));
    expect(requestLibraries(connection, 'chaptarr').map((i) => i.id), ['b']);
    expect(defaultRequestLibrary(connection, 'chaptarr'), 'b');
    final restored =
        ServiceInstance.fromJson(connection.instances.last.toJson());
    expect(restored.assigned, isTrue);
    expect(restored.requestDefault, isTrue);
  });

  test('older config responses retain their existing default and choices', () {
    final connection = BackendConnection(
        serverUrl: 'http://example',
        accessToken: '',
        refreshToken: '',
        instances: [
          ServiceInstance.fromJson({
            'id': 'legacy',
            'service_type': 'chaptarr',
            'name': 'Books',
            'is_default': true
          }),
        ]);
    expect(requestLibraries(connection, 'chaptarr').single.id, 'legacy');
    expect(defaultRequestLibrary(connection, 'chaptarr'), 'legacy');
  });

  for (final choices in [
    const <LibraryChoice>[],
    const [LibraryChoice(id: 'only', name: 'Only library')]
  ]) {
    testWidgets('${choices.length} assignments cannot open an ambiguous picker',
        (tester) async {
      String? selected;
      await tester.pumpWidget(MaterialApp(
          home: Scaffold(
              body: Builder(
                  builder: (context) => TextButton(
                      onPressed: () async {
                        selected = await confirmRequestLibrary(context,
                            libraries: choices, defaultLibraryId: 'unassigned');
                      },
                      child: const Text('Request'))))));
      await tester.tap(find.text('Request'));
      await tester.pumpAndSettle();
      expect(find.byType(RequestOptionsSheet), findsNothing);
      expect(selected, choices.isEmpty ? null : 'only');
      if (choices.isEmpty) {
        expect(find.textContaining('No request library is assigned.'),
            findsOneWidget);
      }
    });
  }

  for (final book in [true, false]) {
    for (final chooseOther in [false, true]) {
      testWidgets(
          '${book ? 'book' : 'music'} confirms ${chooseOther ? 'chosen' : 'default'} library before writing',
          (tester) async {
        final capture = Capture();
        final service = RequestService(
            backendDio: Dio(BaseOptions(baseUrl: 'http://example'))
              ..httpClientAdapter = capture);
        final panel = book
            ? BookFormatPanel(
                foreignId: 'title',
                title: 'Title',
                instanceId: 'a',
                service: service,
                requestLibraries: libraries,
                defaultRequestLibraryId: 'b',
                chooseRequestLibrary: true)
            : AlbumRequestPanel(
                foreignId: 'title',
                title: 'Title',
                instanceId: 'a',
                service: service,
                requestLibraries: libraries,
                defaultRequestLibraryId: 'b',
                chooseRequestLibrary: true);
        await tester.pumpWidget(ProviderScope(
            overrides: [
              requestQuotasSupportedProvider.overrideWithValue(false)
            ],
            child: MaterialApp(
                home: Scaffold(body: SingleChildScrollView(child: panel)))));
        await tester.pumpAndSettle();
        await tester.tap(find.text('Request').first);
        await tester.pumpAndSettle();
        expect(capture.posts, isEmpty);
        expect(
            tester
                .widget<ChoiceChip>(
                    find.widgetWithText(ChoiceChip, 'Library B'))
                .selected,
            isTrue);
        await tester.tap(find.text('Cancel'));
        await tester.pumpAndSettle();
        expect(capture.posts, isEmpty);
        await tester.tap(find.text('Request').first);
        await tester.pumpAndSettle();
        if (chooseOther) {
          await tester.tap(find.text('Library A'));
          await tester.pumpAndSettle();
        }
        await tester.tap(find.text('Request').last);
        await tester.pumpAndSettle();
        expect(capture.posts.single['instance_id'], chooseOther ? 'a' : 'b');
        expect(capture.posts.single['foreign_id'], 'title');
        if (book) expect(capture.posts.single['book_format'], 'ebook');
        expect(capture.reads.last, chooseOther ? 'a' : 'b');
        await tester.pumpWidget(const SizedBox.shrink());
      });
    }
  }
}
