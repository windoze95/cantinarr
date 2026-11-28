import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'import_doctor.dart';

/// Keep submitted work across sheet dismissals, scoped to the signed-in user,
/// server and Sonarr instance. A verification retry only reads this attempt.
final importRecoveryProvider =
    ChangeNotifierProvider.family<ImportRecovery, String>((ref, instance) {
  ref.watch(authProvider.select((s) {
    final auth = s.valueOrNull;
    return (auth?.connection?.serverUrl, auth?.user?.id, auth?.user?.role);
  }));
  return ImportRecovery();
});

bool offersImport(SonarrQueueItem row, {required bool force}) {
  if (row.id <= 0 ||
      row.trackedDownloadState == 'importing' ||
      row.trackedDownloadState == 'imported' ||
      row.statusMessages
          .any((m) => m.toLowerCase().contains('caution: found'))) {
    return false;
  }
  final diagnosis = diagnoseSonarrQueueItem(row);
  return diagnosis.actions.contains(
          force ? DoctorAction.forceImport : DoctorAction.manualImport) ||
      (!force &&
          diagnosis.isHealthy &&
          row.trackedDownloadState == 'importBlocked');
}

class ImportTarget {
  ImportTarget(this.rows);
  final List<SonarrQueueItem> rows;
  SonarrQueueItem get item => rows.first;
  String get downloadId => item.downloadId?.trim() ?? '';
  String? get mode => switch (item.protocol) {
        'torrent' => 'copy',
        'usenet' => 'move',
        _ => null,
      };

  // Video containers from Sonarr's MediaFileExtensions, excluding playlists,
  // disk images and generic binary files. Review never sends an archive.
  static const _video = {
    'webm',
    'm4v',
    '3gp',
    'nsv',
    'ty',
    'rm',
    'rmvb',
    'mov',
    'qt',
    'divx',
    'xvid',
    'bivx',
    'pva',
    'wmv',
    'asf',
    'ogm',
    'ogv',
    'm2v',
    'avi',
    'dvr-ms',
    'mpg',
    'mpeg',
    'mp4',
    'avc',
    'vp3',
    'svq3',
    'nuv',
    'viv',
    'dv',
    'fli',
    'flv',
    'vob',
    'mkv',
    'ts',
    'wtv',
    'm2ts',
  };

  bool _safePath(String source) {
    final path = source.replaceAll('\\', '/');
    final parts = path.split('/');
    final name = parts.last;
    if (!RegExp(r'^(?:/|[a-zA-Z]:/)').hasMatch(path) ||
        RegExp(r'[\x00-\x1f]').hasMatch(path) ||
        parts.contains('..') ||
        name.startsWith('._') ||
        !name.contains('.') ||
        !_video.contains(name.split('.').last.toLowerCase())) {
      return false;
    }
    return rows.every((row) {
      final root = row.outputPath
          ?.replaceAll('\\', '/')
          .replaceFirst(RegExp(r'/+$'), '');
      return root == null ||
          root.isEmpty ||
          path == root ||
          path.startsWith('$root/');
    });
  }

  bool accepts(SonarrManualImportCandidate file, {required bool force}) {
    if (downloadId.isEmpty ||
        mode == null ||
        item.seriesId == null ||
        item.seriesId! <= 0 ||
        file.seriesId != item.seriesId ||
        file.downloadId != downloadId ||
        file.id <= 0 ||
        !file.reviewedRejections ||
        file.existingFile ||
        file.size <= 0 ||
        file.quality == null ||
        file.episodeIds.isEmpty ||
        file.episodeIds.length != file.episodeLabels.length) {
      return false;
    }
    if (!_safePath(file.path)) return false;
    if (!rows.every((r) =>
        r.seriesId == item.seriesId &&
        r.downloadId?.trim() == downloadId &&
        r.downloadClient == item.downloadClient &&
        r.protocol == item.protocol &&
        offersImport(r, force: force))) {
      return false;
    }
    final expected = {for (final row in rows) row.episodeId};
    if (!file.episodeIds.every((id) => id > 0 && expected.contains(id)) ||
        file.episodeIds.toSet().length != file.episodeIds.length) {
      return false;
    }
    if (file.rejections.isEmpty) return true;
    // Only the Doctor's explicit mapping, sample and upgrade overrides. Force
    // cannot turn a path, archive, malware or unknown rejection into a video.
    return force &&
        file.rejections.every((r) =>
            r.isPermanent &&
            RegExp(
              r'^(sample\b|unable to determine if file is a sample|not (a custom format |an? )?upgrade\b|individual episode mappings\b)',
              caseSensitive: false,
            ).hasMatch(r.reason.trim()));
  }

  bool stillTracked(List<SonarrQueueItem> current, {required bool force}) {
    final matching = current.where((r) => r.downloadId?.trim() == downloadId);
    return matching.every((r) =>
            r.protocol == item.protocol &&
            r.seriesId == item.seriesId &&
            r.downloadClient == item.downloadClient &&
            !r.statusMessages
                .any((m) => m.toLowerCase().contains('caution: found'))) &&
        rows.every((row) => matching.any((r) =>
            r.id == row.id &&
            r.episodeId == row.episodeId &&
            r.outputPath == row.outputPath &&
            offersImport(r, force: force)));
  }
}

enum ImportPhase {
  preparing,
  notSubmitted,
  processing,
  verified,
  partial,
  failed,
  unverified
}

class ImportAttempt {
  ImportAttempt(this.target, this.files);
  final ImportTarget target;
  final List<SonarrManualImportCandidate> files;
  final beforeFiles = <int, int>{};
  final beforeHistory = <int>{};
  final verifiedEpisodes = <int>{};
  final retryableEpisodes = <int>{};
  ImportPhase phase = ImportPhase.preparing;
  bool submitted = false;
  bool checking = false;
  bool readFailed = false;
  bool timedOut = false;
  int? commandId;
  String? commandStatus;

  bool get commandFailed => const {'failed', 'aborted', 'cancelled', 'orphaned'}
      .contains(commandStatus);
  Iterable<int> get episodeIds => files.expand((f) => f.episodeIds);
  int verifiedCount(SonarrManualImportCandidate file) =>
      file.episodeIds.where(verifiedEpisodes.contains).length;

  bool blocks(ImportTarget other, SonarrManualImportCandidate file) {
    if (target.item.seriesId != other.item.seriesId ||
        (!submitted && phase != ImportPhase.preparing)) {
      return false;
    }
    final sameDownload = target.downloadId == other.downloadId;
    for (final id in episodeIds) {
      if (retryableEpisodes.contains(id)) continue;
      if (verifiedEpisodes.contains(id)) {
        if (sameDownload && file.episodeIds.contains(id)) return true;
      } else if (sameDownload || file.episodeIds.contains(id)) {
        return true;
      }
    }
    return false;
  }

  bool stillEligible(SonarrManualImportCandidate reviewed,
      List<SonarrManualImportCandidate> candidates, bool force) {
    final matches = candidates.where((c) => c.path == reviewed.path);
    if (matches.length != 1) return false;
    final fresh = matches.single;
    return target.accepts(fresh, force: force) &&
        fresh.size == reviewed.size &&
        jsonEncode(fresh.toImportFile()) ==
            jsonEncode(reviewed.toImportFile()) &&
        listEquals(fresh.rejections.map((r) => (r.reason, r.type)).toList(),
            reviewed.rejections.map((r) => (r.reason, r.type)).toList());
  }

  bool matchesFile(SonarrManualImportCandidate file, SonarrEpisode episode,
      SonarrHistoryRecord row) {
    final result = episode.episodeFile;
    if (!episode.hasFile ||
        result == null ||
        result.id <= 0 ||
        result.id != episode.episodeFileId ||
        episode.seriesId != target.item.seriesId ||
        row.id <= 0 ||
        row.eventType != SonarrHistoryRecord.importedEventType ||
        row.seriesId != episode.seriesId ||
        row.episodeId != episode.id ||
        row.downloadId != target.downloadId) {
      return false;
    }
    final data =
        row.data.map((key, value) => MapEntry(key.toLowerCase(), value));
    if (data['droppedpath'] != file.path) return false;
    final id = data['fileid'];
    final path = data['importedpath'];
    return (id != null || (path != null && path.isNotEmpty)) &&
        (id == null || int.tryParse(id) == result.id) &&
        (path == null || path == result.path);
  }
}

class ImportRecovery extends ChangeNotifier {
  final attempts = <ImportAttempt>[];
  bool _active = true;

  @override
  void dispose() {
    _active = false;
    super.dispose();
  }

  void _changed() {
    if (_active) notifyListeners();
  }

  bool conflicts(ImportTarget target, SonarrManualImportCandidate file) =>
      attempts.any((a) => a.blocks(target, file));

  /// Reserve the entire selection synchronously, before the first read. Read
  /// back the reviewed mapping and eligibility before each download's POST.
  Future<List<ImportAttempt>> submit(SonarrApiService service,
      Map<ImportTarget, List<SonarrManualImportCandidate>> selection,
      {required bool force, required bool Function() authorized}) async {
    final batch = <ImportAttempt>[];
    final episodes = <int>{};
    final paths = <String>{};
    final downloads = <String>{};
    for (final entry in selection.entries) {
      if (entry.value.isEmpty) continue;
      if (!downloads.add(entry.key.downloadId)) return [];
      for (final file in entry.value) {
        if (!entry.key.accepts(file, force: force) ||
            conflicts(entry.key, file) ||
            !paths.add(file.path) ||
            !file.episodeIds.every(episodes.add)) {
          return [];
        }
      }
      batch.add(ImportAttempt(entry.key, List.unmodifiable(entry.value)));
    }
    if (!_active || !authorized() || (force && paths.length != 1)) return [];
    attempts.addAll(batch);
    _changed();
    for (final a in batch) {
      final target = a.target;
      try {
        if (!_active || !authorized()) {
          a.phase = ImportPhase.notSubmitted;
          continue;
        }
        // A failed command may have landed files since the last review. Check
        // its original baseline again before retrying any affected episode.
        final previous = attempts
            .where((other) =>
                other != a &&
                other.target.item.seriesId == target.item.seriesId &&
                other.retryableEpisodes.any(a.episodeIds.contains))
            .toList();
        for (final earlier in previous) {
          await verify(service, earlier);
        }
        if (previous.any(
            (earlier) => a.files.any((file) => earlier.blocks(target, file)))) {
          a.phase = ImportPhase.notSubmitted;
          continue;
        }
        final series = target.item.seriesId!;
        final (queue, candidates, history, current) = await (
          service.getSeriesQueue(series),
          service.getManualImportCandidates(target.downloadId),
          service.getDownloadImports(target.downloadId),
          service.getImportEpisodes(series, a.episodeIds.toSet()),
        ).wait;
        final ok = target.stillTracked(queue, force: force) &&
            a.files.every((f) =>
                a.stillEligible(f, candidates, force) &&
                listEquals(f.episodeLabels, [
                  for (final id in f.episodeIds) current[id]!.seasonEpisodeLabel
                ]) &&
                f.episodeIds.every((id) => !history
                    .any((row) => a.matchesFile(f, current[id]!, row))));
        if (!ok || !_active || !authorized()) {
          a.phase = ImportPhase.notSubmitted;
          continue;
        }
        a.beforeFiles.addAll(
            {for (final id in a.episodeIds) id: current[id]!.episodeFileId});
        a.beforeHistory.addAll(history.map((h) => h.id));
        a.submitted = true; // A lost response does not undo the POST.
        a.phase = ImportPhase.processing;
        a.commandId = await service.executeManualImport(
            a.files.map((f) => f.toImportFile()).toList(),
            importMode: target.mode!);
      } catch (_) {
        a.phase =
            a.submitted ? ImportPhase.unverified : ImportPhase.notSubmitted;
      } finally {
        _changed();
      }
      if (a.submitted && _active && authorized()) await verify(service, a);
    }
    return batch;
  }

  Future<void> verify(SonarrApiService service, ImportAttempt a) async {
    if (!_active || !a.submitted || a.checking) return;
    a.checking = true;
    a.readFailed = false;
    a.retryableEpisodes.clear();
    _changed();
    try {
      final commandId = a.commandId;
      final (status, history, current) = await (
        commandId == null
            ? Future<String?>.value()
            : service.getImportCommandStatus(commandId),
        service.getDownloadImports(a.target.downloadId),
        service.getImportEpisodes(
            a.target.item.seriesId!, a.episodeIds.toSet()),
      ).wait;
      a.commandStatus = status;
      for (final f in a.files) {
        for (final id in f.episodeIds) {
          final episode = current[id]!;
          final events = history.where((r) =>
              !a.beforeHistory.contains(r.id) &&
              r.episodeId == id &&
              r.downloadId == a.target.downloadId &&
              r.eventType == SonarrHistoryRecord.importedEventType);
          if (a.beforeFiles[id] != episode.episodeFileId &&
              events.any((row) => a.matchesFile(f, episode, row))) {
            a.verifiedEpisodes.add(id);
          } else if (a.commandFailed &&
              events.isEmpty &&
              a.beforeFiles[id] == episode.episodeFileId &&
              !a.verifiedEpisodes.contains(id)) {
            a.retryableEpisodes.add(id);
          }
        }
      }
      if (a.episodeIds.every(a.verifiedEpisodes.contains)) {
        a.phase = ImportPhase.verified;
      } else if (a.verifiedEpisodes.isNotEmpty) {
        a.phase = ImportPhase.partial;
      } else if (a.commandFailed) {
        a.phase = ImportPhase.failed;
      } else if (status == 'queued' || status == 'started') {
        a.phase = ImportPhase.processing;
      } else {
        a.phase = ImportPhase.unverified;
      }
    } catch (_) {
      a.readFailed = true;
      a.retryableEpisodes.clear();
      a.phase = a.verifiedEpisodes.isEmpty
          ? ImportPhase.unverified
          : ImportPhase.partial;
    } finally {
      a.checking = false;
      _changed();
    }
  }
}
