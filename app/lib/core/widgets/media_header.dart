import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../config/app_config.dart';
import '../theme/app_theme.dart';

/// A hero-style header with backdrop, poster, title, and status.
class MediaHeader extends StatelessWidget {
  final String? posterPath;
  final String? backdropPath;
  final String title;
  final String? statusText;
  final Color? statusTint;
  final Widget? actions;

  const MediaHeader({
    super.key,
    this.posterPath,
    this.backdropPath,
    required this.title,
    this.statusText,
    this.statusTint,
    this.actions,
  });

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        // Backdrop
        if (backdropPath != null)
          SizedBox(
            height: 280,
            width: double.infinity,
            child: ShaderMask(
              shaderCallback: (rect) => LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                colors: [
                  Colors.white,
                  Colors.white.withValues(alpha: 0.0),
                ],
                stops: const [0.4, 1.0],
              ).createShader(rect),
              blendMode: BlendMode.dstIn,
              child: CachedNetworkImage(
                imageUrl: AppConfig.tmdbBackdrop(backdropPath, width: 1280),
                fit: BoxFit.cover,
              ),
            ),
          ),

        // Content overlay
        Padding(
          padding: EdgeInsets.only(
            top: backdropPath != null ? 180 : 16,
            left: 16,
            right: 16,
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              // Poster
              ClipRRect(
                borderRadius: BorderRadius.circular(10),
                child: SizedBox(
                  width: 110,
                  height: 165,
                  child: posterPath != null
                      ? CachedNetworkImage(
                          imageUrl: AppConfig.tmdbPoster(posterPath),
                          fit: BoxFit.cover,
                        )
                      : Container(
                          color: AppTheme.surfaceVariant,
                          child: const Icon(Icons.movie_outlined,
                              color: AppTheme.textSecondary, size: 40),
                        ),
                ),
              ),
              const SizedBox(width: 16),
              // Title + status
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.end,
                  children: [
                    Text(
                      title,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 22,
                        fontWeight: FontWeight.bold,
                      ),
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (statusText != null) ...[
                      const SizedBox(height: 6),
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 10, vertical: 4),
                        decoration: BoxDecoration(
                          color: (statusTint ?? AppTheme.accent)
                              .withValues(alpha: 0.2),
                          borderRadius: BorderRadius.circular(8),
                          border: Border.all(
                            color: (statusTint ?? AppTheme.accent)
                                .withValues(alpha: 0.4),
                          ),
                        ),
                        child: Text(
                          statusText!,
                          style: TextStyle(
                            color: statusTint ?? AppTheme.accent,
                            fontSize: 12,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
