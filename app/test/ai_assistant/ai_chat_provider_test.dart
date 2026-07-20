import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_models.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('shows safe guidance when an assistant response times out', () async {
    final notifier = AiChatNotifier(
      chatService: _FailingAiChatService(
        DioException.receiveTimeout(
          timeout: Duration.zero,
          requestOptions: RequestOptions(path: '/api/ai/chat'),
        ),
      ),
    );
    addTearDown(notifier.dispose);

    await notifier.sendMessage('update every Sonarr profile');

    final errorText = notifier.state.messages.last.errorText;
    expect(
      errorText,
      'The assistant did not finish responding in time. If this request '
      'could change settings, check Configuration History before retrying.',
    );
    expect(errorText, isNot(contains('DioException')));
    expect(errorText, isNot(contains('0:00:00')));
    expect(notifier.state.isLoading, isFalse);
  });

  test('shows safe guidance when Cantinarr cannot reach the assistant',
      () async {
    final notifier = AiChatNotifier(
      chatService: _FailingAiChatService(
        DioException(
          requestOptions: RequestOptions(path: '/api/ai/chat'),
          type: DioExceptionType.connectionError,
          error: 'private transport details',
        ),
      ),
    );
    addTearDown(notifier.dispose);

    await notifier.sendMessage('find a movie');

    expect(
      notifier.state.messages.last.errorText,
      'Cantinarr could not reach the assistant. Check your connection, '
      'then try again.',
    );
    expect(
      notifier.state.messages.last.errorText,
      isNot(contains('private transport details')),
    );
  });

  test('shows rate-limit guidance without exposing the response', () async {
    final request = RequestOptions(path: '/api/ai/chat');
    final notifier = AiChatNotifier(
      chatService: _FailingAiChatService(
        DioException.badResponse(
          statusCode: 429,
          requestOptions: request,
          response: Response(
            requestOptions: request,
            statusCode: 429,
            data: {'error': 'provider account details'},
          ),
        ),
      ),
    );
    addTearDown(notifier.dispose);

    await notifier.sendMessage('find a show');

    expect(
      notifier.state.messages.last.errorText,
      'The assistant is busy. Wait a moment, then try again.',
    );
    expect(
      notifier.state.messages.last.errorText,
      isNot(contains('provider account details')),
    );
  });
}

class _FailingAiChatService extends AiChatService {
  final Object failure;

  _FailingAiChatService(this.failure)
      : super(backendDio: Dio(), isWeb: false);

  @override
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
    String? conversationId,
  }) =>
      Stream.error(failure);
}
