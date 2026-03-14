import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../../discover/data/tmdb_models.dart';

/// Grid of TV seasons with poster thumbnails.
class SeasonGrid extends StatelessWidget {
  final List<Season> seasons;

  const SeasonGrid({super.key, required this.seasons});

  @override
  Widget build(BuildContext context) {
    // Filter out specials (season 0) for cleaner display.
    final filtered = seasons.where((s) => s.seasonNumber > 0).toList();

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: GridView.builder(
        shrinkWrap: true,
        physics: const NeverScrollableScrollPhysics(),
        gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: 4,
          childAspectRatio: 0.6,
          crossAxisSpacing: 10,
          mainAxisSpacing: 10,
        ),
        itemCount: filtered.length,
        itemBuilder: (context, index) {
          final season = filtered[index];
          return Column(
            children: [
              Expanded(
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: season.posterPath != null
                      ? CachedNetworkImage(
                          imageUrl:
                              AppConfig.tmdbPoster(season.posterPath, width: 185),
                          fit: BoxFit.cover,
                          width: double.infinity,
                        )
                      : Container(
                          color: AppTheme.surfaceVariant,
                          child: Center(
                            child: Text(
                              'S${season.seasonNumber}',
                              style: const TextStyle(
                                  color: AppTheme.textSecondary,
                                  fontWeight: FontWeight.w600),
                            ),
                          ),
                        ),
                ),
              ),
              const SizedBox(height: 4),
              Text(
                season.name ?? 'Season ${season.seasonNumber}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 11),
              ),
              if (season.episodeCount != null)
                Text(
                  '${season.episodeCount} eps',
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 10),
                ),
            ],
          );
        },
      ),
    );
  }
}
