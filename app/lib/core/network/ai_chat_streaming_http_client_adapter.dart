import 'dart:async';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:http/http.dart' as http;

/// Routes the AI chat SSE request through `package:http` so browser responses
/// are delivered incrementally by Fetch's `ReadableStream` implementation.
///
/// Every other request stays on Dio's normal platform adapter. This keeps the
/// streaming transport narrowly scoped while preserving the existing client
/// behavior for the rest of the API.
class AiChatStreamingHttpClientAdapter implements HttpClientAdapter {
  final HttpClientAdapter _fallbackAdapter;
  final http.Client _streamingClient;

  AiChatStreamingHttpClientAdapter({
    required HttpClientAdapter fallbackAdapter,
    http.Client? streamingClient,
  })  : _fallbackAdapter = fallbackAdapter,
        _streamingClient = streamingClient ?? http.Client();

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) {
    if (!_isAiChatStream(options)) {
      return _fallbackAdapter.fetch(options, requestStream, cancelFuture);
    }
    return _fetchAiChatStream(options, requestStream, cancelFuture);
  }

  bool _isAiChatStream(RequestOptions options) {
    final path = Uri.tryParse(options.path)?.path ?? options.path;
    return path == '/api/ai/chat' &&
        options.responseType == ResponseType.stream;
  }

  Future<ResponseBody> _fetchAiChatStream(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    var cancelled = false;
    var connectTimedOut = false;
    final abort = Completer<void>();

    cancelFuture?.then(
      (_) {
        cancelled = true;
        if (!abort.isCompleted) abort.complete();
      },
      onError: (_, __) {
        cancelled = true;
        if (!abort.isCompleted) abort.complete();
      },
    );

    final body = await _readRequestBody(
      requestStream,
      options,
      () => cancelled,
    );
    if (cancelled) throw _cancelled(options);

    final request = http.AbortableRequest(
      options.method,
      options.uri,
      abortTrigger: abort.future,
    )
      ..bodyBytes = body
      ..headers.addAll(_requestHeaders(options.headers));

    request.followRedirects = options.followRedirects;
    request.maxRedirects = options.maxRedirects;
    request.persistentConnection = options.persistentConnection;

    final connectTimeout = options.connectTimeout;
    Timer? connectTimer;
    if (connectTimeout != null && connectTimeout > Duration.zero) {
      connectTimer = Timer(connectTimeout, () {
        connectTimedOut = true;
        if (!abort.isCompleted) abort.complete();
      });
    }

    final http.StreamedResponse response;
    try {
      response = await _streamingClient.send(request);
    } catch (error, stackTrace) {
      connectTimer?.cancel();
      Error.throwWithStackTrace(
        _transportException(
          options,
          cancelled: cancelled,
          connectTimedOut: connectTimedOut,
        ),
        stackTrace,
      );
    }
    connectTimer?.cancel();

    if (cancelled) throw _cancelled(options);
    if (connectTimedOut) {
      throw DioException.connectionTimeout(
        timeout: connectTimeout!,
        requestOptions: options,
      );
    }

    return ResponseBody(
      _responseStream(
        response.stream,
        options,
        isCancelled: () => cancelled,
      ),
      response.statusCode,
      statusMessage: response.reasonPhrase,
      isRedirect: response.isRedirect,
      headers: {
        for (final header in response.headers.entries)
          header.key: [header.value],
      },
      onClose: () {
        if (!abort.isCompleted) abort.complete();
      },
    );
  }

  Future<Uint8List> _readRequestBody(
    Stream<Uint8List>? requestStream,
    RequestOptions options,
    bool Function() isCancelled,
  ) async {
    if (requestStream == null) return Uint8List(0);

    final bytes = BytesBuilder(copy: false);
    try {
      await for (final chunk in requestStream) {
        if (isCancelled()) throw _cancelled(options);
        bytes.add(chunk);
      }
    } on DioException {
      rethrow;
    } catch (_) {
      throw DioException.connectionError(
        requestOptions: options,
        reason: 'The AI chat request body could not be sent.',
      );
    }
    return bytes.takeBytes();
  }

  Stream<Uint8List> _responseStream(
    Stream<List<int>> source,
    RequestOptions options, {
    required bool Function() isCancelled,
  }) async* {
    try {
      await for (final chunk in source) {
        yield chunk is Uint8List ? chunk : Uint8List.fromList(chunk);
      }
    } catch (error, stackTrace) {
      Error.throwWithStackTrace(
        isCancelled()
            ? _cancelled(options)
            : DioException.connectionError(
                requestOptions: options,
                reason: 'The AI chat response stream was interrupted.',
              ),
        stackTrace,
      );
    }
  }

  Map<String, String> _requestHeaders(Map<String, dynamic> headers) => {
        for (final header in headers.entries)
          if (header.value != null &&
              header.key.toLowerCase() != Headers.contentLengthHeader)
            header.key: header.value is Iterable
                ? (header.value as Iterable)
                    .map((value) => value.toString())
                    .join(', ')
                : header.value.toString(),
      };

  DioException _transportException(
    RequestOptions options, {
    required bool cancelled,
    required bool connectTimedOut,
  }) {
    if (cancelled) return _cancelled(options);
    if (connectTimedOut) {
      return DioException.connectionTimeout(
        timeout: options.connectTimeout!,
        requestOptions: options,
      );
    }
    return DioException.connectionError(
      requestOptions: options,
      reason: 'The AI chat connection could not be established.',
    );
  }

  DioException _cancelled(RequestOptions options) =>
      DioException.requestCancelled(
        requestOptions: options,
        reason: 'The AI chat request was cancelled.',
      );

  @override
  void close({bool force = false}) {
    _streamingClient.close();
    _fallbackAdapter.close(force: force);
  }
}
