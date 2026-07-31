import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../core/widgets/status_pill.dart';
import '../../data/chaptarr_models.dart';
import 'format_badge.dart';

/// Short status label for a queue item (e.g. "Import pending", "Downloading").
String chaptarrQueueStatusLabel(ChaptarrQueueItem item) {
  if (item.trackedDownloadStatus == 'error' || item.status == 'failed') {
    return 'Error';
  }
  switch (item.trackedDownloadState) {
    case 'importPending':
      return 'Import pending';
    case 'importing':
      return 'Importing';
    case 'imported':
      return 'Imported';
    case 'importBlocked':
      return 'Import blocked';
    case 'failedPending':
      return 'Failed';
  }
  if (item.trackedDownloadStatus == 'warning') return 'Warning';
  switch (item.status) {
    case 'downloading':
      return 'Downloading';
    case 'paused':
      return 'Paused';
    case 'queued':
      return 'Queued';
    case 'completed':
      return 'Completed';
    case 'delay':
      return 'Delayed';
    case 'downloadClientUnavailable':
      return 'Client unavailable';
    case 'warning':
      return 'Warning';
    default:
      return item.status.isEmpty ? 'Unknown' : item.status;
  }
}

/// Status colour for a queue item, matched to the design system tokens.
Color chaptarrQueueStatusColor(ChaptarrQueueItem item) {
  if (item.trackedDownloadStatus == 'error' ||
      item.status == 'failed' ||
      item.trackedDownloadState == 'failedPending') {
    return AppTheme.error;
  }
  if (item.trackedDownloadStatus == 'warning' || item.status == 'warning') {
    return AppTheme.requested;
  }
  switch (item.trackedDownloadState) {
    case 'importPending':
    case 'importing':
    case 'importBlocked':
      return AppTheme.requested;
    case 'imported':
      return AppTheme.available;
  }
  switch (item.status) {
    case 'downloading':
      return AppTheme.downloading;
    case 'completed':
      return AppTheme.available;
    case 'paused':
    case 'queued':
    case 'delay':
    case 'downloadClientUnavailable':
      return AppTheme.unavailable;
    default:
      return AppTheme.downloading;
  }
}

/// A rich queue item card: status, protocol, indexer, download client, progress
/// bar, time left and any error/status messages. Shared by the Chaptarr queue
/// screen and the per-book detail sheet.
class ChaptarrQueueItemCard extends StatelessWidget {
  final ChaptarrQueueItem item;

  /// When provided, an "Issues" affordance opens this callback instead of
  /// showing the inline issues box (used by the Import Doctor).
  final VoidCallback? onTap;

  /// When provided, a Remove action is offered in the overflow menu.
  final VoidCallback? onRemove;

  const ChaptarrQueueItemCard({
    super.key,
    required this.item,
    this.onTap,
    this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    final statusColor = chaptarrQueueStatusColor(item);
    final issues = <String>[
      if (item.errorMessage != null && item.errorMessage!.isNotEmpty)
        item.errorMessage!,
      ...item.statusMessages,
    ];
    final protocol = item.protocol;
    final bookLabel = item.bookLabel;

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.fromLTRB(12, 10, 4, 12),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      bookLabel ?? item.title,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontWeight: FontWeight.w600,
                          fontSize: 14),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (bookLabel != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 2),
                        child: Text(
                          item.title,
                          style: const TextStyle(
                              color: AppTheme.textSecondary, fontSize: 11),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                ),
              ),
              if (onRemove != null)
                PopupMenuButton<String>(
                  icon: const Icon(Icons.more_vert,
                      color: AppTheme.textSecondary, size: 20),
                  color: AppTheme.surfaceVariant,
                  onSelected: (value) {
                    if (value == 'remove') onRemove!();
                  },
                  itemBuilder: (_) => const [
                    PopupMenuItem(
                      value: 'remove',
                      child: Row(
                        children: [
                          Icon(Icons.delete_outline,
                              size: 18, color: AppTheme.error),
                          SizedBox(width: 8),
                          Text('Remove'),
                        ],
                      ),
                    ),
                  ],
                )
              else
                const SizedBox(width: 8),
            ],
          ),
          const SizedBox(height: 6),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                StatusPill(
                    text: chaptarrQueueStatusLabel(item), color: statusColor),
                if (item.format != BookFormat.unknown)
                  ChaptarrFormatBadge(format: item.format),
                StatusPill(
                  text: protocol.toUpperCase(),
                  color: protocol == 'torrent'
                      ? AppTheme.downloading
                      : AppTheme.available,
                ),
                if (item.quality != null)
                  StatusPill(text: item.quality!, color: AppTheme.accent),
                if (item.indexer != null && item.indexer!.isNotEmpty)
                  StatusPill(
                      text: item.indexer!, color: AppTheme.textSecondary),
                if (item.downloadClient != null &&
                    item.downloadClient!.isNotEmpty)
                  StatusPill(
                      text: item.downloadClient!,
                      color: AppTheme.textSecondary),
              ],
            ),
          ),
          const SizedBox(height: 10),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: item.progress,
                minHeight: 5,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(statusColor),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    '${(item.progress * 100).toStringAsFixed(1)}% • '
                    '${item.downloadedFormatted} of ${item.sizeFormatted}',
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 11),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (item.timeleft != null && item.timeleft!.isNotEmpty)
                  Text(
                    item.timeleft!,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 11),
                  ),
              ],
            ),
          ),
          if (item.hasIssues && issues.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8, right: 8),
              child: onTap != null
                  ? _IssuesButton(count: issues.length, onTap: onTap!)
                  : _IssuesBox(text: issues.join('\n')),
            ),
        ],
      ),
    );
  }
}

/// The inline amber issues box (default rendering when no Doctor handler set).
class _IssuesBox extends StatelessWidget {
  final String text;
  const _IssuesBox({required this.text});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: AppTheme.error.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.warning_amber_rounded,
              size: 16, color: AppTheme.requested),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              text,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
            ),
          ),
        ],
      ),
    );
  }
}

/// A tappable "Messages" affordance that defers to the Import Doctor.
class _IssuesButton extends StatelessWidget {
  final int count;
  final VoidCallback onTap;
  const _IssuesButton({required this.count, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
        decoration: BoxDecoration(
          color: AppTheme.requested.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(6),
        ),
        child: Row(
          children: [
            const Icon(Icons.warning_amber_rounded,
                size: 16, color: AppTheme.requested),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                count == 1
                    ? '1 message — tap to resolve'
                    : '$count messages — tap to resolve',
                style: const TextStyle(
                    color: AppTheme.requested,
                    fontSize: 12,
                    fontWeight: FontWeight.w500),
              ),
            ),
            const Icon(Icons.chevron_right,
                size: 18, color: AppTheme.requested),
          ],
        ),
      ),
    );
  }
}
