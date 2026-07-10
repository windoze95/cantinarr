import 'package:flutter/material.dart';
import '../config/app_config.dart';
import '../theme/app_theme.dart';
import 'cached_image.dart';

/// A poster card for movies/TV shows with optional status badge.
class MediaCard extends StatelessWidget {
  final int id;
  final String title;
  final String? posterPath;
  final String? statusLabel;
  final Color? statusColor;

  /// Optional secondary line under the title (e.g. "18/24 eps" availability).
  final String? subtitle;
  final VoidCallback? onTap;
  final double width;

  /// Optional TMDB-style score displayed over the artwork.
  final double? rating;

  const MediaCard({
    super.key,
    required this.id,
    required this.title,
    this.posterPath,
    this.statusLabel,
    this.statusColor,
    this.subtitle,
    this.onTap,
    this.width = 120,
    this.rating,
  });

  @override
  Widget build(BuildContext context) {
    final imageUrl = posterPath != null && posterPath!.startsWith('http')
        ? posterPath!
        : AppConfig.tmdbPoster(posterPath, width: 342);

    final semantics = [
      title,
      if (subtitle != null) subtitle!,
      if (statusLabel != null) statusLabel!,
      if (rating != null && rating! > 0) 'Rated ${rating!.toStringAsFixed(1)}',
    ].join(', ');

    return Semantics(
      button: onTap != null,
      label: semantics,
      excludeSemantics: true,
      onTap: onTap,
      child: _InteractiveMediaCard(
        onTap: onTap,
        width: width,
        posterPath: posterPath,
        imageUrl: imageUrl,
        title: title,
        subtitle: subtitle,
        statusLabel: statusLabel,
        statusColor: statusColor,
        rating: rating,
      ),
    );
  }
}

class _InteractiveMediaCard extends StatefulWidget {
  final VoidCallback? onTap;
  final double width;
  final String? posterPath;
  final String imageUrl;
  final String title;
  final String? subtitle;
  final String? statusLabel;
  final Color? statusColor;
  final double? rating;

  const _InteractiveMediaCard({
    required this.onTap,
    required this.width,
    required this.posterPath,
    required this.imageUrl,
    required this.title,
    required this.subtitle,
    required this.statusLabel,
    required this.statusColor,
    required this.rating,
  });

  @override
  State<_InteractiveMediaCard> createState() => _InteractiveMediaCardState();
}

class _InteractiveMediaCardState extends State<_InteractiveMediaCard> {
  bool _hovered = false;
  bool _focused = false;

  @override
  Widget build(BuildContext context) {
    final emphasized = _hovered || _focused;
    final reduceMotion = MediaQuery.disableAnimationsOf(context);
    final badgeColor = widget.statusColor ?? AppTheme.accent;
    final badgeForeground = badgeColor.computeLuminance() > 0.24
        ? AppTheme.background
        : AppTheme.textPrimary;

    return AnimatedScale(
      scale: emphasized && !reduceMotion ? 1.025 : 1,
      duration: reduceMotion ? Duration.zero : AppTheme.motionFast,
      curve: Curves.easeOutCubic,
      child: SizedBox(
        width: widget.width,
        child: Material(
          color: Colors.transparent,
          child: InkWell(
            onTap: widget.onTap,
            onHover: (value) => setState(() => _hovered = value),
            onFocusChange: (value) => setState(() => _focused = value),
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            splashColor: AppTheme.accent.withValues(alpha: 0.12),
            hoverColor: Colors.transparent,
            focusColor: Colors.transparent,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                AnimatedContainer(
                  duration: reduceMotion ? Duration.zero : AppTheme.motionFast,
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                    border: Border.all(
                      color: emphasized
                          ? AppTheme.borderStrong
                          : AppTheme.border.withValues(alpha: 0.82),
                    ),
                    boxShadow: [
                      BoxShadow(
                        color: Colors.black.withValues(
                          alpha: emphasized ? 0.48 : 0.28,
                        ),
                        blurRadius: emphasized ? 22 : 12,
                        offset: const Offset(0, 8),
                      ),
                      if (emphasized)
                        BoxShadow(
                          color: AppTheme.signal.withValues(alpha: 0.09),
                          blurRadius: 20,
                        ),
                    ],
                  ),
                  child: AspectRatio(
                    aspectRatio: 2 / 3,
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(
                        AppTheme.radiusLarge - 1,
                      ),
                      child: Stack(
                        fit: StackFit.expand,
                        children: [
                          CachedImage(
                            url: widget.posterPath == null
                                ? null
                                : widget.imageUrl,
                            fit: BoxFit.cover,
                            icon: Icons.movie_outlined,
                            iconSize: 32,
                          ),
                          const Positioned.fill(
                            child: DecoratedBox(
                              decoration: BoxDecoration(
                                gradient: LinearGradient(
                                  begin: Alignment.topCenter,
                                  end: Alignment.bottomCenter,
                                  stops: [0.6, 1],
                                  colors: [
                                    Colors.transparent,
                                    Color(0x8F0C0805)
                                  ],
                                ),
                              ),
                            ),
                          ),
                          if (widget.statusLabel != null)
                            Positioned(
                              top: 7,
                              right: 7,
                              child: Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 8,
                                  vertical: 4,
                                ),
                                decoration: BoxDecoration(
                                  color: badgeColor,
                                  borderRadius: BorderRadius.circular(99),
                                  boxShadow: [
                                    BoxShadow(
                                      color:
                                          Colors.black.withValues(alpha: 0.3),
                                      blurRadius: 8,
                                    ),
                                  ],
                                ),
                                child: Text(
                                  widget.statusLabel!,
                                  style: TextStyle(
                                    color: badgeForeground,
                                    fontSize: 11,
                                    fontWeight: FontWeight.w800,
                                  ),
                                ),
                              ),
                            ),
                          if (widget.rating != null && widget.rating! > 0)
                            Positioned(
                              left: 7,
                              bottom: 7,
                              child: Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 7,
                                  vertical: 4,
                                ),
                                decoration: BoxDecoration(
                                  color: AppTheme.background
                                      .withValues(alpha: 0.82),
                                  borderRadius: BorderRadius.circular(99),
                                  border: Border.all(
                                    color: AppTheme.textPrimary
                                        .withValues(alpha: 0.12),
                                  ),
                                ),
                                child: Row(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                    const Icon(
                                      Icons.star_rounded,
                                      size: 12,
                                      color: AppTheme.accent,
                                    ),
                                    const SizedBox(width: 3),
                                    Text(
                                      widget.rating!.toStringAsFixed(1),
                                      style: const TextStyle(
                                        color: AppTheme.textPrimary,
                                        fontSize: 10.5,
                                        fontWeight: FontWeight.w800,
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
                ),
                const SizedBox(height: 9),
                Text(
                  widget.title,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.labelLarge?.copyWith(
                        color: AppTheme.textPrimary,
                        fontSize: 12.5,
                        height: 1.22,
                        fontWeight: FontWeight.w600,
                      ),
                ),
                if (widget.subtitle != null) ...[
                  const SizedBox(height: 2),
                  Text(
                    widget.subtitle!,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.labelSmall?.copyWith(
                          color: AppTheme.textMuted,
                          fontSize: 11,
                        ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
