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
  RequestOptions? lastOptions;
  int requestCount = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    lastOptions = options;
    requestCount++;
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

class _PartialRequestAdapter implements HttpClientAdapter {
  _PartialRequestAdapter({this.ebookStatus = 'requested'});

  final String ebookStatus;
  var submitted = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Map<String, dynamic> body;
    if (options.method == 'POST') {
      submitted = true;
      body = {
        'status': 'partial',
        'book_formats': {
          'ebook': ebookStatus,
          'audiobook': 'unavailable',
        },
      };
    } else if (submitted) {
      body = {
        'status': 'partial',
        'book_formats': {
          'ebook': ebookStatus,
          'audiobook': 'unavailable',
        },
      };
    } else {
      body = {'status': 'unavailable'};
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

class _FailedPostAfterMutationAdapter implements HttpClientAdapter {
  var mutated = false;
  var postCount = 0;
  var statusChecks = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      postCount++;
      mutated = true;
      return ResponseBody.fromString(
        jsonEncode({'error': 'upstream response was lost'}),
        500,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    statusChecks++;
    final body = mutated
        ? {
            'status': 'requested',
            'book_formats': {'ebook': 'requested'},
          }
        : {'status': 'unavailable'};
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

class _FailedPostWithoutMutationAdapter implements HttpClientAdapter {
  var postCount = 0;
  var statusChecks = 0;
  var submitted = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      postCount++;
      submitted = true;
      return ResponseBody.fromString(
        jsonEncode({
          'code': 'book_search_unconfirmed',
          'error': 'upstream response was lost',
        }),
        502,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    statusChecks++;
    return ResponseBody.fromString(
      jsonEncode(submitted
          ? {
              'status': 'unavailable',
              'status_known': false,
              'unknown_reason': 'outcome_pending',
            }
          : {'status': 'unavailable'}),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _CodedPostFailureAdapter implements HttpClientAdapter {
  final String postCode;
  final int postStatus;
  final String? reconciledFailureCode;
  var submitted = false;
  var postCount = 0;
  var statusChecks = 0;

  _CodedPostFailureAdapter({
    required this.postCode,
    required this.postStatus,
    this.reconciledFailureCode,
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      submitted = true;
      postCount++;
      return ResponseBody.fromString(
        jsonEncode({
          'code': postCode,
          'error': 'server wording intentionally ignored',
        }),
        postStatus,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    statusChecks++;
    final body = submitted && reconciledFailureCode != null
        ? {
            'status': 'unavailable',
            'status_known': false,
            'unknown_reason': 'request_failed',
            'failure_code': reconciledFailureCode,
            'book_formats': {'ebook': 'unavailable'},
          }
        : {'status': 'unavailable'};
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

class _DeferredPostRefreshAdapter implements HttpClientAdapter {
  final refreshResponse = Completer<ResponseBody>();
  var statusChecks = 0;
  var postCount = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      postCount++;
      return _jsonResponse({
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      });
    }
    statusChecks++;
    if (statusChecks == 1) {
      return _jsonResponse({'status': 'unavailable'});
    }
    return refreshResponse.future;
  }

  void completeRefresh() {
    refreshResponse.complete(_jsonResponse({
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
    }));
  }

  ResponseBody _jsonResponse(Map<String, dynamic> body) =>
      ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  void close({bool force = false}) {}
}

class _TerminalFailureRetryAdapter implements HttpClientAdapter {
  final String failureCode;
  final retryResponse = Completer<ResponseBody>();
  Map<String, dynamic> submittedBody = {};
  var postCount = 0;
  var statusChecks = 0;
  var retryCompleted = false;

  _TerminalFailureRetryAdapter({
    this.failureCode = 'book_edition_unavailable',
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      postCount++;
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      submittedBody = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      return retryResponse.future;
    }
    statusChecks++;
    return _jsonResponse(retryCompleted
        ? {
            'status': 'requested',
            'book_formats': {'audiobook': 'requested'},
          }
        : {
            'status': 'unavailable',
            'status_known': false,
            'unknown_reason': 'request_failed',
            'failure_code': failureCode,
            'book_formats': {'audiobook': 'unavailable'},
          });
  }

  void completeRetry() {
    retryCompleted = true;
    retryResponse.complete(_jsonResponse({
      'status': 'requested',
      'book_formats': {'audiobook': 'requested'},
    }));
  }

  ResponseBody _jsonResponse(Map<String, dynamic> body) =>
      ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

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
      expect(d.isRequestable(BookRequestFormat.ebook), isTrue);
    });

    test('aggregate requested without format truth blocks duplicate actions',
        () async {
      final d = await _service({'status': 'requested'})
          .checkBookStatusDetail('fb');

      expect(d.isKnown, isFalse);
      expect(d.statusFor(BookRequestFormat.ebook), isNull);
      expect(d.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(d.isRequestable(BookRequestFormat.audiobook), isFalse);
    });

    test('a legacy both status expands to both concrete formats', () async {
      final d = await _service({
        'status': 'requested',
        'book_formats': {'both': 'requested'},
      }).checkBookStatusDetail('fb');

      expect(d.statusFor(BookRequestFormat.ebook), RequestStatus.requested);
      expect(d.statusFor(BookRequestFormat.audiobook), RequestStatus.requested);
    });

    test('an unknown server status is not treated as requestable', () async {
      final d = await _service({
        'status': 'future-status',
        'book_formats': {'ebook': 'future-status'},
      }).checkBookStatusDetail('fb');

      expect(d.isKnown, isFalse);
      expect(d.statusFor(BookRequestFormat.ebook), isNull);
      expect(d.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(d.effectiveUnknownReason, BookStatusUnknownReason.transient);
    });

    test('explicit unresolved format truth carries an admin-fixable reason',
        () async {
      final d = await _service({
        'status': 'unavailable',
        'status_known': false,
        'book_formats': const <String, dynamic>{},
      }).checkBookStatusDetail('fb');

      expect(d.isKnown, isFalse);
      expect(
        d.effectiveUnknownReason,
        BookStatusUnknownReason.formatNeedsAttention,
      );
    });

    test('terminal request failure preserves its retry-safe format and code',
        () async {
      final d = await _service({
        'status': 'unavailable',
        'status_known': false,
        'unknown_reason': 'request_failed',
        'failure_code': 'book_edition_unavailable',
        'book_formats': {'audiobook': 'unavailable'},
      }).checkBookStatusDetail('fb');

      expect(d.isKnown, isFalse);
      expect(
        d.effectiveUnknownReason,
        BookStatusUnknownReason.requestFailed,
      );
      expect(d.failureCode, 'book_edition_unavailable');
      expect(d.isRequestable(BookRequestFormat.audiobook), isTrue);
      expect(d.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(d.withOwnership(null).failureCode, 'book_edition_unavailable');
    });

    test('an unknown format key blocks uncovered format actions', () async {
      final d = await _service({
        'status': 'requested',
        'book_formats': {
          'ebook': 'requested',
          'future-audio': 'requested',
        },
      }).checkBookStatusDetail('fb');

      expect(d.isKnown, isFalse);
      expect(d.statusFor(BookRequestFormat.ebook), RequestStatus.requested);
      expect(d.statusFor(BookRequestFormat.audiobook), isNull);
      expect(d.isRequestable(BookRequestFormat.audiobook), isFalse);
    });

    test('status lookup is pinned to the selected Chaptarr instance', () async {
      final adapter = _GetAdapter({'status': 'unavailable'});
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;

      await RequestService(backendDio: dio).checkBookStatusDetail(
        'fb',
        instanceId: 'books-two',
      );

      expect(adapter.lastOptions?.queryParameters['instance_id'], 'books-two');
    });
  });

  testWidgets('unknown book truth is visible and blocks request mutation',
      (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: _service({'status': 'future-status'}),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Couldn’t check · Retry'), findsOneWidget);
    expect(find.text('Choose format'), findsNothing);
    expect(find.text('Request eBook'), findsNothing);
  });

  testWidgets('unresolved book format gives guidance without a retry action',
      (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: _service({'status': 'unavailable'}),
            ownershipStatusKnown: false,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
    expect(find.text('Couldn’t check · Retry'), findsNothing);
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets('terminal book failure exposes one enabled format-safe retry',
      (tester) async {
    final adapter = _TerminalFailureRetryAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Couldn’t add · Try again'), findsOneWidget);
    expect(adapter.statusChecks, 1);
    expect(
      tester.widget<TextButton>(find.byType(TextButton)).onPressed,
      isNotNull,
    );

    await tester.tap(find.text('Couldn’t add · Try again'));
    for (var attempt = 0;
        attempt < 50 && adapter.submittedBody.isEmpty;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }

    expect(adapter.postCount, 1);
    expect(adapter.submittedBody['book_format'], 'audiobook');
    expect(
      tester.widget<TextButton>(find.byType(TextButton)).onPressed,
      isNull,
    );

    await tester.tap(find.byType(TextButton), warnIfMissed: false);
    await tester.pump();
    expect(adapter.postCount, 1);

    adapter.completeRetry();
    await tester.pumpAndSettle();

    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 2);
    expect(find.text('Couldn’t add · Try again'), findsNothing);
    expect(find.text('Request eBook'), findsOneWidget);
  });

  testWidgets('terminal library configuration failure explains admin action',
      (tester) async {
    final adapter = _TerminalFailureRetryAdapter(
      failureCode: 'book_connection_invalid',
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Ask an admin, then retry'), findsOneWidget);
    expect(
      tester.widget<TextButton>(find.byType(TextButton)).onPressed,
      isNotNull,
    );
  });

  testWidgets('partial both request names each outcome and leaves retry open',
      (tester) async {
    final adapter = _PartialRequestAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook + Audiobook'));
    await tester.pumpAndSettle();

    expect(
      find.text(
        'eBook requested. Audiobook could not be requested. Try again.',
      ),
      findsOneWidget,
    );
    expect(find.text('Request Audiobook'), findsOneWidget);
  });

  testWidgets('partial request distinguishes an already available format',
      (tester) async {
    final adapter = _PartialRequestAdapter(ebookStatus: 'available');
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook + Audiobook'));
    await tester.pumpAndSettle();

    expect(
      find.text(
        'eBook is available. Audiobook could not be requested. Try again.',
      ),
      findsOneWidget,
    );
  });

  testWidgets('an interrupted POST recovers success from exact status truth',
      (tester) async {
    final adapter = _FailedPostAfterMutationAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 2);
    expect(find.text('Request eBook'), findsNothing);
    expect(find.text('Request Audiobook'), findsOneWidget);
    expect(find.text('eBook requested.'), findsOneWidget);
  });

  testWidgets('an unknown failed POST polls without sending a duplicate',
      (tester) async {
    final adapter = _FailedPostWithoutMutationAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));
    await tester.pump();
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();

    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 4);
    expect(find.text('Choose format'), findsNothing);
    expect(find.text('Still checking · Refresh'), findsOneWidget);
    expect(
      find.text(
        'The book library is still confirming this request. Cantinarr will keep checking it.',
      ),
      findsOneWidget,
    );

    await tester.pump(const Duration(seconds: 30));
    await tester.pump();
    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 5);
  });

  testWidgets('a stable invalid selection shows reselect guidance immediately',
      (tester) async {
    final adapter = _CodedPostFailureAdapter(
      postCode: 'book_selection_invalid',
      postStatus: 400,
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 2);
    expect(
      find.text(
        'The selected book version is no longer valid. Search for the book again and choose a current version.',
      ),
      findsOneWidget,
    );
    expect(find.textContaining('couldn’t confirm'), findsNothing);
  });

  testWidgets('a reconciled terminal failure shows its actionable cause',
      (tester) async {
    final adapter = _CodedPostFailureAdapter(
      postCode: 'book_search_unconfirmed',
      postStatus: 502,
      reconciledFailureCode: 'book_edition_unavailable',
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(adapter.postCount, 1);
    expect(adapter.statusChecks, 2,
        reason: 'terminal reconciliation should stop polling');
    expect(
      find.text(
        'No eBook edition is available for this title. Try another version or format.',
      ),
      findsOneWidget,
    );
    expect(find.textContaining('couldn’t confirm'), findsNothing);
  });

  testWidgets('a successful POST stays disabled until refreshed truth arrives',
      (tester) async {
    final adapter = _DeferredPostRefreshAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final parentRefresh = Completer<void>();
    var refreshTick = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, rebuild) => BookRequestButton(
              foreignId: 'fb',
              title: 'Flock',
              service: RequestService(backendDio: dio),
              refreshTick: refreshTick,
              onRequestCompleted: () async {
                rebuild(() => refreshTick++);
                await parentRefresh.future;
              },
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Choose format'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));

    for (var attempt = 0;
        attempt < 50 && adapter.statusChecks < 2;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(adapter.postCount, 1);
    expect(refreshTick, 0);
    expect(adapter.statusChecks, 2);
    expect(tester.widget<TextButton>(find.byType(TextButton)).onPressed, isNull);

    await tester.tap(find.byType(TextButton));
    await tester.pump();
    expect(adapter.postCount, 1);

    adapter.completeRefresh();
    for (var attempt = 0;
        attempt < 50 && refreshTick == 0;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(refreshTick, 1);
    expect(adapter.statusChecks, 2,
        reason: 'the refreshTick rebuild must not supersede the accepted check');
    expect(tester.widget<TextButton>(find.byType(TextButton)).onPressed, isNull);

    parentRefresh.complete();
    await tester.pumpAndSettle();
    expect(find.text('Request Audiobook'), findsOneWidget);
    expect(adapter.postCount, 1);
  });

  testWidgets('pending formats recheck periodically and stop after disposal',
      (tester) async {
    final adapter = _GetAdapter({
      'status': 'pending',
      'book_formats': {'ebook': 'pending'},
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookRequestButton(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(adapter.requestCount, 1);

    await tester.pump(const Duration(seconds: 29));
    expect(adapter.requestCount, 1);
    await tester.pump(const Duration(seconds: 1));
    await tester.pumpAndSettle();
    expect(adapter.requestCount, 2);

    await tester.pumpWidget(const MaterialApp(home: SizedBox.shrink()));
    await tester.pump(const Duration(seconds: 60));
    expect(adapter.requestCount, 2);
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
                showCoveredStatus: true,
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
