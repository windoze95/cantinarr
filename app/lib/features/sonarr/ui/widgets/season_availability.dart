import 'dart:math' as math;

import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';

/// (text, colour) pair for the availability line on a season card.
typedef SeasonAvailability = ({String text, Color color});

/// Builds "11/13 Episodes Available • 2 unaired" for one season — or, from the
/// series-level statistics, for the whole series.
///
/// The denominator is every episode Sonarr knows about, not its `episodeCount`:
/// that one drops unaired and unmonitored episodes, so a 13-episode season with
/// two still to come reads "100% • 11/11 Episodes Available" and looks finished
/// next to an episode list that has 13 rows. Sonarr can get away with the same
/// denominator because it renders the queue in the label too ("11 + 2 / 11") —
/// so this line takes [queue], the season's slice of `queue/details`, and does
/// the same accounting in words.
///
/// The suffix splits the remainder into buckets that add back up to the
/// denominator, using the episode list's own vocabulary: episodes already in
/// flight are "downloading" (or "waiting to import" once every one of them has
/// finished transferring), episodes that aired and are wanted but absent are
/// "missing", and what is left is "unaired" while the season still has an air
/// date pending ([moreToCome]) or "unmonitored" when it does not.
///
/// Colour follows the library tile's grammar: green only once every episode is
/// on disk, red for a gap nothing is working on, amber for a gap nobody
/// monitors, and the info tone for a season that is caught up or busy.
SeasonAvailability seasonAvailabilityLine(
  SonarrStatistics? stats, {
  required bool moreToCome,
  List<SonarrQueueItem> queue = const [],
}) {
  final files = stats?.episodeFileCount ?? 0;
  final obtainable = stats?.episodeCount ?? 0;
  // Older payloads (and Sonarr's series-level stats before v3) can omit
  // totalEpisodeCount; fall back to the obtainable count rather than dividing
  // by a zero the season plainly is not.
  final total = math.max(stats?.totalEpisodeCount ?? 0, obtainable);
  if (total == 0) {
    return (
      text: '0/0 Episodes Available',
      color: AppTheme.textSecondary,
    );
  }

  // One release per episode, but Sonarr lists a season pack once per episode
  // and an episode can be re-grabbed, so count episodes rather than rows.
  final aired = <int>{}, unaired = <int>{};
  for (final item in queue) {
    final key = item.episodeId ?? -item.id;
    (item.episodeHasAired ? aired : unaired).add(key);
  }
  // An in-flight episode fills a hole in exactly one bucket; capping keeps a
  // quality upgrade (a queued episode that is already on disk) from inventing
  // an episode the season does not have.
  final gap = math.max(0, obtainable - files);
  final held = total - obtainable;
  final downloadingAired = math.min(aired.length, gap);
  final downloadingHeld = math.min(unaired.length, held);

  final missing = gap - downloadingAired;
  final downloading = downloadingAired + downloadingHeld;
  final remaining = held - downloadingHeld;
  final busy = downloading > 0;

  final parts = [
    if (missing > 0) '$missing missing',
    if (busy)
      queue.every(_isWaitingToImport)
          ? '$downloading waiting to import'
          : '$downloading downloading',
    if (remaining > 0) moreToCome ? '$remaining unaired' : '$remaining unmonitored',
  ];

  return (
    text: '$files/$total Episodes Available'
        '${parts.isEmpty ? '' : ' • ${parts.join(', ')}'}',
    color: files >= total
        ? AppTheme.available
        : missing > 0
            ? AppTheme.error
            : busy || moreToCome
                ? AppTheme.downloading
                : AppTheme.requested,
  );
}

/// True once a download has finished transferring and is waiting on — or stuck
/// at — the import step, the state the episode rows spell out as "Downloaded —
/// waiting to import". Calling that "downloading" would point at the wrong
/// thing when a whole season is parked in front of a broken import.
bool _isWaitingToImport(SonarrQueueItem item) =>
    item.trackedDownloadState == 'importPending' ||
    item.trackedDownloadState == 'importBlocked' ||
    item.trackedDownloadState == 'importing';
