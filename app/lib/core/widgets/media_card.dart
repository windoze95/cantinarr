import 'package:flutter/material.dart';
import '../config/app_config.dart';
import '../theme/app_theme.dart';
import 'cached_image.dart';

/// A poster card for movies/TV shows with optional status badge.
class MediaCard extends StatelessWidget {
  /// Extra height a horizontal row of [MediaCard]s must reserve below the
  /// poster when its cards carry a [subtitle] line (e.g. TV rows' episode
  /// availability). Shared by every browse-row/library-row call site
  /// (`CategoryRow`, `DashboardTvTab`, `DashboardMoviesTab`) so the literal
  /// can never drift between them — see [plainRowExtraHeight] for rows whose
  /// cards never carry a subtitle.
  static const double subtitleRowExtraHeight = 68;

  /// Extra height a horizontal row of [MediaCard]s must reserve below the
  /// poster when its cards never carry a [subtitle] line (e.g. movie rows).
  static const double plainRowExtraHeight = 54;

  final int id;
  final String title;
  final String? posterPath;
  final String? statusLabel;
  final Color? statusColor;

  /// Optional secondary line under the title (e.g. "18/24 eps" availability).
  final String? subtitle;

  /// How many lines the subtitle may use. One by default — a shelf of cards
  /// reads best with a single line — but a caller whose subtitle carries a
  /// fact that must not be cut off (a "9 of 41 books available" count means
  /// nothing once it ellipsises to "9 of 41 books avail…") can allow more,
  /// and size its row to match.
  final int subtitleMaxLines;
  final VoidCallback? onTap;
  final double width;

  /// Optional TMDB-style score displayed over the artwork.
  final double? rating;

  /// Headers for the poster request. Chaptarr covers are served through the
  /// authenticated instance proxy and need the user's bearer token.
  final Map<String, String>? posterHeaders;

  /// Drawn when there is no artwork. Books want a book, not a film reel.
  final IconData placeholderIcon;

  const MediaCard({
    super.key,
    required this.id,
    required this.title,
    this.posterPath,
    this.statusLabel,
    this.statusColor,
    this.subtitle,
    this.subtitleMaxLines = 1,
    this.onTap,
    this.width = 120,
    this.rating,
    this.posterHeaders,
    this.placeholderIcon = Icons.movie_outlined,
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
        subtitleMaxLines: subtitleMaxLines,
        statusLabel: statusLabel,
        statusColor: statusColor,
        rating: rating,
        posterHeaders: posterHeaders,
        placeholderIcon: placeholderIcon,
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
  final int subtitleMaxLines;
  final String? statusLabel;
  final Color? statusColor;
  final double? rating;
  final Map<String, String>? posterHeaders;
  final IconData placeholderIcon;

  const _InteractiveMediaCard({
    required this.onTap,
    required this.width,
    required this.posterPath,
    required this.imageUrl,
    required this.title,
    required this.subtitle,
    required this.subtitleMaxLines,
    required this.statusLabel,
    required this.statusColor,
    required this.rating,
    required this.posterHeaders,
    required this.placeholderIcon,
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
                            headers: widget.posterHeaders,
                            fit: BoxFit.cover,
                            icon: widget.placeholderIcon,
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
                    maxLines: widget.subtitleMaxLines,
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
