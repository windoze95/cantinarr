import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/book_ownership.dart';
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

  test('an answered rejection is definitive; gateway statuses stay unconfirmed',
      () async {
    Future<RequestSubmissionException> submit(int statusCode) async {
      final adapter = _CaptureAdapter(
        statusCode: statusCode,
        response: {'error': 'add failed in a way the app has no label for'},
      );
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;
      try {
        await RequestService(backendDio: dio).requestBook(
          foreignId: 'book-123',
          title: 'A Book',
          format: BookRequestFormat.ebook,
        );
        fail('requestBook did not throw for status $statusCode');
      } on RequestSubmissionException catch (e) {
        return e;
      }
    }

    final rejected = await submit(500);
    expect(rejected.definitive, isTrue,
        reason: 'the server answered, so the failure is a confirmed outcome');
    expect(rejected.message,
        'The library could not complete this request. Try again later.');

    final gateway = await submit(503);
    expect(gateway.definitive, isFalse,
        reason: 'a proxy answering for the server leaves the outcome unknown');
    expect(gateway.message,
        'This book could not be requested. Check the connection and try again.');
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

  test('requestBook sends the search term that found the book', () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'Ten Algorithms: A Guide (Part 1) (A Series)',
      format: BookRequestFormat.ebook,
      searchTerm: '  ten algorithms  ',
    );

    expect(adapter.body['search_term'], 'ten algorithms');
  });

  test('requestBook omits an absent search term', () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.ebook,
      searchTerm: '   ',
    );

    expect(adapter.body.containsKey('search_term'), isFalse);
  });

  test('requestBook carries the server message for a parked request', () async {
    final adapter = _CaptureAdapter(response: {
      'status': 'pending',
      'book_formats': {'ebook': 'pending'},
      'message': "This book couldn't be matched in the library.",
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final result = await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.ebook,
    );

    expect(result?.status, RequestStatus.pending);
    expect(result?.isKnown, isTrue);
    expect(result?.message, "This book couldn't be matched in the library.");
  });

  test('requestBook leaves the message empty when the server omits it',
      () async {
    final adapter = _CaptureAdapter(response: {
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final result = await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.ebook,
    );

    expect(result?.message, isEmpty);
  });

  test('a submission carries the durable wait, not just the one-shot message',
      () async {
    final adapter = _CaptureAdapter(response: {
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
      'message': 'This book’s author is still being added to the library.',
      'book_format_waits': {
        'ebook': {
          'reason': 'author_import',
          'waiting_since': '2026-08-14T20:27:36Z',
          'last_attempt_at': '2026-08-15T02:35:49Z',
        },
      },
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final result = await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.ebook,
    );

    final wait = result?.formatWaits[BookRequestFormat.ebook];
    expect(wait?.reason, BookWaitReason.authorImport);
    expect(wait?.waitingSince, DateTime.utc(2026, 8, 14, 20, 27, 36).toLocal());
    expect(wait?.lastAttemptAt, DateTime.utc(2026, 8, 15, 2, 35, 49).toLocal());
  });

  test('a waiting book reads as waiting, stays covered, and cannot be re-asked',
      () async {
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = _StatusAdapter({
        'status': 'requested',
        'book_formats': {'ebook': 'requested', 'audiobook': 'unavailable'},
        'book_format_waits': {
          'ebook': {
            'reason': 'author_import',
            'waiting_since': '2026-08-14T20:27:36Z',
          },
        },
      });

    final detail =
        await RequestService(backendDio: dio).checkBookStatusDetail('book-123');

    final wait = detail.waitFor(BookRequestFormat.ebook);
    expect(wait, isNotNull);
    expect(wait!.label, 'Waiting for library');
    expect(wait.explanation, contains('still adding this author'));
    expect(wait.explanation, contains('no action is needed'));
    // The status word the server had to send for older clients is unchanged,
    // and coverage still comes from it: a wait must never re-open the row.
    expect(detail.statusFor(BookRequestFormat.ebook), RequestStatus.requested);
    expect(detail.isRequestable(BookRequestFormat.ebook), isFalse);
    expect(detail.hasFormatWait, isTrue);
    // The format nobody asked for is untouched.
    expect(detail.waitFor(BookRequestFormat.audiobook), isNull);
    expect(detail.isRequestable(BookRequestFormat.audiobook), isTrue);
  });

  test('an unknown wait reason is still a wait, described generically',
      () async {
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = _StatusAdapter({
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
        'book_format_waits': {
          'ebook': {'reason': 'some_future_reason'},
        },
      });

    final detail =
        await RequestService(backendDio: dio).checkBookStatusDetail('book-123');

    final wait = detail.waitFor(BookRequestFormat.ebook);
    expect(wait?.reason, BookWaitReason.unknown);
    expect(wait?.label, 'Waiting for library');
    expect(wait?.explanation, contains('isn’t ready for this book yet'));
    expect(detail.isRequestable(BookRequestFormat.ebook), isFalse);
    // A malformed extra must not fail the status closed — coverage came from
    // book_formats and is unaffected.
    expect(detail.isKnown, isTrue);
  });

  test('an older server without waits behaves exactly as it did', () async {
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = _StatusAdapter({
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      });

    final detail =
        await RequestService(backendDio: dio).checkBookStatusDetail('book-123');

    expect(detail.waitFor(BookRequestFormat.ebook), isNull);
    expect(detail.hasFormatWait, isFalse);
    expect(detail.statusFor(BookRequestFormat.ebook), RequestStatus.requested);
    expect(detail.isKnown, isTrue);
  });

  test('live truth retires the wait with the absence it explained', () {
    const wait = BookFormatWait(reason: BookWaitReason.authorImport);
    // A wait the server sent moments ago, against an ownership digest that has
    // since seen the file. Explaining why a book is missing, next to the book,
    // is its own kind of wrong.
    const detail = BookRequestStatusDetail(
      status: RequestStatus.requested,
      formats: {BookRequestFormat.ebook: RequestStatus.requested},
      formatWaits: {BookRequestFormat.ebook: wait},
      ownership: BookOwnership(
        ebook: FormatOwnership(monitored: true, downloaded: true),
      ),
    );

    expect(detail.statusFor(BookRequestFormat.ebook), RequestStatus.available);
    expect(detail.waitFor(BookRequestFormat.ebook), isNull);
    expect(detail.hasFormatWait, isFalse);
  });
}

/// Answers any GET with one fixed status payload.
class _StatusAdapter implements HttpClientAdapter {
  final Map<String, dynamic> response;

  _StatusAdapter(this.response);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async =>
      ResponseBody.fromString(
        jsonEncode(response),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  void close({bool force = false}) {}
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
