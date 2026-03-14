/// A single message in the AI chat conversation.
class ChatMessage {
  final String id;
  final ChatRole role;
  final String content;
  final DateTime timestamp;

  const ChatMessage({
    required this.id,
    required this.role,
    required this.content,
    required this.timestamp,
  });

  Map<String, dynamic> toApiMessage() => {
        'role': role == ChatRole.user ? 'user' : 'assistant',
        'content': content,
      };
}

enum ChatRole { user, assistant, system }
