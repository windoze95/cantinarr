import 'package:cantinarr/l10n/l10n.dart';
import 'package:cantinarr/l10n/doctor_labels.dart';
import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';
import '../../logic/import_doctor.dart';
import 'queue_item_card.dart';

/// (text, colour) pair for an episode's one-line status.
typedef EpisodeStatus = ({String text, Color color});

String importBlockerText(BuildContext context, SonarrImportBlocker blocker) {
  final problem = blocker.problem;
  final reason = problem == 'Release is an unextracted archive'
      ? context.l10n.sonarrExtractBeforeImport
      : problem != null
          ? localizeDoctorLabel(context, problem)
          : blocker.reportedReason ?? context.l10n.sonarrNoImportReason;
  return context.l10n.sonarrBlockedReason(reason);
}

/// Computes the episode status line, mirroring LunaSea's priority order:
/// 1. a queued download -> import blocker, or "{pct}% — {phrase}"
/// 2. no file, already aired          -> "Missing"  (red)
/// 3. no file, not yet aired          -> "Unaired"  (info/ember)
/// 4. has file, below quality cutoff  -> "{quality} — {size}"  (orange)
/// 5. has file, meets cutoff          -> "{quality} — {size}"  (green)
EpisodeStatus episodeStatusLine(
    BuildContext context, SonarrEpisode episode, SonarrQueueItem? queue) {
  final strings = context.l10n;
  if (queue != null) {
    final blocker = sonarrImportBlocker(queue);
    if (blocker != null) {
      return (
        text: importBlockerText(context, blocker),
        color: AppTheme.requested
      );
    }
    final pct = (queue.progress * 100).round();
    return (
      text: '$pct% — ${_downloadPhrase(queue, strings: strings)}',
      color: sonarrQueueStatusColor(queue),
    );
  }
  if (!episode.hasFile) {
    return episode.hasAired
        ? (text: strings.adminMissing, color: AppTheme.error)
        : (text: strings.adminUnaired, color: AppTheme.downloading);
  }
  final file = episode.episodeFile;
  final quality = file?.quality ?? strings.adminDownloaded;
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
String _downloadPhrase(SonarrQueueItem q, {required AppLocalizations strings}) {
  switch (q.trackedDownloadState) {
    case 'importPending':
      return strings.adminDownloadedWaitingToImport;
    case 'importing':
      return strings.adminImporting;
    case 'imported':
      return strings.adminImported;
    case 'failedPending':
    case 'failed':
      return strings.adminFailed2;
  }
  if (q.trackedDownloadStatus == 'error') return strings.adminError;
  switch (q.status) {
    case 'downloading':
      return strings.adminDownloading;
    case 'paused':
      return strings.adminPaused;
    case 'queued':
      return strings.adminQueued;
    case 'delay':
      return strings.adminPending;
    case 'completed':
      return strings.adminDownloaded;
    default:
      return sonarrQueueStatusLabel(q, strings: strings);
  }
}
