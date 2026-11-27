import '../data/sonarr_models.dart';
import 'import_doctor.dart';

/// Both views retain the original queue rows: queue operations use the row ID,
/// while candidate lookup uses the client download ID.
class SeasonQueue {
  final Map<int, List<SonarrQueueItem>> byEpisode;
  final List<BlockedDownload> blockedDownloads;

  const SeasonQueue(
      {this.byEpisode = const {}, this.blockedDownloads = const []});

  factory SeasonQueue.forView(List<SonarrQueueItem> rows,
      List<SonarrEpisode> episodes, int seriesId, int? season) {
    final episodeSeasons = {for (final e in episodes) e.id: e.seasonNumber};
    final scoped = <SonarrQueueItem>[];
    final byEpisode = <int, List<SonarrQueueItem>>{};
    for (final item in rows) {
      if (item.seriesId != null && item.seriesId != seriesId) continue;
      final episodeSeason = episodeSeasons[item.episodeId];
      final rowSeason = item.seasonNumber ?? episodeSeason;
      if (season != null && rowSeason != season) continue;
      scoped.add(item);
      if (item.episodeId != null &&
          episodeSeason != null &&
          episodeSeason == rowSeason) {
        byEpisode.putIfAbsent(item.episodeId!, () => []).add(item);
      }
    }
    return SeasonQueue(
        byEpisode: byEpisode, blockedDownloads: groupBlockedDownloads(scoped));
  }
}

class BlockedDownload {
  final SonarrImportBlocker blocker;
  final members = <SonarrQueueItem>[];
  BlockedDownload._(this.blocker);

  SonarrQueueItem get target => members.first;
}

/// Prefer a member that has a reason. Break ties by queue and episode ID so
/// the Doctor target doesn't depend on the order of Sonarr's response.
List<BlockedDownload> groupBlockedDownloads(List<SonarrQueueItem> rows) {
  final blocked = [
    for (final row in rows)
      if (sonarrImportBlocker(row) case final reason?)
        (row: row, reason: reason),
  ]..sort((a, b) {
      final aHasReason =
          a.reason.problem != null || a.reason.reportedReason != null;
      final bHasReason =
          b.reason.problem != null || b.reason.reportedReason != null;
      if (aHasReason != bHasReason) return aHasReason ? -1 : 1;
      final byId = a.row.id.compareTo(b.row.id);
      return byId != 0
          ? byId
          : (a.row.episodeId ?? 0).compareTo(b.row.episodeId ?? 0);
    });
  final groups = <Object, BlockedDownload>{};
  for (final (:row, :reason) in blocked) {
    final id = row.downloadId?.trim();
    // A row without a client ID always gets its own group, even if the
    // queue ID or title is also absent or duplicated.
    final key = id == null || id.isEmpty ? Object() : id;
    groups.putIfAbsent(key, () => BlockedDownload._(reason)).members.add(row);
  }
  return groups.values.toList();
}
