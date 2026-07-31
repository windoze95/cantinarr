import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/safe_http_log_interceptor.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

class _SecretResponseAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    return ResponseBody.fromString(
      jsonEncode({
        'access_token': 'response-access-secret',
        'cookie': 'response-cookie-secret',
      }),
      401,
      headers: {
        'content-type': ['application/json'],
        'set-cookie': ['session=response-set-cookie-secret'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  test('logger emits metadata without request, response, or URL secrets',
      () async {
    final logs = <String>[];
    var nowMicros = 1000000;
    final dio = Dio()..httpClientAdapter = _SecretResponseAdapter();
    dio.interceptors.add(SafeHttpLogInterceptor(
      logPrint: logs.add,
      nowMicros: () {
        final current = nowMicros;
        nowMicros += 12000;
        return current;
      },
    ));

    await expectLater(
      dio.post<void>(
        'https://authority-user:authority-password@backend.example/'
        'api/admin/issues/42/activity?api_key=query-api-secret&token=query-token-secret',
        data: {
          'api_key': 'request-api-secret',
          'password': 'request-password-secret',
          'refresh_token': 'request-refresh-secret',
        },
        options: Options(headers: {
          'Authorization': 'Bearer header-access-secret',
          'Cookie': 'session=request-cookie-secret',
        }),
      ),
      throwsA(isA<DioException>()),
    );

    expect(logs, hasLength(2));
    expect(logs.first, '[HTTP] -> POST /api/admin/issues/…');
    expect(logs.last, '[HTTP] !! POST /api/admin/issues/… 401 12ms');

    final output = logs.join('\n');
    for (final secret in const [
      'authority-user',
      'authority-password',
      'backend.example',
      'query-api-secret',
      'query-token-secret',
      'request-api-secret',
      'request-password-secret',
      'request-refresh-secret',
      'header-access-secret',
      'request-cookie-secret',
      'response-access-secret',
      'response-cookie-secret',
      'response-set-cookie-secret',
    ]) {
      expect(output, isNot(contains(secret)));
    }
    expect(output, isNot(contains('Authorization')));
    expect(output, isNot(contains('Cookie')));
    expect(output, isNot(contains('api_key')));
  });

  test('path sanitizer never copies dynamic path or query values', () {
    expect(
      SafeHttpLogInterceptor.sanitizedPath(Uri.parse(
        'https://example.test/api/instances/instance-secret/webhook'
        '?password=query-secret',
      )),
      '/api/instances/…',
    );
    expect(
      SafeHttpLogInterceptor.sanitizedPath(Uri.parse(
        'https://example.test/api/admin/agent-actions/action-secret/approve',
      )),
      '/api/admin/agent-actions/…',
    );
    expect(
      SafeHttpLogInterceptor.sanitizedPath(Uri.parse(
        'https://example.test/path-token-secret/private?token=query-secret',
      )),
      '/…',
    );
  });
}
