import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/action_sheet.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'edit_series_screen.dart';

enum SeriesAction { searchMonitored, edit, refresh, remove, toggleMonitor }

/// Long-press / overflow menu for one series: shows the action sheet and runs
/// the chosen action. [onChanged] fires after anything that alters the series
/// (edit saved, monitor toggled, refresh triggered); [onRemoved] fires after a
/// successful remove instead.
Future<void> showSeriesActions(
  BuildContext context, {
  required SonarrApiService service,
  required String instanceId,
  required SonarrSeries series,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = await showActionSheet<SeriesAction>(
    context,
    title: series.title,
    actions: [
      const SheetAction(
          SeriesAction.searchMonitored, Icons.search, 'Search Monitored'),
      const SheetAction(SeriesAction.edit, Icons.edit_outlined, 'Edit Series'),
      const SheetAction(SeriesAction.refresh, Icons.refresh, 'Refresh Series'),
      const SheetAction(SeriesAction.remove, Icons.delete_outline,
          'Remove Series',
          color: AppTheme.error),
      SheetAction(
          SeriesAction.toggleMonitor,
          series.monitored ? Icons.bookmark_border : Icons.bookmark,
          series.monitored ? 'Unmonitor Series' : 'Monitor Series'),
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
      case SeriesAction.searchMonitored:
        await service.searchSeries(series.id);
        toast('Searching for monitored episodes of ${series.title}…');
      case SeriesAction.edit:
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(MaterialPageRoute(
          builder: (_) =>
              EditSeriesScreen(instanceId: instanceId, series: series),
        ));
        if (saved == true) onChanged?.call();
      case SeriesAction.refresh:
        await service.refreshSeries(series.id);
        toast('Refreshing ${series.title}…');
        onChanged?.call();
      case SeriesAction.remove:
        final deleteFiles = await confirmRemoveSeries(context, series.title);
        if (deleteFiles == null) return;
        await service.deleteSeries(series.id, deleteFiles: deleteFiles);
        toast('Removed ${series.title}');
        onRemoved?.call();
      case SeriesAction.toggleMonitor:
        await service.setSeriesMonitored(series.id,
            monitored: !series.monitored);
        toast(series.monitored
            ? 'Stopped monitoring ${series.title}'
            : 'Monitoring ${series.title}');
        onChanged?.call();
    }
  } catch (e) {
    toast('Action failed: $e');
  }
}

/// Remove confirmation with a "delete files" choice. Resolves to the
/// delete-files flag, or null when cancelled.
Future<bool?> confirmRemoveSeries(BuildContext context, String title) {
  var deleteFiles = false;
  return showDialog<bool>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (ctx, setState) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Remove Series'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Remove "$title" from Sonarr?'),
            const SizedBox(height: 8),
            CheckboxListTile(
              value: deleteFiles,
              onChanged: (v) => setState(() => deleteFiles = v ?? false),
              title: const Text('Delete files from disk',
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
            child: const Text('Remove'),
          ),
        ],
      ),
    ),
  );
}
