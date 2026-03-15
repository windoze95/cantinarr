import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/radarr_models.dart';

/// List of Radarr movies with swipe actions.
class RadarrMovieList extends StatelessWidget {
  final List<RadarrMovie> movies;
  final void Function(int id) onDelete;
  final void Function(int id) onSearch;
  final bool embedded;

  const RadarrMovieList({
    super.key,
    required this.movies,
    required this.onDelete,
    required this.onSearch,
    this.embedded = false,
  });

  @override
  Widget build(BuildContext context) {
    if (movies.isEmpty) {
      return const Center(
        child: Text('No movies found',
            style: TextStyle(color: AppTheme.textSecondary)),
      );
    }

    return ListView.separated(
      shrinkWrap: embedded,
      physics: embedded ? const NeverScrollableScrollPhysics() : null,
      itemCount: movies.length,
      separatorBuilder: (_, __) =>
          const Divider(color: AppTheme.border, height: 1),
      itemBuilder: (context, index) {
        final movie = movies[index];
        return Dismissible(
          key: ValueKey(movie.id),
          direction: DismissDirection.endToStart,
          background: Container(
            color: AppTheme.error,
            alignment: Alignment.centerRight,
            padding: const EdgeInsets.only(right: 20),
            child: const Icon(Icons.delete, color: Colors.white),
          ),
          confirmDismiss: (_) => _confirmDelete(context, movie.title),
          onDismissed: (_) => onDelete(movie.id),
          child: _MovieTile(
            movie: movie,
            onSearch: () => onSearch(movie.id),
          ),
        );
      },
    );
  }

  Future<bool?> _confirmDelete(BuildContext context, String title) {
    return showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Delete Movie'),
        content: Text('Remove "$title" from Radarr?'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(foregroundColor: AppTheme.error),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
  }
}

class _MovieTile extends StatelessWidget {
  final RadarrMovie movie;
  final VoidCallback onSearch;

  const _MovieTile({required this.movie, required this.onSearch});

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: movie.posterUrl != null
              ? CachedNetworkImage(
                  imageUrl: movie.posterUrl!,
                  fit: BoxFit.cover,
                )
              : Container(
                  color: AppTheme.surfaceVariant,
                  child: const Icon(Icons.movie,
                      color: AppTheme.textSecondary, size: 20),
                ),
        ),
      ),
      title: Text(
        movie.title,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Row(
        children: [
          Text('${movie.year}',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13)),
          const SizedBox(width: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
            decoration: BoxDecoration(
              color: movie.hasFile
                  ? AppTheme.available.withValues(alpha: 0.15)
                  : movie.monitored
                      ? AppTheme.requested.withValues(alpha: 0.15)
                      : AppTheme.unavailable.withValues(alpha: 0.15),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              movie.hasFile
                  ? 'Downloaded'
                  : movie.monitored
                      ? 'Missing'
                      : 'Unmonitored',
              style: TextStyle(
                color: movie.hasFile
                    ? AppTheme.available
                    : movie.monitored
                        ? AppTheme.requested
                        : AppTheme.unavailable,
                fontSize: 11,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          if (movie.movieFile != null) ...[
            const SizedBox(width: 6),
            Text(
              movie.movieFile!.sizeFormatted,
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 11),
            ),
          ],
        ],
      ),
      trailing: IconButton(
        icon: const Icon(Icons.search, color: AppTheme.textSecondary),
        onPressed: onSearch,
        tooltip: 'Search for download',
      ),
    );
  }
}
