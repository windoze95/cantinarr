import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';
import '../../../core/network/backend_client.dart';
import '../data/ai_models.dart';
import '../data/ai_chat_service.dart';

/// How long an assistant session can sit unused before it is refreshed.
const aiChatSessionIdleTimeout = Duration(minutes: 30);

/// Shared assistant conversation for both the full-screen assistant and the
/// shell's inline AI mode.
///
/// The keep-alive link lets users leave and reopen the assistant without
/// losing the active conversation. Auto-dispose still gives us a natural
/// refresh after the app is backgrounded or the assistant has no listeners for
/// a while.
final aiChatProvider =
    ChangeNotifierProvider.autoDispose<AiChatNotifier>((ref) {
  final backendDio = ref.watch(backendClientProvider);
  final notifier = AiChatNotifier(
    chatService: AiChatService(backendDio: backendDio),
  );
  final keepAliveLink = ref.keepAlive();

  Timer? idleTimer;
  Timer? backgroundTimer;

  void expireSession() {
    idleTimer?.cancel();
    backgroundTimer?.cancel();
    ref.invalidateSelf();
  }

  void startIdleTimer() {
    idleTimer?.cancel();
    idleTimer = Timer(aiChatSessionIdleTimeout, expireSession);
  }

  void cancelIdleTimer() {
    idleTimer?.cancel();
    idleTimer = null;
  }

  void startBackgroundTimer() {
    backgroundTimer?.cancel();
    backgroundTimer = Timer(aiChatSessionIdleTimeout, expireSession);
  }

  void cancelBackgroundTimer() {
    backgroundTimer?.cancel();
    backgroundTimer = null;
  }

  final lifecycleObserver = _AiChatLifecycleObserver(
    onBackgrounded: startBackgroundTimer,
    onResumed: cancelBackgroundTimer,
  );

  WidgetsBinding.instance.addObserver(lifecycleObserver);

  ref.onCancel(startIdleTimer);
  ref.onResume(cancelIdleTimer);
  ref.onDispose(() {
    cancelIdleTimer();
    cancelBackgroundTimer();
    WidgetsBinding.instance.removeObserver(lifecycleObserver);
    keepAliveLink.close();
  });

  return notifier;
});

class _AiChatLifecycleObserver extends WidgetsBindingObserver {
  final VoidCallback onBackgrounded;
  final VoidCallback onResumed;

  _AiChatLifecycleObserver({
    required this.onBackgrounded,
    required this.onResumed,
  });

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      onResumed();
      return;
    }

    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      onBackgrounded();
    }
  }
}

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
  bool _disposed = false;

  /// Server-assigned conversation ID; sent on every turn so the server can
  /// keep full tool context. Reset when the chat is cleared.
  String? _conversationId;
  String? get conversationId => _conversationId;

  /// Monotonic token tying stream updates to the chat session that started
  /// them; clearChat/new turns bump it so stale streams stop mutating state.
  int _generation = 0;

  AiChatState _state = const AiChatState();
  AiChatState get state => _state;
  set state(AiChatState value) {
    if (_disposed) return;
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
      excludeFromHistory: true,
    ));
  }

  /// Send a user message and stream the AI response.
  Future<void> sendMessage(String text) async {
    if (_disposed) return;
    if (text.trim().isEmpty) return;
    // One turn at a time: a second concurrent stream would corrupt chat
    // state and race the server-side conversation store.
    if (state.isLoading) return;
    final generation = ++_generation;

    _addMessage(ChatMessage(
      id: _uuid.v4(),
      role: ChatRole.user,
      content: text,
      timestamp: DateTime.now(),
    ));

    state = state.copyWith(isLoading: true, error: null);

    final responseId = _uuid.v4();
    final buffer = StringBuffer();
    final mediaItems = <MediaResultItem>[];
    final toolActivity = <ToolActivity>[];
    String? errorText;

    void upsertResponse({required bool streaming}) {
      // A clearChat (or newer turn) since this stream started owns the
      // state now — drop stale updates instead of resurrecting them.
      if (generation != _generation) return;
      final updated = List<ChatMessage>.from(state.messages);
      final idx = updated.indexWhere((m) => m.id == responseId);
      final message = ChatMessage(
        id: responseId,
        role: ChatRole.assistant,
        content: buffer.toString(),
        timestamp: DateTime.now(),
        mediaResults: List.unmodifiable(mediaItems),
        toolActivity: List.unmodifiable(toolActivity),
        isStreaming: streaming,
        errorText: errorText,
        // A failed message with no text carries nothing worth re-sending.
        excludeFromHistory: errorText != null && buffer.isEmpty,
      );
      if (idx >= 0) {
        updated[idx] = message;
      } else {
        updated.add(message);
      }
      state = state.copyWith(messages: updated);
    }

    try {
      final history = _historyForApi();

      await for (final event in _chatService.sendMessage(
        messages: history,
        conversationId: _conversationId,
      )) {
        switch (event) {
          case ConversationIdEvent(:final id):
            _conversationId = id;
          case TextChunkEvent(:final text):
            buffer.write(text);
          case MediaResultsEvent(:final items):
            if (mediaItems.isEmpty && buffer.isNotEmpty) {
              upsertResponse(streaming: true);
              await Future<void>.delayed(const Duration(milliseconds: 16));
            }
            mediaItems.addAll(items);
          case ToolStartEvent(:final name, :final label):
            toolActivity.add(ToolActivity(name: name, label: label));
          case ToolEndEvent(:final name, :final ok):
            final idx =
                toolActivity.lastIndexWhere((t) => t.name == name && !t.done);
            if (idx >= 0) {
              toolActivity[idx] =
                  toolActivity[idx].copyWith(done: true, ok: ok);
            }
          case StreamErrorEvent(:final message):
            errorText = message;
        }

        // Stop streaming on a server-reported error; the final state is
        // written below with the partial text retained.
        if (errorText != null) break;

        // Only materialize the assistant bubble once there is something
        // to show (text, media, or tool activity).
        if (buffer.isNotEmpty ||
            mediaItems.isNotEmpty ||
            toolActivity.isNotEmpty) {
          upsertResponse(streaming: true);
        }
      }

      // An empty response with no explicit error is still a failure from
      // the user's perspective: surface it as a retryable inline error
      // instead of injecting fake assistant text into the transcript.
      if (errorText == null && buffer.isEmpty && mediaItems.isEmpty) {
        errorText = 'I didn\'t get a response. Please try again.';
      }

      upsertResponse(streaming: false);
      if (generation == _generation) {
        // A failed turn desyncs us from the server's stored transcript
        // (which may also have been invalidated): start the next turn fresh
        // from the client transcript rather than replaying a broken state.
        if (errorText != null) _conversationId = null;
        state = state.copyWith(isLoading: false);
      }
    } catch (e) {
      errorText ??= _friendlyError(e);
      upsertResponse(streaming: false);
      if (generation == _generation) {
        _conversationId = null;
        state = state.copyWith(isLoading: false);
      }
    }
  }

  /// Re-send the most recent user message (e.g. after an error).
  ///
  /// Removes the failed exchange from the transcript before retrying so the
  /// history sent to the server stays clean.
  Future<void> retryLast() async {
    if (state.isLoading) return;
    final messages = List<ChatMessage>.from(state.messages);
    final lastUserIdx = messages.lastIndexWhere((m) => m.role == ChatRole.user);
    if (lastUserIdx < 0) return;
    final text = messages[lastUserIdx].content;
    messages.removeRange(lastUserIdx, messages.length);
    state = state.copyWith(messages: messages);
    await sendMessage(text);
  }

  /// Conversation transcript to send to the server: skips display-only
  /// messages (welcome text, synthetic notices) and empty content.
  List<ChatMessage> _historyForApi() => state.messages
      .where((m) =>
          m.role != ChatRole.system &&
          !m.excludeFromHistory &&
          m.content.isNotEmpty)
      .toList();

  String _friendlyError(Object e) {
    final text = e.toString();
    return 'Failed to get a response: ${text.length > 100 ? '${text.substring(0, 100)}...' : text}';
  }

  void _addMessage(ChatMessage message) {
    state = state.copyWith(messages: [...state.messages, message]);
  }

  void clearError() => state = state.copyWith(error: null);

  void clearChat() {
    if (_disposed) return;
    _conversationId = null;
    _generation++; // orphan any in-flight stream
    state = const AiChatState();
    _addMessage(ChatMessage(
      id: _uuid.v4(),
      role: ChatRole.assistant,
      content: 'Chat cleared! What can I help you find?',
      timestamp: DateTime.now(),
      excludeFromHistory: true,
    ));
  }

  @override
  void dispose() {
    _generation++;
    _disposed = true;
    super.dispose();
  }
}
