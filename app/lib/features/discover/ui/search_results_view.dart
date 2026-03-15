import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:shimmer/shimmer.dart';
import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../../person/ui/person_detail_sheet.dart';
import '../data/tmdb_models.dart';

/// Library presence indicator for search results.
class LibraryStatus {
  final String label;
  final Color color;
  const LibraryStatus({required this.label, required this.color});
}

/// List view of search results with poster thumbnails and metadata.
class SearchResultsView extends StatelessWidget {
  final List<MediaItem> results;
  final bool isLoading;
  final String query;
  final void Function(MediaItem)? onLoadMore;
  final Map<int, LibraryStatus> libraryStatus;

  const SearchResultsView({
    super.key,
    required this.results,
    required this.isLoading,
    required this.query,
    this.onLoadMore,
    this.libraryStatus = const {},
  });

  @override
  Widget build(BuildContext context) {
    if (results.isEmpty && isLoading) {
      return _buildLoadingList();
    }

    if (results.isEmpty && !isLoading && query.isNotEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.search_off,
                size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 12),
            Text(
              'No results for "$query"',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 16),
            ),
            const SizedBox(height: 4),
            const Text(
              'Try a different search term',
              style:
                  TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ],
        ),
      );
    }

    return ListView.separated(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      itemCount: results.length + (isLoading ? 3 : 0),
      separatorBuilder: (_, __) => const SizedBox.shrink(),
      itemBuilder: (context, index) {
        if (index >= results.length) {
          return _buildShimmerRow();
        }
        final item = results[index];
        if (onLoadMore != null && index >= results.length - 5) {
          onLoadMore!(item);
        }
        return _SearchResultTile(
          item: item,
          status: libraryStatus[item.id],
        );
      },
    );
  }

  Widget _buildLoadingList() {
    return ListView.separated(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      itemCount: 8,
      separatorBuilder: (_, __) => const SizedBox(height: 12),
      itemBuilder: (_, __) => _buildShimmerRow(),
    );
  }

  Widget _buildShimmerRow() {
    return Shimmer.fromColors(
      baseColor: AppTheme.surfaceVariant,
      highlightColor: AppTheme.surface.withValues(alpha: 0.5),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 50,
              height: 75,
              decoration: BoxDecoration(
                color: AppTheme.surfaceVariant,
                borderRadius: BorderRadius.circular(6),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    height: 13,
                    width: 160,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Container(
                    height: 10,
                    width: 60,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Container(
                    height: 10,
                    width: double.infinity,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                  const SizedBox(height: 4),
                  Container(
                    height: 10,
                    width: 180,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SearchResultTile extends StatelessWidget {
  final MediaItem item;
  final LibraryStatus? status;

  static const _titleStyle = TextStyle(
    color: AppTheme.textPrimary,
    fontSize: 14,
    fontWeight: FontWeight.w600,
  );

  const _SearchResultTile({required this.item, this.status});

  @override
  Widget build(BuildContext context) {
    if (item.mediaType == MediaType.person) {
      return _buildPersonTile(context);
    }
    return _buildMediaTile(context);
  }

  Widget _buildPersonTile(BuildContext context) {
    final imageUrl = item.posterPath != null &&
            item.posterPath!.startsWith('http')
        ? item.posterPath!
        : AppConfig.tmdbPoster(item.posterPath, width: 154);

    return GestureDetector(
      onTap: () => showPersonDetailSheet(
        context,
        personId: item.id,
        personName: item.title,
        profilePath: item.posterPath,
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          // Circular profile thumbnail
          ClipOval(
            child: SizedBox(
              width: 50,
              height: 50,
              child: item.posterPath != null
                  ? CachedNetworkImage(
                      imageUrl: imageUrl,
                      fit: BoxFit.cover,
                      placeholder: (_, __) => Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.person,
                              color: AppTheme.textSecondary, size: 18),
                        ),
                      ),
                      errorWidget: (_, __, ___) => Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.person,
                              color: AppTheme.textSecondary, size: 18),
                        ),
                      ),
                    )
                  : Container(
                      color: AppTheme.surfaceVariant,
                      child: const Center(
                        child: Icon(Icons.person,
                            color: AppTheme.textSecondary, size: 18),
                      ),
                    ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  item.title,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: _titleStyle,
                ),
                const SizedBox(height: 3),
                const Text(
                  'Person',
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
      ),
    );
  }

  Widget _buildMediaTile(BuildContext context) {
    final imageUrl = item.posterPath != null &&
            item.posterPath!.startsWith('http')
        ? item.posterPath!
        : AppConfig.tmdbPoster(item.posterPath, width: 154);

    final year = item.releaseDate != null && item.releaseDate!.length >= 4
        ? item.releaseDate!.substring(0, 4)
        : null;

    return GestureDetector(
      onTap: () => context.push(
        '/detail/${item.mediaType.name}/${item.id}',
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Poster thumbnail
            ClipRRect(
              borderRadius: BorderRadius.circular(6),
              child: SizedBox(
                width: 50,
                height: 75,
                child: item.posterPath != null
                    ? CachedNetworkImage(
                        imageUrl: imageUrl,
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.movie_outlined,
                                color: AppTheme.textSecondary, size: 18),
                          ),
                        ),
                        errorWidget: (_, __, ___) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.broken_image_outlined,
                                color: AppTheme.textSecondary, size: 18),
                          ),
                        ),
                      )
                    : Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.movie_outlined,
                              color: AppTheme.textSecondary, size: 18),
                        ),
                      ),
              ),
            ),
            const SizedBox(width: 10),
            // Metadata column
            Expanded(
              child: LayoutBuilder(
                builder: (context, constraints) {
                  // Measure title line count to adjust description lines
                  final titlePainter = TextPainter(
                    text: TextSpan(text: item.title, style: _titleStyle),
                    maxLines: 3,
                    textDirection: TextDirection.ltr,
                  )..layout(maxWidth: constraints.maxWidth);
                  final titleLines =
                      titlePainter.computeLineMetrics().length;
                  final descMaxLines =
                      titleLines <= 1 ? 2 : titleLines <= 2 ? 1 : 0;
                  final hasOverview = descMaxLines > 0 &&
                      item.overview != null &&
                      item.overview!.isNotEmpty;

                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      // Title (up to 3 lines)
                      Text(
                        item.title,
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                        style: _titleStyle,
                      ),
                      const SizedBox(height: 3),
                      // Year + library status + rating
                      Row(
                        children: [
                          if (year != null)
                            Text(
                              year,
                              style: const TextStyle(
                                color: AppTheme.textSecondary,
                                fontSize: 12,
                              ),
                            ),
                          if (status != null) ...[
                            const SizedBox(width: 6),
                            _Chip(
                              label: status!.label,
                              color: status!.color,
                              backgroundColor:
                                  status!.color.withValues(alpha: 0.15),
                            ),
                          ],
                          if (item.voteAverage != null &&
                              item.voteAverage! > 0) ...[
                            const Spacer(),
                            const Icon(Icons.star_rounded,
                                size: 13, color: AppTheme.accent),
                            const SizedBox(width: 2),
                            Text(
                              item.voteAverage!.toStringAsFixed(1),
                              style: const TextStyle(
                                color: AppTheme.textSecondary,
                                fontSize: 11,
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                          ],
                        ],
                      ),
                      // Overview (lines adapt to title length)
                      if (hasOverview) ...[
                        const SizedBox(height: 4),
                        Text(
                          item.overview!,
                          maxLines: descMaxLines,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 11,
                            height: 1.3,
                          ),
                        ),
                      ],
                    ],
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  final String label;
  final Color color;
  final Color backgroundColor;

  const _Chip({
    required this.label,
    required this.color,
    required this.backgroundColor,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: backgroundColor,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}
