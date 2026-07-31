import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/radarr_models.dart';

/// List of Radarr movies with explicit, keyboard-accessible row actions.
class RadarrMovieList extends StatelessWidget {
  final List<RadarrMovie> movies;
  final void Function(int id, {bool deleteFiles}) onDelete;
  final void Function(int id) onSearch;
  final void Function(RadarrMovie movie)? onInteractiveSearch;
  final void Function(RadarrMovie movie)? onOpen;
  final bool embedded;

  const RadarrMovieList({
    super.key,
    required this.movies,
    required this.onDelete,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
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
        return _MovieTile(
          movie: movie,
          onDelete: () async {
            final deleteFiles = await _confirmDelete(context, movie.title);
            if (deleteFiles == null) return;
            onDelete(movie.id, deleteFiles: deleteFiles);
          },
          onSearch: () => onSearch(movie.id),
          onInteractiveSearch: onInteractiveSearch != null
              ? () => onInteractiveSearch!(movie)
              : null,
          onOpen: onOpen != null ? () => onOpen!(movie) : null,
        );
      },
    );
  }

  /// Delete confirmation with an opt-in "also delete files" choice.
  /// Resolves to the delete-files flag, or null when cancelled.
  Future<bool?> _confirmDelete(BuildContext context, String title) {
    var deleteFiles = false;
    return showDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          backgroundColor: AppTheme.surface,
          title: const Text('Delete Movie'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Remove "$title" from Radarr?'),
              const SizedBox(height: 8),
              CheckboxListTile(
                value: deleteFiles,
                onChanged: (v) => setState(() => deleteFiles = v ?? false),
                title: const Text('Also delete files from disk',
                    style: TextStyle(fontSize: 14)),
                contentPadding: EdgeInsets.zero,
                controlAffinity: ListTileControlAffinity.leading,
                activeColor: AppTheme.error,
              ),
            ],
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('Cancel')),
            TextButton(
              onPressed: () => Navigator.pop(ctx, deleteFiles),
              style: TextButton.styleFrom(foregroundColor: AppTheme.error),
              child: const Text('Delete'),
            ),
          ],
        ),
      ),
    );
  }
}

class _MovieTile extends StatelessWidget {
  final RadarrMovie movie;
  final VoidCallback onDelete;
  final VoidCallback onSearch;
  final VoidCallback? onInteractiveSearch;
  final VoidCallback? onOpen;

  const _MovieTile({
    required this.movie,
    required this.onDelete,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      onTap: onOpen,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: movie.posterUrl,
            fit: BoxFit.cover,
            icon: Icons.movie,
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
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
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
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
            ),
          ],
        ],
      ),
      trailing: PopupMenuButton<String>(
        icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
        color: AppTheme.surfaceVariant,
        tooltip: 'Actions for ${movie.title}',
        onSelected: (value) {
          switch (value) {
            case 'search':
              onSearch();
            case 'interactive':
              onInteractiveSearch?.call();
            case 'delete':
              onDelete();
          }
        },
        itemBuilder: (_) => [
          const PopupMenuItem(
            value: 'search',
            child: Row(
              children: [
                Icon(Icons.search, size: 18, color: AppTheme.textSecondary),
                SizedBox(width: 10),
                Text('Automatic search'),
              ],
            ),
          ),
          if (onInteractiveSearch != null)
            const PopupMenuItem(
              value: 'interactive',
              child: Row(
                children: [
                  Icon(Icons.manage_search,
                      size: 18, color: AppTheme.textSecondary),
                  SizedBox(width: 10),
                  Text('Interactive search'),
                ],
              ),
            ),
          const PopupMenuDivider(),
          const PopupMenuItem(
            value: 'delete',
            child: Row(
              children: [
                Icon(Icons.delete_outline, size: 18, color: AppTheme.error),
                SizedBox(width: 10),
                Text('Delete…', style: TextStyle(color: AppTheme.error)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
