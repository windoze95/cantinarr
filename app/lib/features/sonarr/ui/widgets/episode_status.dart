import 'package:cantinarr/l10n/l10n.dart';
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
EpisodeStatus episodeStatusLine(SonarrEpisode episode, SonarrQueueItem? queue,
    {AppLocalizations? strings}) {
  if (queue != null) {
    final pct = (queue.progress * 100).round();
    return (
      text: '$pct% — ${_downloadPhrase(queue, strings: strings)}',
      color: sonarrQueueStatusColor(queue),
    );
  }
  if (!episode.hasFile) {
    return episode.hasAired
        ? (
            text: (strings ?? lookupAppLocalizations(const Locale('en')))
                .adminMissing,
            color: AppTheme.error
          )
        : (
            text: (strings ?? lookupAppLocalizations(const Locale('en')))
                .adminUnaired,
            color: AppTheme.downloading
          );
  }
  final file = episode.episodeFile;
  final quality = file?.quality ??
      (strings ?? lookupAppLocalizations(const Locale('en'))).adminDownloaded;
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
String _downloadPhrase(SonarrQueueItem q, {AppLocalizations? strings}) {
  switch (q.trackedDownloadState) {
    case 'importPending':
    case 'importBlocked':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminDownloadedWaitingToImport;
    case 'importing':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminImporting;
    case 'imported':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminImported;
    case 'failedPending':
    case 'failed':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminFailed2;
  }
  if (q.trackedDownloadStatus == 'error')
    return (strings ?? lookupAppLocalizations(const Locale('en'))).adminError;
  switch (q.status) {
    case 'downloading':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminDownloading;
    case 'paused':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminPaused;
    case 'queued':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminQueued;
    case 'delay':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminPending;
    case 'completed':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminDownloaded;
    default:
      return sonarrQueueStatusLabel(q, strings: strings);
  }
}
