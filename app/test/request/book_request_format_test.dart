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

    expect(status?.status, RequestStatus.requested);
    expect(adapter.body['media_type'], 'book');
    expect(adapter.body['foreign_id'], 'book-123');
    expect(adapter.body['book_format'], 'audiobook');
  });

  test('requestBook pins the selected instance and preserves partial formats',
      () async {
    final adapter = _CaptureAdapter(response: {
      'status': 'partial',
      'book_formats': {
        'ebook': 'requested',
        'audiobook': 'unavailable',
      },
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final result = await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.both,
      instanceId: 'books-two',
    );

    expect(adapter.body['instance_id'], 'books-two');
    expect(result?.status, RequestStatus.partial);
    expect(result?.succeeded(BookRequestFormat.ebook), isTrue);
    expect(result?.succeeded(BookRequestFormat.audiobook), isFalse);
  });

  test('requestBook fails closed on unknown response statuses', () async {
    final cases = [
      {
        'status': 'future-status',
        'book_formats': {'ebook': 'requested'},
      },
      {
        'status': 'partial',
        'book_formats': {
          'ebook': 'future-status',
          'audiobook': 'unavailable',
        },
      },
    ];

    for (final response in cases) {
      final adapter = _CaptureAdapter(response: response);
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;
      final result = await RequestService(backendDio: dio).requestBook(
        foreignId: 'book-123',
        title: 'A Book',
        format: BookRequestFormat.both,
      );

      expect(result?.isKnown, isFalse);
    }
  });

  test('submission success excludes denied and unavailable outcomes', () {
    const submission = BookRequestSubmission(
      status: RequestStatus.partial,
      formats: {
        BookRequestFormat.ebook: RequestStatus.denied,
        BookRequestFormat.audiobook: RequestStatus.pending,
      },
    );

    expect(submission.succeeded(BookRequestFormat.ebook), isFalse);
    expect(submission.succeeded(BookRequestFormat.audiobook), isTrue);
    expect(submission.succeeded(BookRequestFormat.both), isFalse);
  });

  test('requestBook surfaces backend error messages', () async {
    final adapter = _CaptureAdapter(
      statusCode: 500,
      response: {'error': 'no audiobook edition available'},
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = RequestService(backendDio: dio);

    expect(
      () => service.requestBook(
        foreignId: 'book-123',
        title: 'Star Wars: Heir to the Empire',
        format: BookRequestFormat.audiobook,
      ),
      throwsA(
        isA<RequestSubmissionException>().having(
          (e) => e.message,
          'message',
          'No audiobook edition is available for this book.',
        ),
      ),
    );
  });

  test('book setup profile errors give requesters one plain next step',
      () async {
    for (final backendError in [
      'quality profile selection is ambiguous',
      'metadata profile is missing',
    ]) {
      final adapter = _CaptureAdapter(
        statusCode: 500,
        response: {'error': backendError},
      );
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;

      expect(
        () => RequestService(backendDio: dio).requestBook(
          foreignId: 'book-123',
          title: 'A Book',
          format: BookRequestFormat.ebook,
        ),
        throwsA(
          isA<RequestSubmissionException>().having(
            (e) => e.message,
            'message',
            'Ask an admin to check the book settings.',
          ),
        ),
      );
    }
  });

  test('pending book requests expose media and format labels', () {
    final item = PendingRequestItem.fromJson({
      'id': 1,
      'user_id': 2,
      'username': 'reader',
      'media_type': 'book',
      'title': 'Star Wars: Heir to the Empire',
      'book_format': 'both',
      'instance_name': 'Family Books',
      'requester_count': 2,
    });

    expect(item.isBook, isTrue);
    expect(item.mediaLabel, 'Book');
    expect(item.requestedBookFormat, BookRequestFormat.both);
    expect(item.instanceName, 'Family Books');
    expect(item.requesterCount, 2);
    expect(item.requestedByLabel, 'Requested by reader and 1 other');
  });

  test('unknown pending book formats are not converted into both', () {
    final item = PendingRequestItem.fromJson({
      'id': 1,
      'media_type': 'book',
      'book_format': 'future-format',
    });

    expect(item.requestedBookFormat, isNull);
  });
}

class _CaptureAdapter implements HttpClientAdapter {
  Map<String, dynamic> body = {};
  final int statusCode;
  final Map<String, dynamic> response;

  _CaptureAdapter({
    this.statusCode = 200,
    this.response = const {'status': 'requested'},
  });

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
      jsonEncode(response),
      statusCode,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
