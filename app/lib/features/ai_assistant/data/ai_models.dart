/// A single message in the AI chat conversation.
class ChatMessage {
  final String id;
  final ChatRole role;
  final String content;
  final DateTime timestamp;
  final List<MediaResultItem> mediaResults;

  const ChatMessage({
    required this.id,
    required this.role,
    required this.content,
    required this.timestamp,
    this.mediaResults = const [],
  });

  ChatMessage copyWith({
    String? content,
    List<MediaResultItem>? mediaResults,
  }) =>
      ChatMessage(
        id: id,
        role: role,
        content: content ?? this.content,
        timestamp: timestamp,
        mediaResults: mediaResults ?? this.mediaResults,
      );

  Map<String, dynamic> toApiMessage() => {
        'role': role == ChatRole.user ? 'user' : 'assistant',
        'content': content,
      };
}

enum ChatRole { user, assistant, system }

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
