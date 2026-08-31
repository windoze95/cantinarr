import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/ai_assistant/data/grok_oauth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses connection identity and plan metadata', () {
    final status = GrokConnectionStatus.fromJson({
      'selected': true,
      'available': true,
      'connected': true,
      'account_email': 'viewer@example.com',
      'plan_type': 'supergrok',
      'updated_at': '2026-08-15T18:30:00Z',
    });

    expect(status.selected, isTrue);
    expect(status.available, isTrue);
    expect(status.connected, isTrue);
    expect(status.accountEmail, 'viewer@example.com');
    expect(status.planType, 'supergrok');
    expect(status.updatedAt, DateTime.utc(2026, 8, 15, 18, 30));
  });

  test('keeps selected and runtime availability distinct', () {
    final status = GrokConnectionStatus.fromJson({
      'selected': true,
      'available': false,
      'connected': false,
    });

    expect(status.selected, isTrue);
    expect(status.available, isFalse);
  });

  test('parses device flow timing and account result', () {
    final flow = GrokDeviceAuthorization.fromJson({
      'flow_id': 'flow-1',
      'verification_uri': 'https://accounts.x.ai/oauth2/device?user_code=GROK',
      'user_code': 'GROK-1234',
      'expires_in': 900,
      'interval': 3,
    });
    final result = GrokDeviceFlowResult.fromJson({
      'status': 'connected',
      'account': {'email': 'viewer@example.com'},
    });

    expect(flow.flowId, 'flow-1');
    expect(flow.userCode, 'GROK-1234');
    expect(flow.expiresIn, const Duration(minutes: 15));
    expect(flow.pollInterval, const Duration(seconds: 3));
    expect(result.status, GrokDeviceFlowStatus.connected);
    expect(result.accountEmail, 'viewer@example.com');
  });

  test('accepts both xAI sign-in hosts and nothing else', () {
    for (final url in [
      'https://auth.x.ai/oauth2/device',
      'https://accounts.x.ai/oauth2/device?user_code=GROK',
    ]) {
      expect(
        GrokDeviceAuthorization.fromJson({
          'flow_id': 'flow-1',
          'verification_uri': url,
          'user_code': 'GROK-1234',
        }).verificationUri.toString(),
        url,
        reason: url,
      );
    }
    for (final url in [
      'http://auth.x.ai/oauth2/device',
      'https://auth.x.ai.evil.example/device',
      'https://viewer@accounts.x.ai/device',
      'https://accounts.x.ai:8443/device',
      'https://auth.openai.com/codex/device',
    ]) {
      expect(
        () => GrokDeviceAuthorization.fromJson({
          'flow_id': 'flow-1',
          'verification_uri': url,
          'user_code': 'GROK-1234',
        }),
        throwsFormatException,
        reason: url,
      );
    }
  });

  test('calls the user-scoped status, device, cancel, and unlink routes',
      () async {
    final adapter = _GrokAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;
    final service = GrokOAuthService(backendDio: dio);

    await service.getStatus();
    await service.beginDeviceAuthorization();
    await service.checkDeviceAuthorization('flow-1');
    await service.cancelDeviceAuthorization('flow-1');
    await service.unlink();

    expect(
      adapter.requests
          .map((request) => '${request.method} ${request.path}')
          .toList(),
      [
        'GET /api/ai/grok/status',
        'POST /api/ai/grok/device/begin',
        'GET /api/ai/grok/device/flow-1',
        'DELETE /api/ai/grok/device/flow-1',
        'DELETE /api/ai/grok',
      ],
    );
  });

  test('uses the admin routes for the shared scope', () async {
    final adapter = _GrokAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;
    final service = GrokOAuthService(
      backendDio: dio,
      scope: GrokOAuthScope.adminShared,
    );

    await service.getStatus();

    expect(
      adapter.requests.single.path,
      '/api/admin/ai/grok/status',
    );
  });

  test('maps a server-expired device flow to an expired result', () async {
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _GrokAdapter(checkExpired: true);

    final result = await GrokOAuthService(
      backendDio: dio,
    ).checkDeviceAuthorization('flow-1');

    expect(result.status, GrokDeviceFlowStatus.expired);
    expect(result.error, 'xAI sign-in expired; start again');
  });

  test('preserves a connected account model-validation failure', () async {
    final adapter = _GrokAdapter(validationFailed: true);
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    final result = await GrokOAuthService(
      backendDio: dio,
    ).checkDeviceAuthorization('flow-1');

    expect(result.status, GrokDeviceFlowStatus.failed);
    expect(result.error, contains('selected model'));
    expect(
      adapter.requests.single.receiveTimeout,
      const Duration(seconds: 75),
    );
  });
}

class _GrokAdapter implements HttpClientAdapter {
  _GrokAdapter({this.checkExpired = false, this.validationFailed = false});

  final bool checkExpired;
  final bool validationFailed;
  final requests = <RequestOptions>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    final isCheck = options.method == 'GET' &&
        (options.path == '/api/ai/grok/device/flow-1' ||
            options.path == '/api/admin/ai/grok/device/flow-1');
    if (checkExpired && isCheck) {
      return ResponseBody.fromString(
        jsonEncode({'error': 'xAI sign-in expired; start again'}),
        410,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    if (validationFailed && isCheck) {
      return ResponseBody.fromString(
        jsonEncode({
          'error':
              'xAI connected, but the selected model could not complete a test message',
        }),
        422,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    final body = switch ((options.method, options.path)) {
      ('GET', '/api/ai/grok/status') ||
      ('GET', '/api/admin/ai/grok/status') =>
        {
          'available': true,
          'connected': false,
        },
      ('POST', '/api/ai/grok/device/begin') => {
          'flow_id': 'flow-1',
          'verification_uri':
              'https://accounts.x.ai/oauth2/device?user_code=GROK-1234',
          'user_code': 'GROK-1234',
          'expires_in': 900,
          'interval': 5,
        },
      ('GET', '/api/ai/grok/device/flow-1') => {
          'status': 'pending',
        },
      _ => const <String, dynamic>{},
    };
    return ResponseBody.fromString(
      jsonEncode(body),
      options.method == 'DELETE' ? 204 : 200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
