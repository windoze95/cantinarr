import 'package:flutter/material.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'edit_series_screen.dart';
import 'sonarr_releases_screen.dart';

Future<void> showSeriesActions(
  BuildContext context, {
  required SonarrApiService service,
  required String instanceId,
  required SonarrSeries series,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ?? await showActionSheet<LibraryAction>(
    context, title: series.title,
    actions: libraryActions('series', monitored: series.monitored),
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
        await service.searchSeries(series.id);
        toast('Searching for monitored episodes of ${series.title}…');
      case LibraryAction.interactive:
        final fresh = await service.getSeriesById(series.id);
        if (!context.mounted) return;
        final seasons = [...fresh.seasons]
          ..sort((a, b) => a.seasonNumber.compareTo(b.seasonNumber));
        if (seasons.isEmpty) { toast('No seasons available'); return; }
        final season = await showActionSheet<int>(context,
          title: 'Select season', actions: [for (final s in seasons)
            SheetAction(s.seasonNumber, Icons.tv,
              s.seasonNumber == 0 ? 'Specials' : 'Season ${s.seasonNumber}')]);
        if (season == null || !context.mounted) return;
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
          builder: (_) => SonarrReleasesScreen(instanceId: instanceId,
            seriesId: series.id, seasonNumber: season, seriesTitle: series.title)));
      case LibraryAction.rescan:
        await service.rescanSeries(series.id);
        toast('Rescan queued for ${series.title}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.edit:
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(AmbientPageRoute(
          builder: (_) =>
              EditSeriesScreen(instanceId: instanceId, series: series),
        ));
        if (saved == true && context.mounted) onChanged?.call();
      case LibraryAction.refresh:
        await service.refreshSeries(series.id);
        toast('Refresh queued for ${series.title}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context, title: series.title, noun: 'series', service: 'Sonarr');
        if (deleteFiles == null) return;
        await service.deleteSeries(series.id, deleteFiles: deleteFiles);
        toast('Removed ${series.title}');
        if (context.mounted) onRemoved?.call();
      case LibraryAction.monitoring:
        await service.setSeriesMonitored(series.id,
            monitored: !series.monitored);
        toast(series.monitored
            ? 'Stopped monitoring ${series.title}'
            : 'Monitoring ${series.title}');
        if (context.mounted) onChanged?.call();
    }
  } catch (e) {
    toast('Action failed: $e');
  }
}
