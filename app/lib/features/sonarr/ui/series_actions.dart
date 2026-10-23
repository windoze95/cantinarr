import 'package:cantinarr/l10n/l10n.dart';
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
  final action = selectedAction ??
      await showActionSheet<LibraryAction>(
        context,
        title: series.title,
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
        toast(context.l10n
            .adminSearchingForMonitoredEpisodesOf((series.title).toString()));
      case LibraryAction.interactive:
        final fresh = await service.getSeriesById(series.id);
        if (!context.mounted) return;
        final seasons = [...fresh.seasons]
          ..sort((a, b) => a.seasonNumber.compareTo(b.seasonNumber));
        if (seasons.isEmpty) {
          toast(context.l10n.adminNoSeasonsAvailable);
          return;
        }
        final season = await showActionSheet<int>(context,
            title: context.l10n.adminSelectSeason2,
            actions: [
              for (final s in seasons)
                SheetAction(
                    s.seasonNumber,
                    Icons.tv,
                    s.seasonNumber == 0
                        ? context.l10n.adminSpecials
                        : context.l10n.adminSeason((s.seasonNumber).toString()))
            ]);
        if (season == null || !context.mounted) return;
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
            builder: (_) => SonarrReleasesScreen(
                instanceId: instanceId,
                seriesId: series.id,
                seasonNumber: season,
                seriesTitle: series.title)));
      case LibraryAction.rescan:
        await service.rescanSeries(series.id);
        toast(context.l10n.adminRescanQueuedFor((series.title).toString()));
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
        toast(context.l10n.adminRefreshQueuedFor((series.title).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context,
            title: series.title, noun: 'series', service: 'Sonarr');
        if (deleteFiles == null) return;
        await service.deleteSeries(series.id, deleteFiles: deleteFiles);
        toast(context.l10n.adminRemoved((series.title).toString()));
        if (context.mounted) onRemoved?.call();
      case LibraryAction.monitoring:
        await service.setSeriesMonitored(series.id,
            monitored: !series.monitored);
        toast(series.monitored
            ? context.l10n.adminStoppedMonitoring2((series.title).toString())
            : context.l10n.adminMonitoring2((series.title).toString()));
        if (context.mounted) onChanged?.call();
    }
  } catch (e) {
    toast(context.l10n.adminActionFailed((e).toString()));
  }
}
