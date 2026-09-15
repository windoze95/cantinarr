import 'package:cantinarr/core/network/api_error_message.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// The one place a failed backend call becomes a line a person reads: the
/// server's `{ "error": ... }` reason wins whether Dio decoded it or (as
/// Go's `http.Error` labels JSON bodies text/plain) left it as a string; a
/// short plain-text body passes through; HTML and oversized bodies fall back
/// to Dio's own description.

DioException _failure(Object? body, {String? message}) {
  final options = RequestOptions(path: '/api/admin/outbound-proxy/test');
  return DioException(
    requestOptions: options,
    response: Response(requestOptions: options, statusCode: 400, data: body),
    message: message,
  );
}

void main() {
  test('reads the error field of a decoded JSON body', () {
    expect(
      apiErrorMessage(_failure({'error': 'proxy address is required'})),
      'proxy address is required',
    );
  });

  test('decodes a JSON envelope Dio left as text', () {
    expect(
      apiErrorMessage(_failure(
        '{"error":"proxy test failed: dial tcp: connection refused"}\n',
      )),
      'proxy test failed: dial tcp: connection refused',
    );
  });

  test('passes a short plain-text body through', () {
    expect(
      apiErrorMessage(_failure('404 page not found\n')),
      '404 page not found',
    );
  });

  test("refuses an HTML body and falls back to Dio's message", () {
    expect(
      apiErrorMessage(_failure(
        '<html><body><h1>502 Bad Gateway</h1></body></html>',
        message: 'Bad response',
      )),
      'Bad response',
    );
  });

  test('refuses a plain-text body longer than 500 characters', () {
    expect(
      apiErrorMessage(_failure('x' * 501, message: 'Bad response')),
      'Bad response',
    );
  });

  test('treats a blank error field as no reason', () {
    expect(
      apiErrorMessage(_failure({'error': '   '}, message: 'Bad response')),
      'Bad response',
    );
  });

  test("uses the exception's own text when Dio carries no message", () {
    final e = _failure(null);
    expect(apiErrorMessage(e), e.toString());
  });

  test('uses toString for anything that is not a Dio failure', () {
    expect(apiErrorMessage(StateError('boom')), 'Bad state: boom');
  });
}
