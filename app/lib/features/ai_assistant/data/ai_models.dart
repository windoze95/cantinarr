import '../../config_changes/data/config_change_models.dart';

/// A single message in the AI chat conversation.
class ChatMessage {
  final String id;
  final ChatRole role;
  final String content;

  /// When set, this is what is sent to the server in place of [content].
  /// [content] is always what the chat bubble renders; [wireContent], when
  /// non-null, lets a hand-off carry context (e.g. the active discovery tab)
  /// that the user never typed, without the transcript claiming they did.
  final String? wireContent;
  final DateTime timestamp;
  final List<MediaResultItem> mediaResults;
  final List<ConfigChange> configurationChanges;
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
    this.wireContent,
    required this.timestamp,
    this.mediaResults = const [],
    this.configurationChanges = const [],
    this.isStreaming = false,
    this.toolActivity = const [],
    this.errorText,
    this.excludeFromHistory = false,
  });

  ChatMessage copyWith({
    String? content,
    String? wireContent,
    List<MediaResultItem>? mediaResults,
    List<ConfigChange>? configurationChanges,
    bool? isStreaming,
    List<ToolActivity>? toolActivity,
    String? errorText,
    bool? excludeFromHistory,
  }) =>
      ChatMessage(
        id: id,
        role: role,
        content: content ?? this.content,
        wireContent: wireContent ?? this.wireContent,
        timestamp: timestamp,
        mediaResults: mediaResults ?? this.mediaResults,
        configurationChanges:
            configurationChanges ?? this.configurationChanges,
        isStreaming: isStreaming ?? this.isStreaming,
        toolActivity: toolActivity ?? this.toolActivity,
        errorText: errorText ?? this.errorText,
        excludeFromHistory: excludeFromHistory ?? this.excludeFromHistory,
      );

  Map<String, dynamic> toApiMessage() => {
        'role': role == ChatRole.user ? 'user' : 'assistant',
        'content': wireContent ?? content,
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

  /// Book identity/artwork: books have no TMDB id ([id] stays 0). [foreignId]
  /// is the Chaptarr foreignBookId the detail route keys on and [posterUrl] is
  /// an absolute external cover URL (used verbatim instead of a TMDB path).
  final String? foreignId;
  final String? posterUrl;

  const MediaResultItem({
    required this.id,
    required this.title,
    this.year,
    this.posterPath,
    this.voteAverage,
    this.overview,
    this.mediaType,
    this.foreignId,
    this.posterUrl,
  });

  factory MediaResultItem.fromJson(Map<String, dynamic> json) =>
      MediaResultItem(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String,
        year: json['year'] as String?,
        posterPath: json['poster_path'] as String?,
        voteAverage: (json['vote_average'] as num?)?.toDouble(),
        overview: json['overview'] as String?,
        mediaType: json['media_type'] as String?,
        foreignId: json['foreign_id'] as String?,
        posterUrl: json['poster_url'] as String?,
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

/// Durable server-authored receipt for a connected-app settings mutation.
class ConfigurationChangeEvent extends ChatStreamEvent {
  final ConfigChange change;
  ConfigurationChangeEvent(this.change);
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
