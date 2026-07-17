import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../automation/web_semantics.dart';
import '../config/app_config.dart';
import '../layout/adaptive.dart';
import '../network/app_image_cache.dart';
import '../theme/app_theme.dart';
import 'cached_image.dart';

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
    return LayoutBuilder(
      builder: (context, constraints) {
        final expanded = constraints.maxWidth >= 720;
        final height = expanded ? 430.0 : 350.0;
        final posterWidth = expanded ? 156.0 : 108.0;
        final posterHeight = posterWidth * 1.5;
        final horizontalPadding = AppBreakpoints.centeredContentPadding(
          constraints.maxWidth,
          maxContentWidth: 1180,
          minPadding: expanded ? 24 : 16,
        );

        return SizedBox(
          height: height,
          width: double.infinity,
          child: Stack(
            fit: StackFit.expand,
            children: [
              if (backdropPath != null)
                CachedNetworkImage(
                  imageUrl: AppConfig.tmdbBackdrop(backdropPath, width: 1280),
                  fit: BoxFit.cover,
                  cacheManager: appImageCache,
                )
              else
                const ColoredBox(color: AppTheme.surfaceVariant),
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment.bottomCenter,
                    stops: [0, 0.42, 0.82, 1],
                    colors: [
                      Color(0x260C0805),
                      Color(0x450C0805),
                      Color(0xE60C0805),
                      AppTheme.background,
                    ],
                  ),
                ),
              ),
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.centerLeft,
                    end: Alignment.centerRight,
                    colors: [Color(0xB80C0805), Colors.transparent],
                    stops: [0, 0.72],
                  ),
                ),
              ),
              Positioned(
                left: horizontalPadding,
                right: horizontalPadding,
                bottom: 16,
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.end,
                  children: [
                    Container(
                      width: posterWidth,
                      height: posterHeight,
                      decoration: BoxDecoration(
                        borderRadius: BorderRadius.circular(
                          AppTheme.radiusLarge,
                        ),
                        border: Border.all(
                          color: AppTheme.textPrimary.withValues(alpha: 0.15),
                        ),
                        boxShadow: [
                          BoxShadow(
                            color: Colors.black.withValues(alpha: 0.52),
                            blurRadius: 26,
                            offset: const Offset(0, 14),
                          ),
                        ],
                      ),
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(
                          AppTheme.radiusLarge - 1,
                        ),
                        child: CachedImage(
                          url: posterPath == null
                              ? null
                              : AppConfig.tmdbPoster(posterPath),
                          fit: BoxFit.cover,
                          icon: Icons.movie_outlined,
                          iconSize: 40,
                        ),
                      ),
                    ),
                    SizedBox(width: expanded ? 24 : 15),
                    Expanded(
                      child: Padding(
                        padding: EdgeInsets.only(bottom: expanded ? 12 : 5),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Semantics(
                              identifier: 'media-detail-title',
                              label: e2eWebSemanticsEnabled ? title : null,
                              header: true,
                              excludeSemantics: e2eWebSemanticsEnabled,
                              child: Text(
                                title,
                                style: Theme.of(context)
                                    .textTheme
                                    .displaySmall
                                    ?.copyWith(
                                  color: AppTheme.textPrimary,
                                  fontSize: expanded ? 42 : 25,
                                  height: 1.05,
                                  fontWeight: FontWeight.w800,
                                  letterSpacing: expanded ? -1.1 : -0.5,
                                  shadows: const [
                                    Shadow(
                                      color: Colors.black,
                                      blurRadius: 16,
                                    ),
                                  ],
                                ),
                                maxLines: expanded ? 3 : 4,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                            if (statusText != null) ...[
                              const SizedBox(height: 10),
                              _HeaderStatus(
                                text: statusText!,
                                color: statusTint ?? AppTheme.accent,
                              ),
                            ],
                            if (actions != null) ...[
                              const SizedBox(height: 14),
                              actions!,
                            ],
                          ],
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _HeaderStatus extends StatelessWidget {
  final String text;
  final Color color;

  const _HeaderStatus({required this.text, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(AppTheme.radiusPill),
        border: Border.all(color: color.withValues(alpha: 0.34)),
      ),
      child: Text(
        text,
        style: TextStyle(
          color: color,
          fontSize: 11.5,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }
}
