import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import 'edit_movie_screen.dart';

enum MovieAction { search, edit, refresh, remove, toggleMonitor }

/// Overflow menu for one movie, the movie-side mirror of the series action
/// sheet: shows the action sheet and runs the chosen action. [onChanged] fires
/// after anything that alters the movie (edit saved, monitor toggled, refresh
/// triggered); [onRemoved] fires after a successful remove instead.
Future<void> showMovieActions(
  BuildContext context, {
  required RadarrApiService service,
  required String instanceId,
  required RadarrMovie movie,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = await showActionSheet<MovieAction>(
    context,
    title: movie.title,
    actions: [
      const SheetAction(MovieAction.search, Icons.search, 'Search Movie'),
      const SheetAction(MovieAction.edit, Icons.edit_outlined, 'Edit Movie'),
      const SheetAction(MovieAction.refresh, Icons.refresh, 'Refresh Movie'),
      const SheetAction(MovieAction.remove, Icons.delete_outline, 'Remove Movie',
          color: AppTheme.error),
      SheetAction(
          MovieAction.toggleMonitor,
          movie.monitored ? Icons.bookmark_border : Icons.bookmark,
          movie.monitored ? 'Unmonitor Movie' : 'Monitor Movie'),
    ],
  );
  if (action == null || !context.mounted) return;

  void toast(String message) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  try {
    switch (action) {
      case MovieAction.search:
        await service.searchMovie(movie.id);
        toast('Searching for ${movie.title}…');
      case MovieAction.edit:
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(AmbientPageRoute(
          builder: (_) => EditMovieScreen(instanceId: instanceId, movie: movie),
        ));
        if (saved == true) onChanged?.call();
      case MovieAction.refresh:
        await service.refreshMovie(movie.id);
        toast('Refreshing ${movie.title}…');
        onChanged?.call();
      case MovieAction.remove:
        final deleteFiles = await confirmRemoveMovie(context, movie.title);
        if (deleteFiles == null) return;
        await service.deleteMovie(movie.id, deleteFiles: deleteFiles);
        toast('Removed ${movie.title}');
        onRemoved?.call();
      case MovieAction.toggleMonitor:
        await service.setMovieMonitored(movie.id, monitored: !movie.monitored);
        toast(movie.monitored
            ? 'Stopped monitoring ${movie.title}'
            : 'Monitoring ${movie.title}');
        onChanged?.call();
    }
  } catch (e) {
    toast('Action failed: $e');
  }
}

/// Remove confirmation with a "delete files" choice. Resolves to the
/// delete-files flag, or null when cancelled. Files are kept by default.
Future<bool?> confirmRemoveMovie(BuildContext context, String title) {
  var deleteFiles = false;
  return showDialog<bool>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (ctx, setState) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Remove Movie'),
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
              onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, deleteFiles),
            style: TextButton.styleFrom(foregroundColor: AppTheme.error),
            child: const Text('Remove'),
          ),
        ],
      ),
    ),
  );
}
