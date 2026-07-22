import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/ui/book_request_button.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Minimal GET adapter returning a canned book-status JSON body.
class _GetAdapter implements HttpClientAdapter {
  _GetAdapter(this.responseJson);
  final Map<String, dynamic> responseJson;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    return ResponseBody.fromString(
      jsonEncode(responseJson),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _DeferredStatusAdapter implements HttpClientAdapter {
  final responses = <String, Completer<ResponseBody>>{};

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) {
    final foreignId = options.queryParameters['foreign_id'] as String;
    final completer = Completer<ResponseBody>();
    responses[foreignId] = completer;
    return completer.future;
  }

  void complete(String foreignId, Map<String, dynamic> body) {
    responses[foreignId]!.complete(
      ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      ),
    );
  }

  @override
  void close({bool force = false}) {}
}

RequestService _service(Map<String, dynamic> resp) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _GetAdapter(resp);
  return RequestService(backendDio: dio);
}

Future<void> _waitForRequest(
  WidgetTester tester,
  _DeferredStatusAdapter adapter,
  String foreignId,
) async {
  for (var attempt = 0;
      attempt < 100 && !adapter.responses.containsKey(foreignId);
      attempt++) {
    await tester.pump(const Duration(milliseconds: 1));
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 1));
    });
  }
  expect(adapter.responses, contains(foreignId));
}

void main() {
  group('checkBookStatusDetail', () {
    test('one requested format leaves the other requestable', () async {
      final d = await _service({
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      }).checkBookStatusDetail('fb');

      expect(d.status, RequestStatus.requested);
      expect(d.isCovered(BookRequestFormat.ebook), isTrue);
      expect(d.isCovered(BookRequestFormat.audiobook), isFalse);
    });

    test('both formats covered', () async {
      final d = await _service({
        'status': 'requested',
        'book_formats': {'ebook': 'requested', 'audiobook': 'pending'},
      }).checkBookStatusDetail('fb');

      expect(d.isCovered(BookRequestFormat.ebook), isTrue);
      expect(d.isCovered(BookRequestFormat.audiobook), isTrue);
    });

    test('denied stays requestable (not covered)', () async {
      final d = await _service({
        'status': 'denied',
        'book_formats': {'ebook': 'denied'},
      }).checkBookStatusDetail('fb');

      expect(d.isCovered(BookRequestFormat.ebook), isFalse);
    });

    test('no book_formats means nothing is covered', () async {
      final d = await _service({'status': 'unavailable'})
          .checkBookStatusDetail('fb');

      expect(d.isCovered(BookRequestFormat.ebook), isFalse);
      expect(d.isCovered(BookRequestFormat.audiobook), isFalse);
    });
  });

  testWidgets('a late status response cannot overwrite a reused book button',
      (tester) async {
    final adapter = _DeferredStatusAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = RequestService(backendDio: dio);
    var foreignId = 'old-book';
    late StateSetter rebuild;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              rebuild = setState;
              return BookRequestButton(
                foreignId: foreignId,
                title: foreignId,
                service: service,
              );
            },
          ),
        ),
      ),
    );
    await _waitForRequest(tester, adapter, 'old-book');

    rebuild(() => foreignId = 'new-book');
    await _waitForRequest(tester, adapter, 'new-book');

    adapter.complete('new-book', {
      'status': 'requested',
      'book_formats': {
        'ebook': 'requested',
        'audiobook': 'requested',
      },
    });
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);

    adapter.complete('old-book', {'status': 'unavailable'});
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);
  });
}
