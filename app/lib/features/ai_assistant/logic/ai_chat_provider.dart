import 'package:flutter/foundation.dart';
import 'package:uuid/uuid.dart';
import '../data/ai_models.dart';
import '../data/ai_chat_service.dart';

/// State for the AI chat conversation.
class AiChatState {
  final List<ChatMessage> messages;
  final bool isLoading;
  final String? error;

  const AiChatState({
    this.messages = const [],
    this.isLoading = false,
    this.error,
  });

  AiChatState copyWith({
    List<ChatMessage>? messages,
    bool? isLoading,
    String? error,
  }) =>
      AiChatState(
        messages: messages ?? this.messages,
        isLoading: isLoading ?? this.isLoading,
        error: error,
      );
}

/// Manages AI chat state and conversation flow.
///
/// The backend handles all tool execution (TMDB search, request status, etc.)
/// so the client only needs to send messages and stream responses.
class AiChatNotifier extends ChangeNotifier {
  final AiChatService _chatService;
  final _uuid = const Uuid();

  AiChatState _state = const AiChatState();
  AiChatState get state => _state;
  set state(AiChatState value) {
    _state = value;
    notifyListeners();
  }

  AiChatNotifier({required AiChatService chatService})
      : _chatService = chatService {
    _addMessage(ChatMessage(
      id: _uuid.v4(),
      role: ChatRole.assistant,
      content:
          'Hey! I\'m your Cantinarr assistant. I can help you discover movies and TV shows, check what\'s available on your server, or help you get set up. What are you looking for?',
      timestamp: DateTime.now(),
    ));
  }

  /// Send a user message and stream the AI response.
  Future<void> sendMessage(String text) async {
    if (text.trim().isEmpty) return;

    _addMessage(ChatMessage(
      id: _uuid.v4(),
      role: ChatRole.user,
      content: text,
      timestamp: DateTime.now(),
    ));

    state = state.copyWith(isLoading: true, error: null);

    final responseId = _uuid.v4();

    try {
      // Build conversation history (skip welcome message)
      final conversationMessages = state.messages
          .where((m) => m.role != ChatRole.system)
          .skip(1)
          .toList();

      final buffer = StringBuffer();
      final mediaItems = <MediaResultItem>[];

      await for (final event
          in _chatService.sendMessage(messages: conversationMessages)) {
        switch (event) {
          case TextChunkEvent(:final text):
            buffer.write(text);
          case MediaResultsEvent(:final items):
            mediaItems.addAll(items);
        }

        // Update the assistant message in-place for streaming effect
        final updatedMessages = List<ChatMessage>.from(state.messages);

        final existingIdx =
            updatedMessages.indexWhere((m) => m.id == responseId);
        final streamingMessage = ChatMessage(
          id: responseId,
          role: ChatRole.assistant,
          content: buffer.toString(),
          timestamp: DateTime.now(),
          mediaResults: List.unmodifiable(mediaItems),
          isStreaming: true,
        );

        if (existingIdx >= 0) {
          updatedMessages[existingIdx] = streamingMessage;
        } else {
          updatedMessages.add(streamingMessage);
        }

        state = state.copyWith(messages: updatedMessages);
      }

      // Mark the streamed message as complete
      final finalMessages = List<ChatMessage>.from(state.messages);
      final doneIdx = finalMessages.indexWhere((m) => m.id == responseId);
      if (doneIdx >= 0) {
        finalMessages[doneIdx] =
            finalMessages[doneIdx].copyWith(isStreaming: false);
        state = state.copyWith(messages: finalMessages);
      }

      // If no content was streamed, the response was empty
      if (buffer.isEmpty && mediaItems.isEmpty) {
        _addMessage(ChatMessage(
          id: responseId,
          role: ChatRole.assistant,
          content: 'I didn\'t get a response. Please try again.',
          timestamp: DateTime.now(),
        ));
      }

      state = state.copyWith(isLoading: false);
    } catch (e) {
      // Clear streaming flag on any partial message so media cards are shown
      final errMessages = List<ChatMessage>.from(state.messages);
      final errIdx = errMessages.indexWhere((m) => m.id == responseId);
      if (errIdx >= 0) {
        errMessages[errIdx] =
            errMessages[errIdx].copyWith(isStreaming: false);
      }

      state = state.copyWith(
        messages: errIdx >= 0 ? errMessages : null,
        isLoading: false,
        error:
            'Failed to get response: ${e.toString().length > 100 ? '${e.toString().substring(0, 100)}...' : e}',
      );
    }
  }

  void _addMessage(ChatMessage message) {
    state = state.copyWith(messages: [...state.messages, message]);
  }

  void clearError() => state = state.copyWith(error: null);

  void clearChat() {
    state = const AiChatState();
    _addMessage(ChatMessage(
      id: _uuid.v4(),
      role: ChatRole.assistant,
      content: 'Chat cleared! What can I help you find?',
      timestamp: DateTime.now(),
    ));
  }
}
