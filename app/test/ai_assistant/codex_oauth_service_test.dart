import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses connection identity and both Codex usage windows', () {
    final status = CodexConnectionStatus.fromJson({
      'selected': true,
      'available': true,
      'connected': true,
      'account_email': 'viewer@example.com',
      'plan_type': 'plus',
      'updated_at': '2026-07-13T18:30:00Z',
      'stale': true,
      'rate_limits': {
        'primary': {
          'used_percent': 42.5,
          'resets_at': 2000000000,
        },
        'secondary': {
          'used_percent': 81,
          'resets_at': null,
        },
      },
    });

    expect(status.selected, isTrue);
    expect(status.available, isTrue);
    expect(status.connected, isTrue);
    expect(status.accountEmail, 'viewer@example.com');
    expect(status.planType, 'plus');
    expect(status.updatedAt, DateTime.utc(2026, 7, 13, 18, 30));
    expect(status.stale, isTrue);
    expect(status.rateLimits!.primary!.usedPercent, 42.5);
    expect(
      status.rateLimits!.primary!.resetsAt,
      DateTime.fromMillisecondsSinceEpoch(2000000000000, isUtc: true),
    );
    expect(status.rateLimits!.secondary!.usedPercent, 81);
    expect(status.rateLimits!.secondary!.resetsAt, isNull);
  });

  test('keeps selected and runtime availability distinct', () {
    final status = CodexConnectionStatus.fromJson({
      'selected': true,
      'available': false,
      'connected': false,
    });

    expect(status.selected, isTrue);
    expect(status.available, isFalse);
  });

  test('parses device flow timing and account result', () {
    final flow = CodexDeviceAuthorization.fromJson({
      'flow_id': 'flow-1',
      'verification_uri': 'https://auth.openai.com/codex/device',
      'user_code': 'ABCD-EFGH',
      'expires_in': 900,
      'interval': 3,
    });
    final result = CodexDeviceFlowResult.fromJson({
      'status': 'connected',
      'account': {'email': 'viewer@example.com'},
    });

    expect(flow.flowId, 'flow-1');
    expect(flow.userCode, 'ABCD-EFGH');
    expect(flow.expiresIn, const Duration(minutes: 15));
    expect(flow.pollInterval, const Duration(seconds: 3));
    expect(result.status, CodexDeviceFlowStatus.connected);
    expect(result.accountEmail, 'viewer@example.com');
  });

  test('rejects a non-HTTPS verification URL', () {
    expect(
      () => CodexDeviceAuthorization.fromJson({
        'flow_id': 'flow-1',
        'verification_uri': 'http://example.com/device',
        'user_code': 'ABCD-EFGH',
      }),
      throwsFormatException,
    );
  });

  test('rejects verification URLs outside the exact trusted OpenAI origin', () {
    for (final url in [
      'https://auth.openai.com.evil.example/device',
      'https://viewer@auth.openai.com/device',
      'https://auth.openai.com:443/device',
    ]) {
      expect(
        () => CodexDeviceAuthorization.fromJson({
          'flow_id': 'flow-1',
          'verification_uri': url,
          'user_code': 'ABCD-EFGH',
        }),
        throwsFormatException,
        reason: url,
      );
    }
  });

  test('calls the user-scoped status, device, cancel, and unlink routes',
      () async {
    final adapter = _CodexAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;
    final service = CodexOAuthService(backendDio: dio);

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
        'GET /api/ai/codex/status',
        'POST /api/ai/codex/device/begin',
        'GET /api/ai/codex/device/flow-1',
        'DELETE /api/ai/codex/device/flow-1',
        'DELETE /api/ai/codex',
      ],
    );
  });

  test('maps a server-expired device flow to an expired result', () async {
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CodexAdapter(checkExpired: true);

    final result = await CodexOAuthService(
      backendDio: dio,
    ).checkDeviceAuthorization('flow-1');

    expect(result.status, CodexDeviceFlowStatus.expired);
    expect(result.error, 'ChatGPT sign-in expired; start again');
  });
}

class _CodexAdapter implements HttpClientAdapter {
  _CodexAdapter({this.checkExpired = false});

  final bool checkExpired;
  final requests = <RequestOptions>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    if (checkExpired &&
        options.method == 'GET' &&
        options.path == '/api/ai/codex/device/flow-1') {
      return ResponseBody.fromString(
        jsonEncode({'error': 'ChatGPT sign-in expired; start again'}),
        410,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    final body = switch ((options.method, options.path)) {
      ('GET', '/api/ai/codex/status') => {
          'available': true,
          'connected': false,
        },
      ('POST', '/api/ai/codex/device/begin') => {
          'flow_id': 'flow-1',
          'verification_uri': 'https://auth.openai.com/codex/device',
          'user_code': 'ABCD-EFGH',
          'expires_in': 900,
          'interval': 5,
        },
      ('GET', '/api/ai/codex/device/flow-1') => {
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
