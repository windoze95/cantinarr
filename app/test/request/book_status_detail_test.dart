import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:dio/dio.dart';
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

RequestService _service(Map<String, dynamic> resp) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _GetAdapter(resp);
  return RequestService(backendDio: dio);
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
}
