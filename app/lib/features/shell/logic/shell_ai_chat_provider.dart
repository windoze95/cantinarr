import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../ai_assistant/data/ai_chat_service.dart';
import '../../ai_assistant/logic/ai_chat_provider.dart';

/// Provides an [AiChatNotifier] scoped to the shell's inline AI mode.
///
/// Created lazily on first access, persists until the provider is disposed.
/// Reuses the existing [AiChatService] and [AiChatNotifier] classes.
final shellAiChatProvider = Provider<AiChatNotifier>((ref) {
  final backendDio = ref.watch(backendClientProvider);
  final notifier = AiChatNotifier(
    chatService: AiChatService(backendDio: backendDio),
  );
  ref.onDispose(() => notifier.dispose());
  return notifier;
});
