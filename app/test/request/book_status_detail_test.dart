import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
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
  var statusChecks = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
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

// _RejectedPostAdapter answers every status check with "not requested" and
// rejects the POST outright with an error the app has no specific label for —
// the shape of a live arr-side validation refusal that mutated nothing.
class _RejectedPostAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      return ResponseBody.fromString(
        jsonEncode({
          'error': 'chaptarr add book: chaptarr POST returned status 400',
        }),
        500,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode({'status': 'unavailable'}),
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

/// Accepts one request per format and then serves that format back as live
/// truth, so a row's post-request state is the server's answer, not a local
/// guess.
class _RequestFlowAdapter implements HttpClientAdapter {
  final requestBodies = <Map<String, dynamic>>[];
  final _requested = <String, String>{};

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      final body = Map<String, dynamic>.from(options.data as Map);
      requestBodies.add(body);
      final format = body['book_format'] as String;
      _requested[format] = 'requested';
      return _json({
        'status': 'requested',
        'book_formats': {format: 'requested'},
      });
    }
    return _json({
      'status': _requested.isEmpty ? 'unavailable' : 'requested',
      'book_formats': _requested,
    });
  }

  ResponseBody _json(Map<String, dynamic> body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  void close({bool force = false}) {}
}

/// Accepts a request and then serves a status read that has not caught up (or
/// has moved on), reproducing Chaptarr truth that lags a just-accepted add.
class _LaggingStatusAdapter implements HttpClientAdapter {
  _LaggingStatusAdapter({required this.postBody, required this.afterBody});

  final Map<String, dynamic> postBody;
  final Map<String, dynamic> afterBody;
  var posts = 0;
  var _submitted = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST') {
      posts++;
      _submitted = true;
      return _json(postBody);
    }
    return _json(_submitted ? afterBody : {'status': 'unavailable'});
  }

  ResponseBody _json(Map<String, dynamic> body) => ResponseBody.fromString(
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
  _concurrencyTests();
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

    test('a canonical foreign id is surfaced and survives ownership overlay',
        () async {
      final d = await _service({
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
        'canonical_foreign_id': '  canon-9  ',
      }).checkBookStatusDetail('fb');

      expect(d.canonicalForeignId, 'canon-9');
      expect(d.withOwnership(null).canonicalForeignId, 'canon-9');
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

  testWidgets('tapping a format row requests exactly that format', (tester) async {
    final adapter = _RequestFlowAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    // A still-open format offers its own action instead of a bare state label.
    expect(find.text('Request'), findsNWidgets(2));
    expect(find.text('Not requested'), findsNothing);

    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['book_format'], 'ebook');
    expect(adapter.requestBodies.single['foreign_id'], 'fb');
    // The tap is confirmed twice: the row's own state, and a named outcome.
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('eBook requested.'), findsOneWidget);
    // The untouched format stays actionable, and the requested one does not.
    expect(find.text('Request'), findsOneWidget);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);
  });

  testWidgets('a re-keyed record reports its canonical id to the panel owner',
      (tester) async {
    final ids = <String>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookFormatPanel(
            foreignId: 'fb',
            title: 'Flock',
            service: _service({
              'status': 'requested',
              'book_formats': {'audiobook': 'requested'},
              'canonical_foreign_id': 'canon-1',
            }),
            onCanonicalForeignId: ids.add,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(ids, ['canon-1']);
  });

  testWidgets('a canonical id equal to the panel id is not reported',
      (tester) async {
    final ids = <String>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookFormatPanel(
            foreignId: 'fb',
            title: 'Flock',
            service: _service({
              'status': 'requested',
              'book_formats': {'audiobook': 'requested'},
              'canonical_foreign_id': 'fb',
            }),
            onCanonicalForeignId: ids.add,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(ids, isEmpty);
  });

  testWidgets('a confirmed request is not re-offered when live truth lags',
      (tester) async {
    // Chaptarr has not materialised the record yet, so the status read after the
    // accepted POST still reports the format as never requested.
    final adapter = _LaggingStatusAdapter(
      postBody: {
        'status': 'requested',
        'book_formats': {'audiobook': 'requested'},
      },
      afterBody: {'status': 'unavailable'},
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    await tester.tap(_row('audiobook'));
    await tester.pumpAndSettle();

    expect(find.text('Audiobook requested.'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNull);
    // Only the untouched format is still on offer.
    expect(find.text('Request'), findsOneWidget);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNotNull);

    await tester.tap(_row('audiobook'));
    await tester.pumpAndSettle();
    expect(adapter.posts, 1);
  });

  testWidgets('an admin denial still overrides a confirmed request',
      (tester) async {
    final adapter = _LaggingStatusAdapter(
      postBody: {
        'status': 'pending',
        'book_formats': {'ebook': 'pending'},
      },
      afterBody: {
        'status': 'denied',
        'book_formats': {'ebook': 'denied'},
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    // Remembering the submission must never hide a decision made about it.
    expect(find.text('Request Denied'), findsOneWidget);
    expect(find.text('Request again'), findsOneWidget);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNotNull);
  });

  testWidgets('a request the server holds for approval says so', (tester) async {
    final adapter = _GetAdapter({
      'status': 'pending',
      'book_formats': {'audiobook': 'pending'},
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    expect(find.text('eBook is pending approval.'), findsOneWidget);
    // The requested format holds the approval state the server reported, even
    // though this stub's status read only ever mentions the other format.
    expect(find.text('Pending Approval'), findsNWidgets(2));
    expect(find.text('Request'), findsNothing);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
  });

  testWidgets('a request the library has not taken yet says what it is waiting on',
      (tester) async {
    await tester.pumpWidget(_panel(_service({
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
      'book_format_waits': {
        'ebook': {
          'reason': 'author_import',
          'waiting_since': '2026-08-14T20:27:36Z',
        },
      },
    })));
    await tester.pumpAndSettle();

    // "Requested" is what this row said before, and it claims a library record
    // that does not exist. One word is the whole defect.
    expect(find.text('Waiting for library'), findsOneWidget);
    expect(find.text('Requested'), findsNothing);
    // Durable, not a toast: it is on screen without anyone having just tapped.
    expect(
      find.textContaining('The library is still adding this author'),
      findsOneWidget,
    );
    expect(find.textContaining('no action is needed'), findsOneWidget);
    // Nothing to press on the waiting row. No retry, no ETA, no second ask.
    expect(
      find.descendant(of: _row('ebook'), matching: find.text('Request')),
      findsNothing,
    );
    expect(find.text('Couldn’t check · Retry'), findsNothing);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    // The format nobody asked for is untouched and still offerable.
    expect(
      find.descendant(of: _row('audiobook'), matching: find.text('Request')),
      findsOneWidget,
    );
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);
  });

  testWidgets('one wait, one explanation, even when both formats wait',
      (tester) async {
    await tester.pumpWidget(_panel(_service({
      'status': 'requested',
      'book_formats': {'ebook': 'requested', 'audiobook': 'requested'},
      'book_format_waits': {
        'ebook': {'reason': 'author_import'},
        'audiobook': {'reason': 'author_import'},
      },
    })));
    await tester.pumpAndSettle();

    expect(find.text('Waiting for library'), findsNWidgets(2));
    // Both rows wait on the same import; saying it twice would read as two
    // separate problems.
    expect(
      find.textContaining('The library is still adding this author'),
      findsOneWidget,
    );
  });

  testWidgets('a submitted wait survives a status read that has not caught up',
      (tester) async {
    final adapter = _WaitingSubmitAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    // The status re-read still reports nothing (the row exists, but the live
    // projection has no record to show). Falling back to a bare "Requested"
    // here would put the original wrong word back on screen at exactly the
    // moment the requester is looking.
    expect(find.text('Waiting for library'), findsOneWidget);
    expect(
      find.textContaining('The library is still adding this author'),
      findsOneWidget,
    );
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
  });

  // Pins the arrangement deterministically instead of by pixels. A golden of
  // this scene sat at 1.01% against the tolerant comparator's 1.00% budget on
  // the Linux CI renderer — glyph-box edges shifting, no structural difference
  // — and test/flutter_test_config.dart says to keep golden scenes text-light
  // for exactly that reason. Here the text IS the feature, so the scene can
  // never be text-light, and a golden a hair from failing on rasterisation
  // would flake on the next engine bump while proving nothing this doesn't.
  testWidgets('the explanation sits under the whole panel, not inside a row',
      (tester) async {
    tester.view.physicalSize = const Size(402, 874);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(_panel(_service({
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
      'book_format_waits': {
        'ebook': {'reason': 'author_import'},
      },
    })));
    await tester.pumpAndSettle();

    final explanation =
        find.textContaining('The library is still adding this author');
    final pill = find.text('Waiting for library');

    // The pill belongs to the row it describes; the explanation belongs to the
    // panel, below both rows, so it reads as one wait rather than as a caption
    // on the eBook line.
    expect(
      find.descendant(of: _row('ebook'), matching: pill),
      findsOneWidget,
    );
    expect(find.descendant(of: _row('ebook'), matching: explanation),
        findsNothing);
    expect(
      tester.getRect(explanation).top,
      greaterThan(tester.getRect(_row('audiobook')).bottom),
    );
    // And it is inside the panel's own column, so it travels with the panel.
    expect(
      find.descendant(of: find.byType(BookFormatPanel), matching: explanation),
      findsOneWidget,
    );
  });

  testWidgets('the wait survives 320px at double text size', (tester) async {
    // The narrowest screen the app targets, at 200% text. "Waiting for library"
    // is longer than the "Requested" it replaces, so the row has to stack
    // rather than run off the edge — and the explanation is a paragraph.
    tester.view.physicalSize = const Size(320, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: MediaQuery(
          data: const MediaQueryData(textScaler: TextScaler.linear(2.0)),
          // The real screen hosts this panel in a ListView, so a tall
          // accessibility layout scrolls; the failure worth catching is a row
          // that cannot fit horizontally.
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              BookFormatPanel(
                foreignId: 'fb',
                title: 'The Body Keeps the Score',
                service: _service({
                  'status': 'requested',
                  'book_formats': {'ebook': 'requested'},
                  'book_format_waits': {
                    'ebook': {'reason': 'author_import'},
                  },
                }),
              ),
            ],
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Waiting for library'), findsOneWidget);
    expect(
      find.textContaining('The library is still adding this author'),
      findsOneWidget,
    );
  });

  testWidgets('waiting formats keep rechecking so the wait can end on its own',
      (tester) async {
    final adapter = _GetAdapter({
      'status': 'requested',
      'book_formats': {'ebook': 'requested'},
      'book_format_waits': {
        'ebook': {'reason': 'author_import'},
      },
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();

    final initial = adapter.requestCount;
    // The owner gets no push when the server completes a park — nobody decided
    // anything — so this poll is the only way the screen ever stops saying
    // "waiting".
    await tester.pump(const Duration(seconds: 31));
    await tester.pumpAndSettle();
    expect(adapter.requestCount, greaterThan(initial));
  });

  testWidgets('unknown book truth is visible and blocks request mutation',
      (tester) async {
    await tester.pumpWidget(_panel(_service({'status': 'future-status'})));
    await tester.pumpAndSettle();

    expect(find.text('Couldn’t check · Retry'), findsOneWidget);
    expect(find.text('Couldn’t check'), findsNWidgets(2));
    expect(find.text('Request'), findsNothing);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNull);
  });

  testWidgets('unresolved book format gives guidance without a retry action',
      (tester) async {
    await tester.pumpWidget(_panel(
      _service({'status': 'unavailable'}),
      ownershipStatusKnown: false,
    ));
    await tester.pumpAndSettle();

    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
    expect(find.text('Couldn’t check · Retry'), findsNothing);
    expect(find.text('Request'), findsNothing);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
  });

  testWidgets('a partial response names the requested format’s own outcome',
      (tester) async {
    final adapter = _PartialRequestAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();
    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    expect(find.text('eBook requested.'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);
  });

  testWidgets('a partial response distinguishes an already available format',
      (tester) async {
    final adapter = _PartialRequestAdapter(ebookStatus: 'available');
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();
    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    expect(find.text('eBook is available.'), findsOneWidget);
    expect(find.text('Available'), findsOneWidget);
  });

  testWidgets('a format the server would not take reports its own failure',
      (tester) async {
    final adapter = _PartialRequestAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();
    await tester.tap(_row('audiobook'));
    await tester.pumpAndSettle();

    expect(
      find.text('Audiobook could not be requested. Try again.'),
      findsOneWidget,
    );
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);
  });

  testWidgets('a failed POST refreshes truth before exposing another action',
      (tester) async {
    final adapter = _FailedPostAfterMutationAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();
    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    expect(adapter.statusChecks, 2);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);
    expect(find.text('Request'), findsOneWidget);
    // The refresh proved the tapped format landed despite the failed call, so
    // the announcement matches the row instead of disowning the outcome.
    expect(find.text('eBook requested.'), findsOneWidget);
  });

  testWidgets('an answered rejection that changed nothing names the failure',
      (tester) async {
    final adapter = _RejectedPostAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
    await tester.pumpAndSettle();
    await tester.tap(_row('ebook'));
    await tester.pumpAndSettle();

    // The server answered, so the outcome is a confirmed failure — not the
    // hedged couldn't-confirm line — and the row stays requestable.
    expect(
      find.text('The library could not complete this request. Try again later.'),
      findsOneWidget,
    );
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNotNull);
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
            builder: (context, rebuild) => BookFormatPanel(
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
    await tester.tap(_row('ebook'));

    for (var attempt = 0;
        attempt < 50 && adapter.statusChecks < 2;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(adapter.postCount, 1);
    expect(refreshTick, 0);
    expect(adapter.statusChecks, 2);
    // The in-flight format says so on its own row and cannot double-submit;
    // the other row stays live — the two formats are independent actions.
    expect(find.text('Requesting…'), findsOneWidget);
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);

    adapter.completeRefresh();
    for (var attempt = 0;
        attempt < 50 && refreshTick == 0;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(refreshTick, 1);
    expect(adapter.statusChecks, 2,
        reason: 'the refreshTick rebuild must not supersede the accepted check');
    // The submitted format itself stays held until the parent refresh lands,
    // but the other format was never part of this flight.
    expect(tester.widget<InkWell>(_row('ebook')).onTap, isNull);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);

    parentRefresh.complete();
    await tester.pumpAndSettle();
    expect(find.text('Requesting…'), findsNothing);
    expect(tester.widget<InkWell>(_row('audiobook')).onTap, isNotNull);
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
    await tester.pumpWidget(_panel(RequestService(backendDio: dio)));
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

  testWidgets('a late status response cannot overwrite a reused book panel',
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
              return BookFormatPanel(
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
    expect(find.text('Requested'), findsNWidgets(2));

    adapter.complete('old-book', {'status': 'unavailable'});
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsNWidgets(2));
    expect(find.text('Request'), findsNothing);
  });
}

/// The panel under test, with only the book identity every case shares.
Widget _panel(
  RequestService service, {
  bool ownershipStatusKnown = true,
}) =>
    MaterialApp(
      home: Scaffold(
        body: BookFormatPanel(
          foreignId: 'fb',
          title: 'Flock',
          service: service,
          ownershipStatusKnown: ownershipStatusKnown,
        ),
      ),
    );

Finder _row(String format) => find.byKey(ValueKey('book-format-row:$format'));

/// Accepts a request as a wait, then keeps answering status reads with nothing
/// — the real sequence while the library is still importing the author, since
/// there is no record for the live projection to find.
class _WaitingSubmitAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final body = options.method == 'POST'
        ? {
            'status': 'requested',
            'book_formats': {'ebook': 'requested'},
            'message':
                'This book’s author is still being added to the library.',
            'book_format_waits': {
              'ebook': {'reason': 'author_import'},
            },
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

/// Holds each POST open per book_format so a test can interleave taps with
/// in-flight submissions; GETs (status checks) answer immediately.
class _HeldSubmitAdapter implements HttpClientAdapter {
  final submissionOrder = <String>[];
  final _held = <String, Completer<void>>{};

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method != 'POST') {
      return ResponseBody.fromString(
        jsonEncode({'status': 'unavailable', 'book_formats': {}}),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final bytes = <int>[];
    if (requestStream != null) {
      await for (final chunk in requestStream) {
        bytes.addAll(chunk);
      }
    }
    final decoded = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
    final format = decoded['book_format'] as String;
    submissionOrder.add(format);
    final gate = _held[format] = Completer<void>();
    await gate.future;
    return ResponseBody.fromString(
      jsonEncode({
        'status': 'requested',
        'book_formats': {format: 'requested'},
      }),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  void release(String format) => _held[format]!.complete();

  @override
  void close({bool force = false}) {}
}

void _concurrencyTests() {
  testWidgets(
      'submitting one format leaves the other row tappable and both requests fly',
      (tester) async {
    final adapter = _HeldSubmitAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookFormatPanel(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    // Fire the eBook request and leave it in flight.
    await tester.tap(_row('ebook'));
    for (var attempt = 0;
        attempt < 50 && adapter.submissionOrder.isEmpty;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(adapter.submissionOrder, ['ebook']);

    // The audiobook row must still act immediately — a shared busy flag here
    // used to swallow this tap without any feedback.
    await tester.tap(_row('audiobook'));
    for (var attempt = 0;
        attempt < 50 && adapter.submissionOrder.length < 2;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    expect(adapter.submissionOrder, ['ebook', 'audiobook'],
        reason: 'the second format must submit while the first is in flight');

    adapter.release('ebook');
    adapter.release('audiobook');
    await tester.pumpAndSettle();

    // Both rows settled into their post-request state.
    expect(
      find.descendant(of: _row('ebook'), matching: find.text('Requested')),
      findsOneWidget,
    );
    expect(
      find.descendant(of: _row('audiobook'), matching: find.text('Requested')),
      findsOneWidget,
    );
  });

  testWidgets('a second tap on the in-flight format itself is ignored',
      (tester) async {
    final adapter = _HeldSubmitAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: BookFormatPanel(
            foreignId: 'fb',
            title: 'Flock',
            service: RequestService(backendDio: dio),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(_row('ebook'));
    for (var attempt = 0;
        attempt < 50 && adapter.submissionOrder.isEmpty;
        attempt++) {
      await tester.pump(const Duration(milliseconds: 1));
    }
    await tester.tap(_row('ebook'), warnIfMissed: false);
    await tester.pump(const Duration(milliseconds: 5));
    expect(adapter.submissionOrder, ['ebook'],
        reason: 'the same format must never double-submit');

    adapter.release('ebook');
    await tester.pumpAndSettle();
  });
}
