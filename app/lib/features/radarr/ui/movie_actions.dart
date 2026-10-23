import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import 'edit_movie_screen.dart';
import 'radarr_releases_screen.dart';

Future<void> showMovieActions(
  BuildContext context, {
  required RadarrApiService service,
  required String instanceId,
  required RadarrMovie movie,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ??
      await showActionSheet<LibraryAction>(
        context,
        title: movie.title,
        actions: libraryActions('movie', monitored: movie.monitored),
      );
  if (action == null || !context.mounted) return;

  void toast(String message) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  try {
    switch (action) {
      case LibraryAction.search:
        await service.searchMovie(movie.id);
        toast(context.l10n.adminSearchingFor2((movie.title).toString()));
      case LibraryAction.interactive:
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
            builder: (_) => RadarrReleasesScreen(
                instanceId: instanceId,
                movieId: movie.id,
                movieTitle: movie.title)));
      case LibraryAction.rescan:
        await service.rescanMovie(movie.id);
        toast(context.l10n.adminRescanQueuedFor((movie.title).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.edit:
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(AmbientPageRoute(
          builder: (_) => EditMovieScreen(instanceId: instanceId, movie: movie),
        ));
        if (saved == true && context.mounted) onChanged?.call();
      case LibraryAction.refresh:
        await service.refreshMovie(movie.id);
        toast(context.l10n.adminRefreshQueuedFor((movie.title).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context,
            title: movie.title, noun: 'movie', service: 'Radarr');
        if (deleteFiles == null) return;
        await service.deleteMovie(movie.id, deleteFiles: deleteFiles);
        toast(context.l10n.adminRemoved((movie.title).toString()));
        if (context.mounted) onRemoved?.call();
      case LibraryAction.monitoring:
        await service.setMovieMonitored(movie.id, monitored: !movie.monitored);
        toast(movie.monitored
            ? context.l10n.adminStoppedMonitoring2((movie.title).toString())
            : context.l10n.adminMonitoring2((movie.title).toString()));
        if (context.mounted) onChanged?.call();
    }
  } catch (e) {
    toast(context.l10n.adminActionFailed((e).toString()));
  }
}
