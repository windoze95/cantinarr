import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';
import 'queue_item_card.dart';

/// (text, colour) pair for an episode's one-line status.
typedef EpisodeStatus = ({String text, Color color});

/// Computes the episode status line, mirroring LunaSea's priority order:
/// 1. an active download in the queue -> "{pct}% — {phrase}"
/// 2. no file, already aired          -> "Missing"  (red)
/// 3. no file, not yet aired          -> "Unaired"  (info/ember)
/// 4. has file, below quality cutoff  -> "{quality} — {size}"  (orange)
/// 5. has file, meets cutoff          -> "{quality} — {size}"  (green)
EpisodeStatus episodeStatusLine(SonarrEpisode episode, SonarrQueueItem? queue) {
  if (queue != null) {
    final pct = (queue.progress * 100).round();
    return (
      text: '$pct% — ${_downloadPhrase(queue)}',
      color: sonarrQueueStatusColor(queue),
    );
  }
  if (!episode.hasFile) {
    return episode.hasAired
        ? (text: 'Missing', color: AppTheme.error)
        : (text: 'Unaired', color: AppTheme.downloading);
  }
  final file = episode.episodeFile;
  final quality = file?.quality ?? 'Downloaded';
  final size =
      (file != null && file.size > 0) ? ' — ${file.sizeFormatted}' : '';
  final cutoffMet = !(file?.qualityCutoffNotMet ?? false);
  return (
    text: '$quality$size',
    color: cutoffMet ? AppTheme.available : AppTheme.requested,
  );
}

/// A friendly phrase for an active download's state, e.g.
/// "Downloaded — waiting to import".
String _downloadPhrase(SonarrQueueItem q) {
  switch (q.trackedDownloadState) {
    case 'importPending':
    case 'importBlocked':
      return 'Downloaded — waiting to import';
    case 'importing':
      return 'Importing';
    case 'imported':
      return 'Imported';
    case 'failedPending':
    case 'failed':
      return 'Failed';
  }
  if (q.trackedDownloadStatus == 'error') return 'Error';
  switch (q.status) {
    case 'downloading':
      return 'Downloading';
    case 'paused':
      return 'Paused';
    case 'queued':
      return 'Queued';
    case 'delay':
      return 'Pending';
    case 'completed':
      return 'Downloaded';
    default:
      return sonarrQueueStatusLabel(q);
  }
}
