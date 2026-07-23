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
    expect(adapter.requestOptions?.connectTimeout,
        const Duration(seconds: 240));
    expect(adapter.requestOptions?.receiveTimeout,
        const Duration(seconds: 240));
  });

  test('requestBook sends the selected external publication identity',
      () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await RequestService(backendDio: dio).requestBook(
      foreignId: 'book-123',
      title: 'A Book',
      format: BookRequestFormat.audiobook,
      selection: const BookRequestSelection(
        foreignAuthorId: 'author-456',
        authorName: 'The Author',
        audiobook: BookPublicationSelection(
          foreignEditionId: 'edition-789',
          isbn13: '9780000000000',
          asin: 'B00BOOK',
          editionTitle: 'Anniversary narration',
          publisher: 'Example Audio',
          language: 'English',
          year: 2024,
          pageCount: 321,
        ),
      ),
    );

    expect(adapter.body['book_selection'], {
      'foreign_author_id': 'author-456',
      'author_name': 'The Author',
      'audiobook': {
        'foreign_edition_id': 'edition-789',
        'isbn13': '9780000000000',
        'asin': 'B00BOOK',
        'edition_title': 'Anniversary narration',
        'publisher': 'Example Audio',
        'language': 'English',
        'year': 2024,
        'page_count': 321,
      },
    });
    expect(
      (adapter.body['book_selection'] as Map<String, dynamic>)
          .containsKey('ebook'),
      isFalse,
    );
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

  test('requestBook uses stable error codes and preserves retry safety',
      () async {
    final knownFailure = _CaptureAdapter(
      statusCode: 503,
      response: {
        'code': 'book_catalog_pending',
        'error': 'server wording intentionally ignored',
      },
    );
    final knownDio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = knownFailure;

    await expectLater(
      () => RequestService(backendDio: knownDio).requestBook(
        foreignId: 'book-123',
        title: 'A Book',
        format: BookRequestFormat.ebook,
      ),
      throwsA(
        isA<RequestSubmissionException>()
            .having((e) => e.code, 'code', 'book_catalog_pending')
            .having((e) => e.definitive, 'definitive', isFalse)
            .having(
              (e) => e.message,
              'message',
              'The book library is still preparing this title. Try again in a moment.',
            ),
      ),
    );

    final unverifiedEdition = _CaptureAdapter(
      statusCode: 502,
      response: {
        'code': 'book_request_unverified',
        'error': 'server wording intentionally ignored',
      },
    );
    final unknownDio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = unverifiedEdition;

    await expectLater(
      () => RequestService(backendDio: unknownDio).requestBook(
        foreignId: 'book-123',
        title: 'A Book',
        format: BookRequestFormat.ebook,
      ),
      throwsA(
        isA<RequestSubmissionException>()
            .having((e) => e.code, 'code', 'book_request_unverified')
            .having((e) => e.definitive, 'definitive', isTrue)
            .having(
              (e) => e.message,
              'message',
              'Cantinarr could not verify the selected edition, so no download search was started. Try again or ask an admin to check the book library.',
            ),
      ),
    );
  });

  test('requestBook classifies every verified book workflow boundary',
      () async {
    final cases = <({String code, int status, String message, bool definitive})>[
      (
        code: 'book_selection_invalid',
        status: 400,
        message:
            'The selected book version is no longer valid. Search for the book again and choose a current version.',
        definitive: true,
      ),
      (
        code: 'book_match_not_found',
        status: 409,
        message:
            'This catalog match changed. Search for the book again and retry.',
        definitive: true,
      ),
      (
        code: 'book_configuration_invalid',
        status: 503,
        message:
            'An admin needs to check this book library’s profiles and folders.',
        definitive: true,
      ),
      (
        code: 'book_connection_invalid',
        status: 503,
        message: 'An admin needs to check this book library’s connection.',
        definitive: true,
      ),
      (
        code: 'book_outcome_pending',
        status: 503,
        message:
            'The book library is still confirming this request. Cantinarr will keep checking it.',
        definitive: false,
      ),
      (
        code: 'book_request_rejected',
        status: 422,
        message:
            'The book library rejected this title or edition. Refresh the catalog and try again, or ask an admin to check the book library.',
        definitive: true,
      ),
      (
        code: 'book_search_rejected',
        status: 422,
        message:
            'The book was prepared, but the book library rejected its download search. Ask an admin to check the book library.',
        definitive: true,
      ),
    ];

    for (final testCase in cases) {
      final adapter = _CaptureAdapter(
        statusCode: testCase.status,
        response: {
          'code': testCase.code,
          'error': 'server wording intentionally ignored',
        },
      );
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;

      await expectLater(
        () => RequestService(backendDio: dio).requestBook(
          foreignId: 'book-123',
          title: 'A Book',
          format: BookRequestFormat.audiobook,
        ),
        throwsA(
          isA<RequestSubmissionException>()
              .having((e) => e.code, 'code', testCase.code)
              .having(
                (e) => e.definitive,
                'definitive',
                testCase.definitive,
              )
              .having((e) => e.message, 'message', testCase.message),
        ),
      );
    }
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

  test('multi-book catalog results explain the supported next step', () async {
    final adapter = _CaptureAdapter(
      statusCode: 422,
      response: {
        'code': 'book_multi_work_unsupported',
        'error': 'internal wording should not become the UI contract',
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await expectLater(
      () => RequestService(backendDio: dio).requestBook(
        foreignId: 'bundle-123',
        title: 'Example Books 1-3',
        format: BookRequestFormat.ebook,
      ),
      throwsA(
        isA<RequestSubmissionException>()
            .having((e) => e.definitive, 'definitive', isTrue)
            .having(
              (e) => e.message,
              'message',
              'This result contains multiple books. Choose an individual title instead.',
            ),
      ),
    );
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
      'book_selection': {
        'foreign_author_id': 'author-external-17',
        'author_name': 'Timothy Zahn',
        'ebook': {
          'foreign_edition_id': 'edition-ebook-2',
          'isbn13': '9780553296129',
          'edition_title': '20th Anniversary Edition',
          'publisher': 'Del Rey',
          'language': 'English',
          'year': 2011,
          'page_count': 496,
        },
        'audiobook': {
          'foreign_edition_id': 'edition-audio-3',
          'asin': 'B00513KQ9C',
          'publisher': 'Random House Audio',
          'year': 2011,
        },
      },
    });

    expect(item.isBook, isTrue);
    expect(item.mediaLabel, 'Book');
    expect(item.requestedBookFormat, BookRequestFormat.both);
    expect(item.instanceName, 'Family Books');
    expect(item.requesterCount, 2);
    expect(item.requestedByLabel, 'Requested by reader and 1 other');
    expect(item.bookSelection?.foreignAuthorId, 'author-external-17');
    expect(item.bookSelection?.authorName, 'Timothy Zahn');
    expect(item.bookSelection?.ebook?.foreignEditionId, 'edition-ebook-2');
    expect(item.bookSelection?.ebook?.isbn13, '9780553296129');
    expect(item.bookSelection?.ebook?.editionTitle, '20th Anniversary Edition');
    expect(item.bookSelection?.ebook?.publisher, 'Del Rey');
    expect(item.bookSelection?.ebook?.language, 'English');
    expect(item.bookSelection?.ebook?.year, 2011);
    expect(item.bookSelection?.ebook?.pageCount, 496);
    expect(item.bookSelection?.audiobook?.foreignEditionId, 'edition-audio-3');
    expect(item.bookSelection?.audiobook?.asin, 'B00513KQ9C');
  });

  test('approval requests allow the verified Chaptarr mutation window',
      () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await RequestSettingsService(backendDio: dio).approve(7);

    expect(adapter.requestOptions?.path, '/api/admin/requests/7/approve');
    expect(adapter.requestOptions?.connectTimeout,
        const Duration(seconds: 240));
    expect(adapter.requestOptions?.receiveTimeout,
        const Duration(seconds: 240));
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
  RequestOptions? requestOptions;
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
    requestOptions = options;
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
