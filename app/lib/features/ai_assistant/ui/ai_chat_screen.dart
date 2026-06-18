import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/models/app_module.dart';
import '../../../core/providers/module_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/ai_models.dart';
import '../logic/ai_chat_provider.dart';
import 'chat_bubble.dart';

/// The AI assistant chat screen.
class AiChatScreen extends ConsumerStatefulWidget {
  final bool aiAvailable;

  const AiChatScreen({
    super.key,
    this.aiAvailable = false,
  });

  @override
  ConsumerState<AiChatScreen> createState() => _AiChatScreenState();
}

class _AiChatScreenState extends ConsumerState<AiChatScreen> {
  AiChatNotifier? _notifier;
  final _inputController = TextEditingController();
  final _scrollController = ScrollController();
  final _focusNode = FocusNode();

  void _setNotifier(AiChatNotifier? notifier) {
    if (identical(_notifier, notifier)) return;
    _notifier?.removeListener(_scrollToBottom);
    _notifier = notifier;
    _notifier?.addListener(_scrollToBottom);
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
    setState(() {});
  }

  @override
  void dispose() {
    _setNotifier(null);
    _inputController.dispose();
    _scrollController.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  void _send() {
    final text = _inputController.text.trim();
    if (text.isEmpty || _notifier == null) return;
    _inputController.clear();
    _dismissKeyboard();
    _notifier!.sendMessage(text);
  }

  void _dismissKeyboard() {
    _focusNode.unfocus();
    FocusManager.instance.primaryFocus?.unfocus();
  }

  void _exitAssistant() {
    if (context.canPop()) {
      context.pop();
      return;
    }

    ref.read(moduleProvider.notifier).setActiveModule(ModuleType.dashboard);
    context.go('/dashboard/movies');
  }

  @override
  Widget build(BuildContext context) {
    if (!widget.aiAvailable) {
      _setNotifier(null);
      return Scaffold(
        appBar: AppBar(
          leading: IconButton(
            icon: const Icon(Icons.close),
            onPressed: _exitAssistant,
            tooltip: 'Exit assistant',
          ),
          title: const Text('AI Assistant'),
        ),
        body: const Center(
          child: Padding(
            padding: EdgeInsets.all(32),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.smart_toy_outlined,
                    size: 64, color: AppTheme.accent),
                SizedBox(height: 16),
                Text(
                  'AI Assistant',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 24,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                SizedBox(height: 12),
                Text(
                  'The AI assistant is not configured on this server. Ask your server admin to set up an AI provider.',
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 15),
                  textAlign: TextAlign.center,
                ),
              ],
            ),
          ),
        ),
      );
    }

    final notifier = ref.watch(aiChatProvider);
    _setNotifier(notifier);

    return _buildChat(context, notifier);
  }

  Widget _buildChat(BuildContext context, AiChatNotifier notifier) {
    final state = notifier.state;

    return Scaffold(
      appBar: AppBar(
        leading: IconButton(
          icon: Icon(context.canPop() ? Icons.arrow_back : Icons.close),
          onPressed: _exitAssistant,
          tooltip: 'Exit assistant',
        ),
        title: const Text('AI Assistant'),
        actions: [
          IconButton(
            icon: const Icon(Icons.add_comment_outlined),
            onPressed: notifier.clearChat,
            tooltip: 'New chat',
          ),
        ],
      ),
      body: Column(
        children: [
          // Messages
          Expanded(
            child: GestureDetector(
              behavior: HitTestBehavior.translucent,
              onTap: _dismissKeyboard,
              child: Builder(builder: (context) {
                // Only show the typing indicator before the assistant bubble
                // materializes (text, tool activity, or media arriving).
                final showTyping = state.isLoading &&
                    (state.messages.isEmpty ||
                        state.messages.last.role != ChatRole.assistant);
                return ListView.builder(
                  controller: _scrollController,
                  keyboardDismissBehavior:
                      ScrollViewKeyboardDismissBehavior.onDrag,
                  padding: const EdgeInsets.all(16),
                  itemCount: state.messages.length + (showTyping ? 1 : 0),
                  itemBuilder: (context, index) {
                    if (index >= state.messages.length) {
                      return const _TypingIndicator();
                    }
                    final msg = state.messages[index];
                    final isLast = index == state.messages.length - 1;
                    return ChatBubble(
                      message: msg,
                      onRetry: isLast && msg.errorText != null
                          ? notifier.retryLast
                          : null,
                    );
                  },
                );
              }),
            ),
          ),

          // Error
          if (state.error != null)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
              child: Text(
                state.error!,
                style: const TextStyle(color: AppTheme.error, fontSize: 12),
                maxLines: 2,
              ),
            ),

          // Input
          Container(
            decoration: const BoxDecoration(
              color: AppTheme.surface,
              border: Border(top: BorderSide(color: AppTheme.border)),
            ),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: SafeArea(
              top: false,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (state.messages.length <= 1) ...[
                    _buildSuggestions(),
                    const SizedBox(height: 8),
                  ],
                  Row(
                    children: [
                      Expanded(
                        child: TextField(
                          controller: _inputController,
                          focusNode: _focusNode,
                          keyboardType: TextInputType.multiline,
                          textInputAction: TextInputAction.send,
                          onSubmitted: (_) => _send(),
                          onTapOutside: (_) => _dismissKeyboard(),
                          decoration: const InputDecoration(
                            hintText: 'Ask me anything...',
                            border: InputBorder.none,
                            contentPadding: EdgeInsets.symmetric(
                                horizontal: 12, vertical: 10),
                          ),
                          maxLines: 4,
                          minLines: 1,
                        ),
                      ),
                      IconButton(
                        onPressed: state.isLoading ? null : _send,
                        icon: Icon(
                          Icons.send_rounded,
                          color: state.isLoading
                              ? AppTheme.textSecondary
                              : AppTheme.accent,
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSuggestions() {
    final suggestions = [
      "What's trending?",
      'Recommend sci-fi movies',
      'Help me set up Plex',
    ];

    return SizedBox(
      height: 36,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: suggestions.length,
        separatorBuilder: (_, __) => const SizedBox(width: 8),
        itemBuilder: (_, index) => ActionChip(
          label: Text(suggestions[index], style: const TextStyle(fontSize: 12)),
          backgroundColor: AppTheme.surfaceVariant,
          side: const BorderSide(color: AppTheme.border),
          onPressed: () {
            _inputController.text = suggestions[index];
            _send();
          },
        ),
      ),
    );
  }
}

class _TypingIndicator extends StatelessWidget {
  const _TypingIndicator();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              color: AppTheme.surfaceVariant,
              borderRadius: BorderRadius.circular(16),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: List.generate(
                3,
                (i) => Padding(
                  padding: EdgeInsets.only(left: i > 0 ? 4 : 0),
                  child: _Dot(delay: i * 200),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Dot extends StatefulWidget {
  final int delay;
  const _Dot({required this.delay});

  @override
  State<_Dot> createState() => _DotState();
}

class _DotState extends State<_Dot> with SingleTickerProviderStateMixin {
  late AnimationController _controller;
  late Animation<double> _animation;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      duration: const Duration(milliseconds: 600),
      vsync: this,
    );
    _animation = Tween(begin: 0.0, end: 1.0).animate(
      CurvedAnimation(parent: _controller, curve: Curves.easeInOut),
    );
    Future.delayed(Duration(milliseconds: widget.delay), () {
      if (mounted) _controller.repeat(reverse: true);
    });
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      listenable: _animation,
      builder: (_, __) => Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          color: AppTheme.textSecondary
              .withValues(alpha: 0.3 + _animation.value * 0.7),
          shape: BoxShape.circle,
        ),
      ),
    );
  }
}

/// Simple AnimatedBuilder replacement that uses Flutter's built-in.
class AnimatedBuilder extends AnimatedWidget {
  final Widget Function(BuildContext, Widget?) builder;

  const AnimatedBuilder({
    super.key,
    required super.listenable,
    required this.builder,
  });

  @override
  Widget build(BuildContext context) => builder(context, null);
}
