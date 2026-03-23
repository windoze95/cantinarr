import 'dart:async';
import 'dart:convert';
import 'package:dio/dio.dart';
import 'ai_models.dart';

/// Client for the Cantinarr backend AI chat endpoint.
///
/// Uses SSE streaming for real-time response delivery.
class AiChatService {
  final Dio _backendDio;

  AiChatService({required Dio backendDio}) : _backendDio = backendDio;

  /// Send messages and stream response events (text chunks + media results) via SSE.
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
  }) async* {
    final apiMessages = messages
        .where((m) => m.role != ChatRole.system)
        .map((m) => m.toApiMessage())
        .toList();

    final resp = await _backendDio.post(
      '/api/ai/chat',
      data: {'messages': apiMessages},
      options: Options(
        responseType: ResponseType.stream,
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
              } else if (json.containsKey('media_results')) {
                final items = (json['media_results'] as List)
                    .map((e) =>
                        MediaResultItem.fromJson(e as Map<String, dynamic>))
                    .toList();
                if (items.isNotEmpty) {
                  yield MediaResultsEvent(items);
                }
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
