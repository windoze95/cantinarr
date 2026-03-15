import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../config/app_config.dart';
import '../theme/app_theme.dart';

/// A poster card for movies/TV shows with optional status badge.
class MediaCard extends StatelessWidget {
  final int id;
  final String title;
  final String? posterPath;
  final String? statusLabel;
  final Color? statusColor;
  final VoidCallback? onTap;
  final double width;

  const MediaCard({
    super.key,
    required this.id,
    required this.title,
    this.posterPath,
    this.statusLabel,
    this.statusColor,
    this.onTap,
    this.width = 120,
  });

  @override
  Widget build(BuildContext context) {
    final imageUrl = posterPath != null && posterPath!.startsWith('http')
        ? posterPath!
        : AppConfig.tmdbPoster(posterPath, width: 342);

    return GestureDetector(
      onTap: onTap,
      child: SizedBox(
        width: width,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Poster
            AspectRatio(
              aspectRatio: 2 / 3,
              child: ClipRRect(
                borderRadius: BorderRadius.circular(10),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    if (posterPath != null)
                      CachedNetworkImage(
                        imageUrl: imageUrl,
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.movie_outlined,
                                color: AppTheme.textSecondary, size: 32),
                          ),
                        ),
                        errorWidget: (_, __, ___) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.broken_image_outlined,
                                color: AppTheme.textSecondary, size: 32),
                          ),
                        ),
                      )
                    else
                      Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.movie_outlined,
                              color: AppTheme.textSecondary, size: 32),
                        ),
                      ),

                    // Status badge
                    if (statusLabel != null)
                      Positioned(
                        top: 6,
                        right: 6,
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 6, vertical: 2),
                          decoration: BoxDecoration(
                            color: (statusColor ?? AppTheme.accent)
                                .withValues(alpha: 0.9),
                            borderRadius: BorderRadius.circular(6),
                          ),
                          child: Text(
                            statusLabel!,
                            style: const TextStyle(
                              color: Colors.white,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 6),
            // Title
            Text(
              title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
