import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/library_actions.dart';
import '../data/radarr_models.dart';

/// Library tiles share their popup and long-press actions.
class RadarrMovieList extends StatelessWidget {
  final List<RadarrMovie> movies;
  final void Function(RadarrMovie)? onOpen;
  final void Function(RadarrMovie, LibraryAction)? onAction;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;

  const RadarrMovieList({
    super.key,
    required this.movies,
    this.onOpen,
    this.onAction,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'radarr-library',
  });

  @override
  Widget build(BuildContext context) => LibraryCollection(
    viewMode: viewMode, scrollKey: scrollKey, embedded: embedded,
    emptyMessage: 'No movies found', itemCount: movies.length,
    itemBuilder: (context, index) {
      final movie = movies[index];
      return _MovieTile(
        key: ValueKey(movie.id), grid: viewMode == LibraryViewMode.grid,
        movie: movie,
        onOpen: onOpen == null ? null : () => onOpen!(movie),
        onAction: onAction == null ? null : (action) => onAction!(movie, action),
      );
    },
  );
}

class _MovieTile extends StatelessWidget {
  final bool grid;
  final RadarrMovie movie;
  final VoidCallback? onOpen;
  final ValueChanged<LibraryAction>? onAction;
  const _MovieTile({super.key, required this.grid, required this.movie,
    this.onOpen, this.onAction});

  @override
  Widget build(BuildContext context) {
    return LibraryItem(
      grid: grid,
      name: movie.title,
      onTap: onOpen,
      onLongPress: onAction == null ? null : () => showLibraryActionMenu(
        context, title: movie.title, actions: libraryActions('movie', monitored: movie.monitored), onSelected: onAction!),
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
      actions: onAction == null ? null : LibraryActionMenu(
        title: movie.title, actions: libraryActions('movie', monitored: movie.monitored), onSelected: onAction!),
    );
  }
}
