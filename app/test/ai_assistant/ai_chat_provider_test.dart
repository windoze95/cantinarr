import 'dart:async';

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

  test('publishes tool progress before the assistant stream completes',
      () async {
    final service = _ControlledAiChatService();
    final notifier = AiChatNotifier(chatService: service);
    addTearDown(notifier.dispose);

    var completed = false;
    final send = notifier.sendMessage('update my profiles').whenComplete(() {
      completed = true;
    });
    await pumpEventQueue();

    service.events.add(ToolStartEvent(
      'get_quality_profiles',
      'Getting quality profiles',
    ));
    await pumpEventQueue();

    final streamingMessage = notifier.state.messages.last;
    expect(completed, isFalse);
    expect(streamingMessage.isStreaming, isTrue);
    expect(streamingMessage.toolActivity.single.name, 'get_quality_profiles');

    service.events.add(TextChunkEvent('Working on it.'));
    await service.events.close();
    await send;

    expect(notifier.state.messages.last.content, 'Working on it.');
    expect(notifier.state.messages.last.isStreaming, isFalse);
  });

  test('clearing chat aborts the active request and ignores late events',
      () async {
    final service = _ControlledAiChatService();
    final notifier = AiChatNotifier(chatService: service);
    addTearDown(notifier.dispose);

    final send = notifier.sendMessage('change every profile');
    await pumpEventQueue();
    expect(service.cancelToken, isNotNull);
    expect(service.cancelToken!.isCancelled, isFalse);

    notifier.clearChat();

    expect(service.cancelToken!.isCancelled, isTrue);
    service.events.add(ConversationIdEvent('late-conversation-id'));
    service.events.add(TextChunkEvent('late response'));
    await service.events.close();
    await send;

    expect(notifier.state.messages, hasLength(1));
    expect(
      notifier.state.messages.single.content,
      'Chat cleared! What can I help you find?',
    );
    expect(notifier.conversationId, isNull);
    expect(notifier.state.isLoading, isFalse);
  });

  group('ChatMessage display/wire split', () {
    test('toApiMessage prefers wireContent when set', () {
      final message = ChatMessage(
        id: '1',
        role: ChatRole.user,
        content: 'what should I read next',
        wireContent: 'Context: ...\n\nwhat should I read next',
        timestamp: DateTime.now(),
      );

      expect(message.toApiMessage(), {
        'role': 'user',
        'content': 'Context: ...\n\nwhat should I read next',
      });
    });

    test('toApiMessage falls back to content with no wireContent set', () {
      final message = ChatMessage(
        id: '1',
        role: ChatRole.user,
        content: 'find a movie',
        timestamp: DateTime.now(),
      );

      expect(message.toApiMessage(), {
        'role': 'user',
        'content': 'find a movie',
      });
    });

    test('copyWith preserves wireContent when not overridden', () {
      final message = ChatMessage(
        id: '1',
        role: ChatRole.user,
        content: 'raw',
        wireContent: 'framed',
        timestamp: DateTime.now(),
      );

      expect(message.copyWith(isStreaming: false).wireContent, 'framed');
    });

    test('copyWith sets wireContent when overridden', () {
      final message = ChatMessage(
        id: '1',
        role: ChatRole.user,
        content: 'raw',
        timestamp: DateTime.now(),
      );

      expect(message.copyWith(wireContent: 'x').wireContent, 'x');
    });
  });

  group('AiChatNotifier wire override', () {
    test(
        'sendMessage with wireContent stores both values and sends the '
        'wire override', () async {
      final service = _CapturingAiChatService();
      final notifier = AiChatNotifier(chatService: service);
      addTearDown(notifier.dispose);

      await notifier.sendMessage('raw', wireContent: 'framed');

      final sent =
          notifier.state.messages.where((m) => m.role == ChatRole.user).last;
      expect(sent.content, 'raw');
      expect(sent.wireContent, 'framed');
      expect(service.captured.single.last.toApiMessage()['content'], 'framed');
    });

    test('sendMessage without wireContent leaves it null and sends raw text',
        () async {
      final service = _CapturingAiChatService();
      final notifier = AiChatNotifier(chatService: service);
      addTearDown(notifier.dispose);

      await notifier.sendMessage('raw');

      final sent =
          notifier.state.messages.where((m) => m.role == ChatRole.user).last;
      expect(sent.wireContent, isNull);
      expect(service.captured.single.last.toApiMessage()['content'], 'raw');
    });

    test('retryLast re-sends the wire override after a failed turn', () async {
      final service = _CapturingAiChatService(failOnCallIndex: 0);
      final notifier = AiChatNotifier(chatService: service);
      addTearDown(notifier.dispose);

      await notifier.sendMessage('raw', wireContent: 'framed');
      expect(notifier.state.messages.last.errorText, isNotNull);

      await notifier.retryLast();

      expect(service.captured, hasLength(2));
      expect(
        service.captured.last.last.toApiMessage()['content'],
        'framed',
      );
    });

    test('whitespace-only text with a wireContent override sends nothing',
        () async {
      final service = _CapturingAiChatService();
      final notifier = AiChatNotifier(chatService: service);
      addTearDown(notifier.dispose);
      final before = notifier.state.messages.length;

      await notifier.sendMessage('   ', wireContent: 'framed');

      expect(notifier.state.messages, hasLength(before));
      expect(service.captured, isEmpty);
    });
  });
}

class _FailingAiChatService extends AiChatService {
  final Object failure;

  _FailingAiChatService(this.failure) : super(backendDio: Dio());

  @override
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
    String? conversationId,
    CancelToken? cancelToken,
  }) =>
      Stream.error(failure);
}

/// Stores every outgoing [messages] list it is handed and yields a single
/// [TextChunkEvent] so the turn finishes cleanly — used to assert on the
/// *serialised* content the service would send, not just notifier state.
///
/// When [failOnCallIndex] matches the 0-based call number, that call throws
/// instead of succeeding, so a test can drive [AiChatNotifier.retryLast]
/// through a failed-then-retried exchange.
class _CapturingAiChatService extends AiChatService {
  final List<List<ChatMessage>> captured = [];
  final int? failOnCallIndex;
  var _callCount = 0;

  _CapturingAiChatService({this.failOnCallIndex}) : super(backendDio: Dio());

  @override
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
    String? conversationId,
    CancelToken? cancelToken,
  }) async* {
    captured.add(messages);
    final callIndex = _callCount++;
    if (failOnCallIndex != null && callIndex == failOnCallIndex) {
      throw Exception('simulated failure');
    }
    yield TextChunkEvent('ok');
  }
}

class _ControlledAiChatService extends AiChatService {
  final events = StreamController<ChatStreamEvent>();
  CancelToken? cancelToken;

  _ControlledAiChatService() : super(backendDio: Dio());

  @override
  Stream<ChatStreamEvent> sendMessage({
    required List<ChatMessage> messages,
    String? conversationId,
    CancelToken? cancelToken,
  }) async* {
    this.cancelToken = cancelToken;
    yield* events.stream;
  }
}
