import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_models.dart';
import 'package:cantinarr/features/config_changes/data/config_change_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses a structured configuration-change SSE receipt', () async {
    final adapter = _SseAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final events = await AiChatService(backendDio: dio, isWeb: false)
        .sendMessage(messages: const [])
        .toList();

    final receipt = events.whereType<ConfigurationChangeEvent>().single.change;
    expect(receipt.id, 42);
    expect(receipt.status, ConfigChangeStatus.applied);
    expect(receipt.resourceName, 'Very High (4K)');
    expect(receipt.changes.single.after, '+100');
  });

  test('disables the whole-request timeout for browser chat streams',
      () async {
    final adapter = _SseAdapter();
    final dio = Dio(BaseOptions(
      baseUrl: 'http://localhost',
      connectTimeout: const Duration(seconds: 15),
      receiveTimeout: const Duration(seconds: 15),
    ))..httpClientAdapter = adapter;

    await AiChatService(backendDio: dio, isWeb: true)
        .sendMessage(messages: const [])
        .toList();

    expect(adapter.requests.single.connectTimeout, Duration.zero);
    expect(adapter.requests.single.receiveTimeout, Duration.zero);
  });

  test('retains the connection timeout for native chat streams', () async {
    final adapter = _SseAdapter();
    final dio = Dio(BaseOptions(
      baseUrl: 'http://localhost',
      connectTimeout: const Duration(seconds: 15),
      receiveTimeout: const Duration(seconds: 15),
    ))..httpClientAdapter = adapter;

    await AiChatService(backendDio: dio, isWeb: false)
        .sendMessage(messages: const [])
        .toList();

    expect(adapter.requests.single.connectTimeout,
        const Duration(seconds: 15));
    expect(adapter.requests.single.receiveTimeout, Duration.zero);
  });
}

class _SseAdapter implements HttpClientAdapter {
  final requests = <RequestOptions>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    final receipt = {
      'id': 42,
      'actor_user_id': 1,
      'actor_name': 'Alex',
      'source': 'ai_chat',
      'service_type': 'sonarr',
      'instance_id': 'sonarr-main',
      'instance_name': 'Main Sonarr',
      'resource_type': 'quality_profile',
      'resource_id': '7',
      'resource_name': 'Very High (4K)',
      'operation': 'update',
      'status': 'applied',
      'summary': 'Prefer English releases',
      'changes': [
        {
          'key': 'english_score',
          'label': 'English',
          'before': '0',
          'after': '+100',
        },
      ],
      'created_at': '2026-07-20T21:57:00Z',
    };
    final body = 'data: ${jsonEncode({'configuration_change': receipt})}\n\n'
        'data: [DONE]\n\n';
    return ResponseBody.fromString(
      body,
      200,
      headers: {
        'content-type': ['text/event-stream'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
