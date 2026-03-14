import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/shimmer_loading.dart';
import '../data/tmdb_models.dart';

/// Grid view of search results.
class SearchResultsView extends StatelessWidget {
  final List<MediaItem> results;
  final bool isLoading;
  final String query;
  final void Function(MediaItem)? onLoadMore;
  final String tmdbApiKey;

  const SearchResultsView({
    super.key,
    required this.results,
    required this.isLoading,
    required this.query,
    this.onLoadMore,
    required this.tmdbApiKey,
  });

  @override
  Widget build(BuildContext context) {
    if (results.isEmpty && isLoading) {
      return _buildLoadingGrid();
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

    return GridView.builder(
      padding: const EdgeInsets.all(16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 3,
        childAspectRatio: 0.55,
        crossAxisSpacing: 12,
        mainAxisSpacing: 12,
      ),
      itemCount: results.length + (isLoading ? 3 : 0),
      itemBuilder: (context, index) {
        if (index >= results.length) {
          return const ShimmerCard(width: double.infinity);
        }
        final item = results[index];
        if (onLoadMore != null && index >= results.length - 5) {
          onLoadMore!(item);
        }
        return MediaCard(
          id: item.id,
          title: item.title,
          posterPath: item.posterPath,
          width: double.infinity,
          onTap: () => context.push(
            '/detail/${item.mediaType.name}/${item.id}',
          ),
        );
      },
    );
  }

  Widget _buildLoadingGrid() {
    return GridView.builder(
      padding: const EdgeInsets.all(16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 3,
        childAspectRatio: 0.55,
        crossAxisSpacing: 12,
        mainAxisSpacing: 12,
      ),
      itemCount: 9,
      itemBuilder: (_, __) => const ShimmerCard(width: double.infinity),
    );
  }
}
