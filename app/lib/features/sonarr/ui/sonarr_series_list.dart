import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/sonarr_models.dart';

/// List of Sonarr series with swipe-to-delete and progress indicators.
class SonarrSeriesList extends StatelessWidget {
  final List<SonarrSeries> series;
  final void Function(int id) onDelete;
  final void Function(int id) onSearch;

  const SonarrSeriesList({
    super.key,
    required this.series,
    required this.onDelete,
    required this.onSearch,
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
          child: _SeriesTile(show: show, onSearch: () => onSearch(show.id)),
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

  const _SeriesTile({required this.show, required this.onSearch});

  @override
  Widget build(BuildContext context) {
    final stats = show.statistics;
    final percent = show.percentComplete;

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: show.posterUrl != null
              ? CachedNetworkImage(
                  imageUrl: show.posterUrl!,
                  fit: BoxFit.cover,
                )
              : Container(
                  color: AppTheme.surfaceVariant,
                  child: const Icon(Icons.tv,
                      color: AppTheme.textSecondary, size: 20),
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
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
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
                valueColor: AlwaysStoppedAnimation(
                  percent >= 1.0 ? AppTheme.available : AppTheme.accent,
                ),
                minHeight: 4,
              ),
            ),
          ],
        ],
      ),
      trailing: IconButton(
        icon: const Icon(Icons.search, color: AppTheme.textSecondary),
        onPressed: onSearch,
        tooltip: 'Search for episodes',
      ),
    );
  }

  Color get _statusColor {
    if (show.percentComplete >= 1.0) return AppTheme.available;
    if (show.status == 'continuing') return AppTheme.downloading;
    if (show.status == 'ended') return AppTheme.textSecondary;
    return AppTheme.requested;
  }

  String get _statusText {
    if (show.percentComplete >= 1.0) return 'Complete';
    if (show.status == 'continuing') return 'Continuing';
    if (show.status == 'ended') return 'Ended';
    return 'Unknown';
  }
}
