import 'package:flutter/material.dart';

import '../../features/discover/data/tmdb_models.dart';
import '../config/app_config.dart';
import '../layout/adaptive.dart';
import '../theme/app_theme.dart';
import 'cached_image.dart';

/// Cinematic lead story for the discovery dashboard.
///
/// This deliberately uses the media artwork already returned by discovery;
/// there is no stored spotlight state that can drift from TMDB/Trakt.
class FeaturedMediaHero extends StatefulWidget {
  final MediaItem item;
  final VoidCallback onTap;
  final String eyebrow;

  const FeaturedMediaHero({
    super.key,
    required this.item,
    required this.onTap,
    this.eyebrow = 'Spotlight',
  });

  @override
  State<FeaturedMediaHero> createState() => _FeaturedMediaHeroState();
}

class _FeaturedMediaHeroState extends State<FeaturedMediaHero> {
  bool _isHovered = false;
  bool _isFocused = false;

  @override
  Widget build(BuildContext context) {
    final desktop = AppBreakpoints.isDesktop(context);
    final reduceMotion = MediaQuery.disableAnimationsOf(context);
    final emphasized = _isHovered || _isFocused;
    final imagePath = widget.item.backdropPath ?? widget.item.posterPath;
    final imageUrl = widget.item.backdropPath != null
        ? AppConfig.tmdbBackdrop(imagePath, width: 1280)
        : AppConfig.tmdbPoster(imagePath, width: 780);
    final year = _year(widget.item.releaseDate);
    final rating = widget.item.voteAverage;

    return Semantics(
      button: true,
      label: 'View ${widget.item.title} details',
      excludeSemantics: true,
      onTap: widget.onTap,
      child: AnimatedScale(
        scale: emphasized && !reduceMotion ? 1.006 : 1,
        duration: reduceMotion ? Duration.zero : AppTheme.motionFast,
        curve: Curves.easeOutCubic,
        child: Container(
          height: desktop ? 330 : 248,
          margin:
              EdgeInsets.fromLTRB(desktop ? 24 : 16, 8, desktop ? 24 : 16, 8),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppTheme.radiusXLarge),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.38),
                blurRadius: emphasized ? 34 : 24,
                offset: const Offset(0, 14),
              ),
              BoxShadow(
                color:
                    AppTheme.signal.withValues(alpha: emphasized ? 0.11 : 0.05),
                blurRadius: 30,
              ),
            ],
          ),
          child: Material(
            color: AppTheme.surface,
            clipBehavior: Clip.antiAlias,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppTheme.radiusXLarge),
              side: BorderSide(
                color: emphasized ? AppTheme.borderStrong : AppTheme.border,
              ),
            ),
            child: InkWell(
              onTap: widget.onTap,
              onHover: (value) => setState(() => _isHovered = value),
              onFocusChange: (value) => setState(() => _isFocused = value),
              focusColor: AppTheme.signal.withValues(alpha: 0.08),
              hoverColor: AppTheme.textPrimary.withValues(alpha: 0.025),
              splashColor: AppTheme.accent.withValues(alpha: 0.12),
              child: Stack(
                fit: StackFit.expand,
                children: [
                  CachedImage(
                    url: imagePath == null ? null : imageUrl,
                    fit: BoxFit.cover,
                    icon: widget.item.mediaType == MediaType.tv
                        ? Icons.live_tv_outlined
                        : Icons.movie_filter_outlined,
                    iconSize: 64,
                  ),
                  const DecoratedBox(
                    decoration: BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topCenter,
                        end: Alignment.bottomCenter,
                        stops: [0, 0.42, 1],
                        colors: [
                          Color(0x140C0805),
                          Color(0x660C0805),
                          Color(0xFA0C0805),
                        ],
                      ),
                    ),
                  ),
                  const DecoratedBox(
                    decoration: BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.centerLeft,
                        end: Alignment.centerRight,
                        colors: [Color(0xC20C0805), Color(0x000C0805)],
                        stops: [0, 0.82],
                      ),
                    ),
                  ),
                  Positioned(
                    left: desktop ? 32 : 20,
                    right: desktop ? 32 : 20,
                    top: desktop ? 28 : 20,
                    child: Row(
                      children: [
                        _HeroPill(
                          icon: Icons.auto_awesome_rounded,
                          label: widget.eyebrow.toUpperCase(),
                          foreground: AppTheme.background,
                          background: AppTheme.accent,
                        ),
                        const Spacer(),
                        if (rating != null && rating > 0)
                          _HeroPill(
                            icon: Icons.star_rounded,
                            label: rating.toStringAsFixed(1),
                            foreground: AppTheme.textPrimary,
                            background:
                                AppTheme.background.withValues(alpha: 0.68),
                          ),
                      ],
                    ),
                  ),
                  Positioned(
                    left: desktop ? 32 : 20,
                    right: desktop ? 32 : 20,
                    bottom: desktop ? 30 : 20,
                    child: ConstrainedBox(
                      constraints:
                          BoxConstraints(maxWidth: desktop ? 600 : 480),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            widget.item.title,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context)
                                .textTheme
                                .displaySmall
                                ?.copyWith(
                              color: AppTheme.textPrimary,
                              fontSize: desktop ? 42 : 30,
                              height: 1.02,
                              fontWeight: FontWeight.w800,
                              letterSpacing: -1.2,
                              shadows: const [
                                Shadow(color: Colors.black, blurRadius: 18),
                              ],
                            ),
                          ),
                          if (desktop &&
                              (widget.item.overview?.trim().isNotEmpty ??
                                  false)) ...[
                            const SizedBox(height: 10),
                            Text(
                              widget.item.overview!.trim(),
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: Theme.of(context)
                                  .textTheme
                                  .bodyLarge
                                  ?.copyWith(
                                    color: AppTheme.textSecondary,
                                    height: 1.45,
                                  ),
                            ),
                          ],
                          const SizedBox(height: 16),
                          Row(
                            children: [
                              Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 14,
                                  vertical: 9,
                                ),
                                decoration: BoxDecoration(
                                  color: AppTheme.textPrimary,
                                  borderRadius: BorderRadius.circular(99),
                                ),
                                child: const Row(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                    Icon(
                                      Icons.arrow_forward_rounded,
                                      color: AppTheme.background,
                                      size: 18,
                                    ),
                                    SizedBox(width: 7),
                                    Text(
                                      'Explore',
                                      style: TextStyle(
                                        color: AppTheme.background,
                                        fontWeight: FontWeight.w800,
                                        fontSize: 13,
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                              if (year != null) ...[
                                const SizedBox(width: 12),
                                Text(
                                  '${widget.item.mediaType.displayName}  /  $year',
                                  style: Theme.of(context)
                                      .textTheme
                                      .labelMedium
                                      ?.copyWith(
                                        color: AppTheme.textSecondary,
                                        fontWeight: FontWeight.w600,
                                        letterSpacing: 0.3,
                                      ),
                                ),
                              ],
                            ],
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
      ),
    );
  }

  String? _year(String? date) {
    if (date == null || date.length < 4) return null;
    return date.substring(0, 4);
  }
}

class _HeroPill extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color foreground;
  final Color background;

  const _HeroPill({
    required this.icon,
    required this.label,
    required this.foreground,
    required this.background,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(99),
        border: Border.all(
          color: foreground.withValues(alpha: 0.12),
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 14, color: foreground),
          const SizedBox(width: 6),
          Text(
            label,
            style: TextStyle(
              color: foreground,
              fontSize: 11,
              fontWeight: FontWeight.w800,
              letterSpacing: 0.8,
            ),
          ),
        ],
      ),
    );
  }
}
