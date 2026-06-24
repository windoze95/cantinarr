import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/settings/data/request_settings_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('requestBook sends the selected book format', () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = RequestService(backendDio: dio);

    final status = await service.requestBook(
      foreignId: 'book-123',
      title: 'Star Wars: Heir to the Empire',
      format: BookRequestFormat.audiobook,
    );

    expect(status, RequestStatus.requested);
    expect(adapter.body['media_type'], 'book');
    expect(adapter.body['foreign_id'], 'book-123');
    expect(adapter.body['book_format'], 'audiobook');
  });

  test('pending book requests expose media and format labels', () {
    final item = PendingRequestItem.fromJson({
      'id': 1,
      'user_id': 2,
      'username': 'reader',
      'media_type': 'book',
      'title': 'Star Wars: Heir to the Empire',
      'book_format': 'both',
    });

    expect(item.isBook, isTrue);
    expect(item.mediaLabel, 'Book');
    expect(item.requestedBookFormat, BookRequestFormat.both);
  });
}

class _CaptureAdapter implements HttpClientAdapter {
  Map<String, dynamic> body = {};

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final bytes = <int>[];
    if (requestStream != null) {
      await for (final chunk in requestStream) {
        bytes.addAll(chunk);
      }
    }
    body = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
    return ResponseBody.fromString(
      jsonEncode({'status': 'requested'}),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
