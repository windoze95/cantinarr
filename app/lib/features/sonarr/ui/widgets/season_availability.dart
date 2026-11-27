import 'package:cantinarr/l10n/l10n.dart';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';
import '../../logic/import_doctor.dart';

/// (text, colour) pair for the availability line on a season card.
typedef SeasonAvailability = ({String text, Color color});

/// The only space a count is allowed to keep from its own words. Every phrase
/// below is stitched with these, so a narrow card wraps between phrases and
/// never inside one: a lone "13" at the end of a line with "waiting to import"
/// beneath it reads like two separate facts.
const String _nbsp = ' ';

/// Builds "11/11 Episodes Available • 2 unaired" for one season — or, from the
/// series-level statistics, for the whole series.
///
/// The fraction is Sonarr's own: files on disk over `episodeCount`, the
/// episodes that are monitored and have aired plus anything already
/// downloaded. Unaired and unmonitored episodes stay out of both numbers — the
/// season is graded on what the admin asked for, the same denominator as the
/// library tile — but they are not allowed to vanish either: the suffix keeps
/// accounting for the whole season, from [queue] (the season's slice of
/// `queue/details`) and the statistics, so a caught-up season that is still
/// airing reads "… • 2 unaired" instead of a bare "11/11" one tap away from an
/// episode list with 13 rows, and a season nobody monitors reads
/// "0/0 … • 13 unmonitored" rather than looking empty by accident.
///
/// The suffix splits everything the fraction leaves out into buckets that,
/// together with the files on disk, add back up to the whole season, using the
/// episode list's own vocabulary: episodes that aired and are wanted but
/// absent are "missing", episodes still transferring are "downloading", ones
/// that have finished name their import phase, and what is left is "unaired" while the season still
/// has an air date pending ([moreToCome]) or "unmonitored" when it does not.
///
/// Colour follows the library tile's grammar: green only once every episode is
/// on disk, red for a gap nothing is working on, amber for a gap nobody
/// monitors, and the info tone for a season that is caught up or busy.
SeasonAvailability seasonAvailabilityLine(SonarrStatistics? stats,
    {required bool moreToCome,
    List<SonarrQueueItem> queue = const [],
    AppLocalizations? strings}) {
  final l10n = strings ?? lookupAppLocalizations(const Locale('en'));
  final files = stats?.episodeFileCount ?? 0;
  // Sonarr's episodeCount already counts every episode with a file; clamping
  // keeps a degenerate payload (a file count without an episodeCount) from
  // reading "8/0". totalEpisodeCount can likewise be absent on older payloads —
  // fall back rather than inventing a remainder the season does not have.
  final obtainable = math.max(stats?.episodeCount ?? 0, files);
  final total = math.max(stats?.totalEpisodeCount ?? 0, obtainable);
  if (total == 0) {
    return (
      text: _phrase(l10n.admin00EpisodesAvailable),
      color: AppTheme.textSecondary,
    );
  }

  // Sonarr lists a season pack once per episode and an episode can be
  // re-grabbed, so count episodes rather than rows.
  final inFlight = <int, _InFlight>{};
  for (final item in queue) {
    final key = item.episodeId;
    if (key == null) continue; // A queue row alone cannot identify an episode.
    final phase = _phaseOf(item);
    final seen = inFlight[key];
    if (seen == null || phase.index < seen.phase.index) {
      inFlight[key] = (aired: item.episodeHasAired, phase: phase);
    }
  }

  // Every in-flight episode fills exactly one hole. Capping by the hole keeps a
  // quality upgrade (a queued episode already on disk) from inventing an
  // episode the season does not have, and the import step is drawn from first
  // because that is the state an admin can act on.
  var missing = math.max(0, obtainable - files);
  var remaining = total - obtainable;
  final counts = {for (final phase in _Phase.values) phase: 0};
  for (final phase in [
    _Phase.blocked,
    _Phase.importing,
    _Phase.pending,
    _Phase.downloading
  ]) {
    for (final aired in [true, false]) {
      final queued = inFlight.values
          .where((f) => f.aired == aired && f.phase == phase)
          .length;
      final taken = math.min(queued, aired ? missing : remaining);
      if (aired) {
        missing -= taken;
      } else {
        remaining -= taken;
      }
      counts[phase] = counts[phase]! + taken;
    }
  }

  final downloading = counts[_Phase.downloading]!;
  final waiting = counts[_Phase.pending]!;
  final importing = counts[_Phase.importing]!;
  final blocked = counts[_Phase.blocked]!;
  final parts = [
    if (missing > 0) _phrase(l10n.adminMissing2('$missing')),
    if (downloading > 0) _phrase(l10n.adminDownloading2('$downloading')),
    if (waiting > 0) _phrase(l10n.adminWaitingToImport('$waiting')),
    if (importing > 0) _phrase(l10n.sonarrImportingCount(importing)),
    if (blocked > 0) _phrase(l10n.sonarrBlockedCount(blocked)),
    if (remaining > 0)
      _phrase(moreToCome
          ? l10n.adminUnaired2('$remaining')
          : l10n.adminUnmonitored3('$remaining')),
  ];

  return (
    text: '${_phrase(l10n.adminEpisodesAvailable(files, obtainable))}'
        '${parts.isEmpty ? '' : ' •$_nbsp${parts.join(', ')}'}',
    color: files >= total
        ? AppTheme.available
        : missing > 0
            ? AppTheme.error
            : blocked > 0
                ? AppTheme.requested
                : waiting + importing + downloading > 0 || moreToCome
                    ? AppTheme.downloading
                    : AppTheme.requested,
  );
}

/// Binds one phrase together so the line can only break between phrases.
String _phrase(String text) => text.replaceAll(' ', _nbsp);

typedef _InFlight = ({bool aired, _Phase phase});

/// Priority for competing rows on *one* episode: don't hide a blocker; an
/// active transfer or import outranks an ordinary pending row.
enum _Phase { blocked, importing, downloading, pending }

_Phase _phaseOf(SonarrQueueItem item) {
  if (sonarrImportBlocker(item) != null) return _Phase.blocked;
  return switch (item.trackedDownloadState) {
    'importing' => _Phase.importing,
    'importPending' => _Phase.pending,
    _ => _Phase.downloading,
  };
}
