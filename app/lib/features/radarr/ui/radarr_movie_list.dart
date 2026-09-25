import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/radarr_models.dart';

/// List or artwork grid of Radarr movies
/// with explicit, keyboard-accessible actions.
/// Long-pressing a tile opens the movie action sheet when [onLongPress] is
/// wired.
class RadarrMovieList extends StatelessWidget {
  final List<RadarrMovie> movies;
  final void Function(int id, {bool deleteFiles}) onDelete;
  final void Function(int id) onSearch;
  final void Function(RadarrMovie movie)? onInteractiveSearch;
  final void Function(RadarrMovie movie)? onOpen;
  final void Function(RadarrMovie movie)? onLongPress;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;

  const RadarrMovieList({
    super.key,
    required this.movies,
    required this.onDelete,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
    this.onLongPress,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'radarr-library',
  });

  @override
  Widget build(BuildContext context) {
    return LibraryCollection(
      viewMode: viewMode,
      scrollKey: scrollKey,
      embedded: embedded,
      emptyMessage: 'No movies found',
      itemCount: movies.length,
      itemBuilder: (context, index) {
        final movie = movies[index];
        return _MovieTile(
          key: ValueKey(movie.id),
          grid: viewMode == LibraryViewMode.grid,
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
          onLongPress: onLongPress != null ? () => onLongPress!(movie) : null,
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
  final bool grid;
  final RadarrMovie movie;
  final VoidCallback onDelete;
  final VoidCallback onSearch;
  final VoidCallback? onInteractiveSearch;
  final VoidCallback? onOpen;
  final VoidCallback? onLongPress;

  const _MovieTile({
    super.key,
    required this.grid,
    required this.movie,
    required this.onDelete,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
    this.onLongPress,
  });

  @override
  Widget build(BuildContext context) {
    return LibraryItem(
      grid: grid,
      name: movie.title,
      onTap: onOpen,
      onLongPress: onLongPress,
      artwork: CachedImage(
        url: movie.posterUrl,
        fit: BoxFit.cover,
        icon: Icons.movie,
      ),
      details: Wrap(
        spacing: 6,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          Text('${movie.year}',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          if (!grid) Container(
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
          if (!grid && movie.movieFile != null) ...[
            Text(
              movie.movieFile!.sizeFormatted,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
            ),
          ],
        ],
      ),
      actions: PopupMenuButton<String>(
        icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
        color: AppTheme.surfaceVariant,
        tooltip: 'Actions for ${movie.title}',
        onSelected: (value) {
          switch (value) {
            case 'search':
              onSearch();
            case 'interactive':
              onInteractiveSearch?.call();
            case 'more':
              onLongPress?.call();
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
                Flexible(child: Text('Automatic search')),
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
                  Flexible(child: Text('Interactive search')),
                ],
              ),
            ),
          if (onLongPress != null)
            const PopupMenuItem(
              value: 'more',
              child: Row(
                children: [
                  Icon(
                    Icons.more_horiz_rounded,
                    size: 18,
                    color: AppTheme.textSecondary,
                  ),
                  SizedBox(width: 10),
                  Flexible(child: Text('More actions…')),
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
