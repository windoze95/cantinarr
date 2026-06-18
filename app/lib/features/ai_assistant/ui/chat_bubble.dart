import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../data/ai_models.dart';

/// A single chat message bubble with optional media result cards.
class ChatBubble extends StatelessWidget {
  final ChatMessage message;

  /// When provided and the message carries an error, an inline retry
  /// affordance is shown (re-sends the last user message).
  final VoidCallback? onRetry;

  const ChatBubble({super.key, required this.message, this.onRetry});

  bool get isUser => message.role == ChatRole.user;

  String get displayContent =>
      isUser ? message.content : message.content.replaceAll('**', '');

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        mainAxisAlignment:
            isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (!isUser) ...[
            // AI avatar
            Container(
              width: 32,
              height: 32,
              decoration: const BoxDecoration(
                color: AppTheme.accent,
                shape: BoxShape.circle,
              ),
              child: const Icon(Icons.smart_toy, size: 18, color: Colors.white),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Transient tool activity line (fades out once the final
                // text has arrived and streaming ends).
                if (!isUser)
                  AnimatedSwitcher(
                    duration: const Duration(milliseconds: 250),
                    child:
                        message.isStreaming && message.toolActivity.isNotEmpty
                            ? _ToolActivityLine(
                                key: ValueKey(
                                    '${message.toolActivity.length}-${message.toolActivity.last.done}'),
                                activity: message.toolActivity.last,
                              )
                            : const SizedBox.shrink(),
                  ),

                // Text bubble
                if (message.content.isNotEmpty)
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 10),
                    decoration: BoxDecoration(
                      color: isUser
                          ? AppTheme.accent.withValues(alpha: 0.15)
                          : AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.only(
                        topLeft: const Radius.circular(16),
                        topRight: const Radius.circular(16),
                        bottomLeft: Radius.circular(isUser ? 16 : 4),
                        bottomRight: Radius.circular(isUser ? 4 : 16),
                      ),
                      border: Border.all(
                        color: isUser
                            ? AppTheme.accent.withValues(alpha: 0.3)
                            : AppTheme.border,
                      ),
                    ),
                    child: SelectableText(
                      displayContent,
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 15,
                        height: 1.4,
                        fontWeight: isUser ? FontWeight.w400 : FontWeight.w400,
                      ),
                    ),
                  ),

                // Inline error state with retry affordance
                if (!isUser && message.errorText != null) ...[
                  if (message.content.isNotEmpty) const SizedBox(height: 8),
                  _ErrorContainer(
                    errorText: message.errorText!,
                    onRetry: onRetry,
                  ),
                ],

                // Media result cards (deferred until streaming completes)
                if (message.mediaResults.isNotEmpty &&
                    !message.isStreaming) ...[
                  const SizedBox(height: 8),
                  SizedBox(
                    height: 232,
                    child: ListView.separated(
                      scrollDirection: Axis.horizontal,
                      itemCount: message.mediaResults.length,
                      separatorBuilder: (_, __) => const SizedBox(width: 8),
                      itemBuilder: (context, index) {
                        return _MediaResultCard(
                            item: message.mediaResults[index]);
                      },
                    ),
                  ),
                ],
              ],
            ),
          ),
          if (isUser) const SizedBox(width: 8),
        ],
      ),
    );
  }
}

/// A compact single-line indicator for what the assistant is doing
/// (e.g. "Searching movies…") while the response streams.
class _ToolActivityLine extends StatelessWidget {
  final ToolActivity activity;

  const _ToolActivityLine({super.key, required this.activity});

  @override
  Widget build(BuildContext context) {
    final Widget icon;
    if (!activity.done) {
      icon = SizedBox(
        width: 12,
        height: 12,
        child: CircularProgressIndicator(
          strokeWidth: 1.5,
          color: AppTheme.accent.withValues(alpha: 0.7),
        ),
      );
    } else if (activity.ok) {
      icon = Icon(
        Icons.check_rounded,
        size: 14,
        color: AppTheme.available.withValues(alpha: 0.8),
      );
    } else {
      icon = Icon(
        Icons.error_outline_rounded,
        size: 14,
        color: AppTheme.error.withValues(alpha: 0.7),
      );
    }

    return Padding(
      padding: const EdgeInsets.only(left: 2, bottom: 6),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          icon,
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              activity.done ? activity.label : '${activity.label}…',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontStyle: FontStyle.italic,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Inline error container shown on a failed assistant message.
class _ErrorContainer extends StatelessWidget {
  final String errorText;
  final VoidCallback? onRetry;

  const _ErrorContainer({required this.errorText, this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
      decoration: BoxDecoration(
        color: AppTheme.error.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.error.withValues(alpha: 0.35)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Padding(
                padding: EdgeInsets.only(top: 1),
                child:
                    Icon(Icons.error_outline, size: 16, color: AppTheme.error),
              ),
              const SizedBox(width: 8),
              Flexible(
                child: Text(
                  errorText,
                  style: const TextStyle(
                    color: AppTheme.error,
                    fontSize: 13,
                    height: 1.35,
                  ),
                ),
              ),
            ],
          ),
          if (onRetry != null)
            Align(
              alignment: Alignment.centerRight,
              child: TextButton.icon(
                onPressed: onRetry,
                style: TextButton.styleFrom(
                  foregroundColor: AppTheme.error,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                icon: const Icon(Icons.refresh_rounded, size: 16),
                label: const Text('Retry', style: TextStyle(fontSize: 13)),
              ),
            )
          else
            const SizedBox(height: 4),
        ],
      ),
    );
  }
}

/// A compact media card for chat results showing poster, title, year, and rating.
class _MediaResultCard extends StatelessWidget {
  final MediaResultItem item;

  const _MediaResultCard({required this.item});

  @override
  Widget build(BuildContext context) {
    final imageUrl = AppConfig.tmdbPoster(item.posterPath, width: 342);

    final mediaType = item.mediaType ?? 'movie';

    return GestureDetector(
      onTap: () => context.push('/detail/$mediaType/${item.id}'),
      child: SizedBox(
        width: 120,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Poster
            AspectRatio(
              aspectRatio: 2 / 3,
              child: ClipRRect(
                borderRadius: BorderRadius.circular(8),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    if (item.posterPath != null)
                      CachedNetworkImage(
                        imageUrl: imageUrl,
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.movie_outlined,
                                color: AppTheme.textSecondary, size: 28),
                          ),
                        ),
                        errorWidget: (_, __, ___) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.broken_image_outlined,
                                color: AppTheme.textSecondary, size: 28),
                          ),
                        ),
                      )
                    else
                      Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.movie_outlined,
                              color: AppTheme.textSecondary, size: 28),
                        ),
                      ),
                    // Rating badge
                    if (item.voteAverage != null && item.voteAverage! > 0)
                      Positioned(
                        top: 4,
                        right: 4,
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 5, vertical: 2),
                          decoration: BoxDecoration(
                            color: Colors.black.withValues(alpha: 0.7),
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              const Icon(Icons.star_rounded,
                                  color: AppTheme.accent, size: 12),
                              const SizedBox(width: 2),
                              Text(
                                item.voteAverage!.toStringAsFixed(1),
                                style: const TextStyle(
                                  color: Colors.white,
                                  fontSize: 10,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 4),
            // Title + year
            Text(
              item.year != null ? '${item.title} (${item.year})' : item.title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 11,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
