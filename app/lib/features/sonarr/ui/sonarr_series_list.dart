import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/sonarr_models.dart';

/// List of Sonarr series with swipe-to-delete and progress indicators.
/// Long-pressing a tile opens the series action sheet when [onLongPress] is
/// wired.
class SonarrSeriesList extends StatelessWidget {
  final List<SonarrSeries> series;
  final void Function(int id) onDelete;
  final void Function(int id) onSearch;
  final void Function(SonarrSeries show)? onInteractiveSearch;
  final void Function(SonarrSeries show)? onOpen;
  final void Function(SonarrSeries show)? onLongPress;
  final bool embedded;

  const SonarrSeriesList({
    super.key,
    required this.series,
    required this.onDelete,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
    this.onLongPress,
    this.embedded = false,
  });

  @override
  Widget build(BuildContext context) {
    if (series.isEmpty) {
      return const Center(
        child: Text('No series found',
            style: TextStyle(color: AppTheme.textSecondary)),
      );
    }

    return ListView.separated(
      shrinkWrap: embedded,
      physics: embedded ? const NeverScrollableScrollPhysics() : null,
      itemCount: series.length,
      separatorBuilder: (_, __) =>
          const Divider(color: AppTheme.border, height: 1),
      itemBuilder: (context, index) {
        final show = series[index];
        return Dismissible(
          key: ValueKey(show.id),
          direction: DismissDirection.endToStart,
          background: Container(
            color: AppTheme.error,
            alignment: Alignment.centerRight,
            padding: const EdgeInsets.only(right: 20),
            child: const Icon(Icons.delete, color: Colors.white),
          ),
          confirmDismiss: (_) => _confirmDelete(context, show.title),
          onDismissed: (_) => onDelete(show.id),
          child: _SeriesTile(
            show: show,
            onSearch: () => onSearch(show.id),
            onInteractiveSearch: onInteractiveSearch != null
                ? () => onInteractiveSearch!(show)
                : null,
            onOpen: onOpen != null ? () => onOpen!(show) : null,
            onLongPress: onLongPress != null ? () => onLongPress!(show) : null,
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
        title: const Text('Delete Series'),
        content: Text('Remove "$title" from Sonarr?'),
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

class _SeriesTile extends StatelessWidget {
  final SonarrSeries show;
  final VoidCallback onSearch;
  final VoidCallback? onInteractiveSearch;
  final VoidCallback? onOpen;
  final VoidCallback? onLongPress;

  const _SeriesTile({
    required this.show,
    required this.onSearch,
    this.onInteractiveSearch,
    this.onOpen,
    this.onLongPress,
  });

  @override
  Widget build(BuildContext context) {
    final stats = show.statistics;
    final percent = show.percentComplete;

    return ListTile(
      onTap: onOpen,
      onLongPress: onLongPress,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: show.posterUrl,
            fit: BoxFit.cover,
            icon: Icons.tv,
          ),
        ),
      ),
      title: Text(
        show.title,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Row(
            children: [
              if (show.year != null)
                Text('${show.year}',
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              const SizedBox(width: 8),
              Container(
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
              if (stats != null) ...[
                const SizedBox(width: 6),
                Text(
                  '${stats.episodeFileCount}/${stats.episodeCount} eps',
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
              ],
            ],
          ),
          if (stats != null && stats.episodeCount > 0) ...[
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
      trailing: PopupMenuButton<String>(
        icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
        color: AppTheme.surfaceVariant,
        tooltip: 'Actions',
        onSelected: (value) {
          switch (value) {
            case 'search':
              onSearch();
            case 'interactive':
              onInteractiveSearch?.call();
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
        ],
      ),
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
  /// caught up shows blue (more episodes are coming), and gaps show red when
  /// monitored or amber when the admin chose not to monitor them.
  Color get _progressColor {
    if (show.percentComplete >= 1.0) {
      return show.status == 'ended' ? AppTheme.available : AppTheme.downloading;
    }
    return show.monitored ? AppTheme.error : AppTheme.requested;
  }
}
