import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/library_actions.dart';
import '../data/sonarr_models.dart';

/// Library tiles share their popup and long-press actions.
class SonarrSeriesList extends StatelessWidget {
  final List<SonarrSeries> series;
  final void Function(SonarrSeries)? onOpen;
  final void Function(SonarrSeries, LibraryAction)? onAction;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;

  const SonarrSeriesList({
    super.key,
    required this.series,
    this.onOpen,
    this.onAction,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'sonarr-library',
  });

  @override
  Widget build(BuildContext context) => LibraryCollection(
    viewMode: viewMode, scrollKey: scrollKey, embedded: embedded,
    emptyMessage: 'No series found', itemCount: series.length,
    itemBuilder: (context, index) {
      final show = series[index];
      return _SeriesTile(
        key: ValueKey(show.id), grid: viewMode == LibraryViewMode.grid,
        show: show,
        onOpen: onOpen == null ? null : () => onOpen!(show),
        onAction: onAction == null ? null : (action) => onAction!(show, action),
      );
    },
  );
}

class _SeriesTile extends StatelessWidget {
  final bool grid;
  final SonarrSeries show;
  final VoidCallback? onOpen;
  final ValueChanged<LibraryAction>? onAction;
  const _SeriesTile({super.key, required this.grid, required this.show,
    this.onOpen, this.onAction});

  @override
  Widget build(BuildContext context) {
    final stats = show.statistics;
    final percent = show.percentComplete;
    final episodeDetails = stats == null
        ? null
        : [
            '${stats.episodeFileCount}/${stats.episodeCount} episodes',
            if (!grid && stats.sizeOnDisk > 0) stats.sizeFormatted,
          ].join(' · ');

    return LibraryItem(
      grid: grid,
      name: show.title,
      onTap: onOpen,
      onLongPress: onAction == null ? null : () => showLibraryActionMenu(
        context, title: show.title, actions: libraryActions('series', monitored: show.monitored), onSelected: onAction!),
      artwork: CachedImage(
        url: show.posterUrl,
        fit: BoxFit.cover,
        icon: Icons.tv,
      ),
      details: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              if (!grid) Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: _statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  _statusText,
                  style: TextStyle(
                    color: _statusColor,
                    fontSize: 11,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
              if (episodeDetails != null)
                Text(
                  episodeDetails,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
            ],
          ),
          if (!grid && stats != null && stats.episodeCount > 0) ...[
            const SizedBox(height: 6),
            ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: percent,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(_progressColor),
                minHeight: 4,
              ),
            ),
          ],
        ],
      ),
      actions: onAction == null ? null : LibraryActionMenu(
        title: show.title, actions: libraryActions('series', monitored: show.monitored), onSelected: onAction!),
    );
  }

  Color get _statusColor => switch (show.status) {
        'continuing' => AppTheme.downloading,
        'ended' => AppTheme.textSecondary,
        'upcoming' => AppTheme.requested,
        'deleted' => AppTheme.error,
        _ => AppTheme.requested,
      };

  String get _statusText => switch (show.status) {
        'continuing' => 'Continuing',
        'ended' => 'Ended',
        'upcoming' => 'Upcoming',
        'deleted' => 'Deleted',
        _ => 'Unknown',
      };

  /// Sonarr's progress-bar grammar: green is reserved for ended series with
  /// every monitored episode on disk. A continuing series that is merely
  /// caught up shows the warm info tone (more episodes are coming), and gaps show red when
  /// monitored or amber when the admin chose not to monitor them.
  Color get _progressColor {
    if (show.percentComplete >= 1.0) {
      return show.status == 'ended' ? AppTheme.available : AppTheme.downloading;
    }
    return show.monitored ? AppTheme.error : AppTheme.requested;
  }
}
