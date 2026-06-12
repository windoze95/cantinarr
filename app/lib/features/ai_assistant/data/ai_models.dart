/// A single message in the AI chat conversation.
class ChatMessage {
  final String id;
  final ChatRole role;
  final String content;
  final DateTime timestamp;
  final List<MediaResultItem> mediaResults;
  final bool isStreaming;

  /// Transient tool activity (populated while the assistant is streaming).
  final List<ToolActivity> toolActivity;

  /// When set, the message is rendered with an inline error state.
  final String? errorText;

  /// Display-only messages (welcome text, synthetic notices) are never
  /// sent back to the server as part of the conversation transcript.
  final bool excludeFromHistory;

  const ChatMessage({
    required this.id,
    required this.role,
    required this.content,
    required this.timestamp,
    this.mediaResults = const [],
    this.isStreaming = false,
    this.toolActivity = const [],
    this.errorText,
    this.excludeFromHistory = false,
  });

  ChatMessage copyWith({
    String? content,
    List<MediaResultItem>? mediaResults,
    bool? isStreaming,
    List<ToolActivity>? toolActivity,
    String? errorText,
    bool? excludeFromHistory,
  }) =>
      ChatMessage(
        id: id,
        role: role,
        content: content ?? this.content,
        timestamp: timestamp,
        mediaResults: mediaResults ?? this.mediaResults,
        isStreaming: isStreaming ?? this.isStreaming,
        toolActivity: toolActivity ?? this.toolActivity,
        errorText: errorText ?? this.errorText,
        excludeFromHistory: excludeFromHistory ?? this.excludeFromHistory,
      );

  Map<String, dynamic> toApiMessage() => {
        'role': role == ChatRole.user ? 'user' : 'assistant',
        'content': content,
      };
}

enum ChatRole { user, assistant, system }

/// A single tool invocation surfaced while the assistant is working.
class ToolActivity {
  final String name;
  final String label;
  final bool done;
  final bool ok;

  const ToolActivity({
    required this.name,
    required this.label,
    this.done = false,
    this.ok = true,
  });

  ToolActivity copyWith({bool? done, bool? ok}) => ToolActivity(
        name: name,
        label: label,
        done: done ?? this.done,
        ok: ok ?? this.ok,
      );
}

/// A media item returned from tool execution for rich UI display.
class MediaResultItem {
  final int id;
  final String title;
  final String? year;
  final String? posterPath;
  final double? voteAverage;
  final String? overview;
  final String? mediaType;

  const MediaResultItem({
    required this.id,
    required this.title,
    this.year,
    this.posterPath,
    this.voteAverage,
    this.overview,
    this.mediaType,
  });

  factory MediaResultItem.fromJson(Map<String, dynamic> json) =>
      MediaResultItem(
        id: json['id'] as int,
        title: json['title'] as String,
        year: json['year'] as String?,
        posterPath: json['poster_path'] as String?,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        overview: json['overview'] as String?,
        mediaType: json['media_type'] as String?,
      );
}

/// Events emitted from the SSE chat stream.
sealed class ChatStreamEvent {}

class TextChunkEvent extends ChatStreamEvent {
  final String text;
  TextChunkEvent(this.text);
}

class MediaResultsEvent extends ChatStreamEvent {
  final List<MediaResultItem> items;
  MediaResultsEvent(this.items);
}

/// Server-assigned conversation ID; echo it back on subsequent turns so the
/// server can keep full tool context across turns.
class ConversationIdEvent extends ChatStreamEvent {
  final String id;
  ConversationIdEvent(this.id);
}

/// The assistant started executing a tool.
class ToolStartEvent extends ChatStreamEvent {
  final String name;
  final String label;
  ToolStartEvent(this.name, this.label);
}

/// A tool finished executing.
class ToolEndEvent extends ChatStreamEvent {
  final String name;
  final bool ok;
  ToolEndEvent(this.name, this.ok);
}

/// The server reported an error mid-stream.
class StreamErrorEvent extends ChatStreamEvent {
  final String message;
  StreamErrorEvent(this.message);
}
