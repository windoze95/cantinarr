import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/ai_chat_streaming_http_client_adapter.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  test('delivers response chunks before the body completes', () async {
    final body = StreamController<List<int>>();
    final client = _RecordingClient((_) async => http.StreamedResponse(
          body.stream,
          200,
          headers: {'content-type': 'text/event-stream'},
        ));
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: _FallbackAdapter(),
      streamingClient: client,
    );
    final response = await adapter.fetch(
      _chatOptions(connectTimeout: const Duration(milliseconds: 1)),
      null,
      null,
    );
    final chunks = <String>[];
    final firstChunk = Completer<void>();
    final complete = Completer<void>();
    response.stream.listen(
      (chunk) {
        chunks.add(utf8.decode(chunk));
        if (!firstChunk.isCompleted) firstChunk.complete();
      },
      onDone: complete.complete,
    );

    await Future<void>.delayed(const Duration(milliseconds: 10));
    body.add(utf8.encode('first'));
    await firstChunk.future;

    expect(chunks, ['first']);
    expect(complete.isCompleted, isFalse);

    body.add(utf8.encode('second'));
    await body.close();
    await complete.future;
    expect(chunks, ['first', 'second']);
  });

  test('delegates every request outside the AI chat stream', () async {
    final fallback = _FallbackAdapter();
    final client = _RecordingClient((_) async =>
        http.StreamedResponse(const Stream.empty(), 200));
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: fallback,
      streamingClient: client,
    );
    final requestStream = Stream.value(Uint8List.fromList([1]));
    final cancel = Completer<void>();
    final otherPath = RequestOptions(
      path: '/api/other',
      baseUrl: 'http://localhost',
      responseType: ResponseType.stream,
    );
    final nonStreamingChat = RequestOptions(
      path: '/api/ai/chat',
      baseUrl: 'http://localhost',
      responseType: ResponseType.json,
    );

    await adapter.fetch(otherPath, requestStream, cancel.future);
    await adapter.fetch(nonStreamingChat, null, null);

    expect(client.requests, isEmpty);
    expect(fallback.requests, hasLength(2));
    expect(identical(fallback.requests.first.options, otherPath), isTrue);
    expect(identical(fallback.requests.first.requestStream, requestStream),
        isTrue);
    expect(identical(fallback.requests.first.cancelFuture, cancel.future),
        isTrue);
    expect(identical(fallback.requests.last.options, nonStreamingChat),
        isTrue);
  });

  test('forwards URL, method, headers, body, and response metadata', () async {
    late http.AbortableRequest sent;
    final client = _RecordingClient((request) async {
      sent = request as http.AbortableRequest;
      return http.StreamedResponse(
        Stream.value(utf8.encode('accepted')),
        207,
        headers: {
          'content-type': 'text/event-stream',
          'x-response': 'preserved',
        },
        isRedirect: true,
        reasonPhrase: 'Multi-Status',
      );
    });
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: _FallbackAdapter(),
      streamingClient: client,
    );
    final options = _chatOptions().copyWith(
      method: 'POST',
      queryParameters: {'conversation': '42'},
      headers: {
        'Authorization': 'Bearer access-token',
        'Content-Type': 'application/json',
        'X-Values': ['one', 'two'],
      },
      followRedirects: false,
      maxRedirects: 2,
      persistentConnection: false,
    );
    final response = await adapter.fetch(
      options,
      Stream.fromIterable([
        Uint8List.fromList(utf8.encode('{"message":')),
        Uint8List.fromList(utf8.encode('"hello"}')),
      ]),
      null,
    );

    expect(sent.method, 'POST');
    expect(sent.url.toString(),
        'http://localhost/api/ai/chat?conversation=42');
    expect(sent.headers['authorization'], 'Bearer access-token');
    expect(sent.headers['content-type'], 'application/json');
    expect(sent.headers['x-values'], 'one, two');
    expect(utf8.decode(sent.bodyBytes), '{"message":"hello"}');
    expect(sent.followRedirects, isFalse);
    expect(sent.maxRedirects, 2);
    expect(sent.persistentConnection, isFalse);
    expect(response.statusCode, 207);
    expect(response.statusMessage, 'Multi-Status');
    expect(response.isRedirect, isTrue);
    expect(response.headers['content-type'], ['text/event-stream']);
    expect(response.headers['x-response'], ['preserved']);
    expect(utf8.decode((await response.stream.toList()).single), 'accepted');
  });

  test('keeps Dio request interceptors on the Fetch streaming path',
      () async {
    late http.AbortableRequest sent;
    final client = _RecordingClient((request) async {
      sent = request as http.AbortableRequest;
      return http.StreamedResponse(
        Stream.value(utf8.encode('data: [DONE]\n\n')),
        200,
        headers: {'content-type': 'text/event-stream'},
      );
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = AiChatStreamingHttpClientAdapter(
        fallbackAdapter: _FallbackAdapter(),
        streamingClient: client,
      )
      ..interceptors.add(InterceptorsWrapper(
        onRequest: (options, handler) {
          options.headers['Authorization'] = 'Bearer current-access-token';
          handler.next(options);
        },
      ));

    final response = await dio.post(
      '/api/ai/chat',
      data: {'messages': []},
      options: Options(responseType: ResponseType.stream),
    );
    await (response.data.stream as Stream<List<int>>).drain<void>();

    expect(sent.headers['authorization'], 'Bearer current-access-token');
  });

  test('aborts and reports cancellation while waiting for headers', () async {
    final requestStarted = Completer<void>();
    final client = _RecordingClient((request) async {
      requestStarted.complete();
      await (request as http.AbortableRequest).abortTrigger;
      throw http.RequestAbortedException(request.url);
    });
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: _FallbackAdapter(),
      streamingClient: client,
    );
    final cancel = Completer<void>();
    final response = adapter.fetch(_chatOptions(), null, cancel.future);
    await requestStarted.future;

    cancel.complete();

    await expectLater(
      response,
      throwsA(isA<DioException>().having(
        (error) => error.type,
        'type',
        DioExceptionType.cancel,
      )),
    );
  });

  test('reports a connection timeout only while waiting for headers',
      () async {
    final client = _RecordingClient((request) async {
      await (request as http.AbortableRequest).abortTrigger;
      throw http.RequestAbortedException(request.url);
    });
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: _FallbackAdapter(),
      streamingClient: client,
    );

    await expectLater(
      adapter.fetch(
        _chatOptions(connectTimeout: const Duration(milliseconds: 5)),
        null,
        null,
      ),
      throwsA(isA<DioException>().having(
        (error) => error.type,
        'type',
        DioExceptionType.connectionTimeout,
      )),
    );
  });

  test('closes both transports and preserves the force flag', () {
    final fallback = _FallbackAdapter();
    final client = _RecordingClient((_) async =>
        http.StreamedResponse(const Stream.empty(), 200));
    final adapter = AiChatStreamingHttpClientAdapter(
      fallbackAdapter: fallback,
      streamingClient: client,
    );

    adapter.close(force: true);

    expect(client.closed, isTrue);
    expect(fallback.closed, isTrue);
    expect(fallback.forceClosed, isTrue);
  });
}

RequestOptions _chatOptions({Duration? connectTimeout}) => RequestOptions(
      path: '/api/ai/chat',
      baseUrl: 'http://localhost',
      method: 'POST',
      responseType: ResponseType.stream,
      connectTimeout: connectTimeout,
    );

class _RecordingClient extends http.BaseClient {
  final Future<http.StreamedResponse> Function(http.BaseRequest request)
      _send;
  final requests = <http.BaseRequest>[];
  bool closed = false;

  _RecordingClient(this._send);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    requests.add(request);
    return _send(request);
  }

  @override
  void close() {
    closed = true;
  }
}

class _FallbackRequest {
  final RequestOptions options;
  final Stream<Uint8List>? requestStream;
  final Future<void>? cancelFuture;

  const _FallbackRequest(
    this.options,
    this.requestStream,
    this.cancelFuture,
  );
}

class _FallbackAdapter implements HttpClientAdapter {
  final requests = <_FallbackRequest>[];
  bool closed = false;
  bool forceClosed = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(_FallbackRequest(options, requestStream, cancelFuture));
    return ResponseBody.fromString('{}', 200);
  }

  @override
  void close({bool force = false}) {
    closed = true;
    forceClosed = force;
  }
}
