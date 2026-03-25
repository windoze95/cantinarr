import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../data/ai_models.dart';

/// A single chat message bubble with optional media result cards.
class ChatBubble extends StatelessWidget {
  final ChatMessage message;

  const ChatBubble({super.key, required this.message});

  bool get isUser => message.role == ChatRole.user;

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
                      message.content,
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 15,
                        height: 1.4,
                        fontWeight: isUser ? FontWeight.w400 : FontWeight.w400,
                      ),
                    ),
                  ),

                // Media result cards
                if (message.mediaResults.isNotEmpty) ...[
                  const SizedBox(height: 8),
                  SizedBox(
                    height: 200,
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
        width: 110,
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
