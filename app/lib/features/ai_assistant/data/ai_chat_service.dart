import 'dart:async';
import 'dart:convert';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import '../../config_changes/data/config_change_models.dart';
import 'ai_models.dart';

/// Client for the Cantinarr backend AI chat endpoint.
///
/// Uses SSE streaming for real-time response delivery.
class AiChatService {
  final Dio _backendDio;
  final bool _isWeb;

  AiChatService({required Dio backendDio, bool? isWeb})
      : _backendDio = backendDio,
        _isWeb = isWeb ?? kIsWeb;

  /// Send messages and stream response events via SSE.
  ///
  /// Pass the [conversationId] from a previous [ConversationIdEvent] so the
  /// server can keep full tool context across turns. The transcript in
  /// [messages] is still sent as a fallback for when server state expired.
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
    String? conversationId,
  }) async* {
    final apiMessages = messages
        .where((m) => m.role != ChatRole.system)
        .map((m) => m.toApiMessage())
        .toList();

    final resp = await _backendDio.post(
      '/api/ai/chat',
      data: {
        'messages': apiMessages,
        if (conversationId != null && conversationId.isNotEmpty)
          'conversation_id': conversationId,
      },
      options: Options(
        responseType: ResponseType.stream,
        // Agent thinking and slow tools can legitimately run longer than the
        // normal request timeout. dio_web_adapter implements XMLHttpRequest's
        // whole-request timeout as connectTimeout + receiveTimeout, so leaving
        // the inherited 15s connect timeout beside a zero receive timeout
        // still aborts browser chat after 15s and reports a misleading zero
        // receive timeout. Disable both browser XHR components; native keeps
        // its bounded connection setup while the server's SSE keepalives cover
        // inter-chunk inactivity.
        connectTimeout:
            _isWeb ? Duration.zero : _backendDio.options.connectTimeout,
        receiveTimeout: Duration.zero,
        headers: {'Accept': 'text/event-stream'},
      ),
    );

    final stream = resp.data.stream as Stream<List<int>>;
    String buffer = '';

    await for (final chunk in stream) {
      buffer += utf8.decode(chunk);

      // Parse SSE events from the buffer
      while (buffer.contains('\n\n')) {
        final eventEnd = buffer.indexOf('\n\n');
        final event = buffer.substring(0, eventEnd);
        buffer = buffer.substring(eventEnd + 2);

        for (final line in event.split('\n')) {
          if (line.startsWith('data: ')) {
            final data = line.substring(6);
            if (data == '[DONE]') return;

            try {
              final json = jsonDecode(data) as Map<String, dynamic>;
              if (json.containsKey('text')) {
                final text = json['text'] as String?;
                if (text != null && text.isNotEmpty) {
                  yield TextChunkEvent(text);
                }
              } else if (json.containsKey('conversation_id')) {
                final id = json['conversation_id'] as String?;
                if (id != null && id.isNotEmpty) {
                  yield ConversationIdEvent(id);
                }
              } else if (json.containsKey('tool_start')) {
                final tool = json['tool_start'] as Map<String, dynamic>;
                final name = tool['name'] as String? ?? '';
                final label = tool['label'] as String? ?? _humanize(name);
                yield ToolStartEvent(name, label);
              } else if (json.containsKey('tool_end')) {
                final tool = json['tool_end'] as Map<String, dynamic>;
                yield ToolEndEvent(
                  tool['name'] as String? ?? '',
                  tool['ok'] != false,
                );
              } else if (json.containsKey('media_results')) {
                final items = (json['media_results'] as List)
                    .map((e) =>
                        MediaResultItem.fromJson(e as Map<String, dynamic>))
                    .toList();
                if (items.isNotEmpty) {
                  yield MediaResultsEvent(items);
                }
              } else if (json.containsKey('configuration_change')) {
                final raw = json['configuration_change'];
                if (raw is Map) {
                  yield ConfigurationChangeEvent(
                    ConfigChange.fromJson(
                      raw.map(
                        (key, value) => MapEntry(key.toString(), value),
                      ),
                    ),
                  );
                }
              } else if (json.containsKey('error')) {
                final message = json['error'] as String?;
                yield StreamErrorEvent(
                  (message == null || message.isEmpty)
                      ? 'Something went wrong.'
                      : message,
                );
              }
            } catch (_) {
              // If it's not JSON, yield the raw data as text
              if (data.isNotEmpty) yield TextChunkEvent(data);
            }
          }
        }
      }
    }
  }

  /// Turns a snake_case tool name into a readable label.
  static String _humanize(String name) {
    if (name.isEmpty) return 'Working';
    return name
        .split('_')
        .where((w) => w.isNotEmpty)
        .map((w) => w[0].toUpperCase() + w.substring(1))
        .join(' ');
  }

  /// Check if the AI service is available on this server.
  Future<bool> isAvailable() async {
    try {
      final resp = await _backendDio.get('/api/ai/available');
      return (resp.data as Map<String, dynamic>)['available'] == true;
    } catch (_) {
      return false;
    }
  }
}
