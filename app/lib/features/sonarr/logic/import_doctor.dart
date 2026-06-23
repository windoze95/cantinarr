import '../data/sonarr_models.dart';

/// One-click remediation a diagnosis can suggest. Mirrors the action verbs in
/// the server-side classifier (server/internal/arr/doctor.go) so the app and
/// the MCP tools agree on fixes.
enum DoctorAction {
  process,
  manualImport,
  forceImport,
  remove,
  blocklistSearch,
  changeCategory,
  rescan,
}

enum DoctorSeverity { ok, info, warning, error }

/// The classifier's verdict for one queue item.
class SonarrDiagnosis {
  final DoctorSeverity severity;
  final String problem;
  final String transparency;
  final List<DoctorAction> actions;

  const SonarrDiagnosis({
    required this.severity,
    this.problem = '',
    this.transparency = '',
    this.actions = const [],
  });

  bool get isHealthy => severity == DoctorSeverity.ok;
}

class _Rule {
  final String prefix; // stable English fragment, lower-cased
  final String problem;
  final String transparency;
  final DoctorSeverity severity;
  final List<DoctorAction> actions;
  const _Rule(
      this.prefix, this.problem, this.transparency, this.severity, this.actions);
}

/// Verbatim-prefix catalog for statusMessages, first-match-wins. More specific /
/// more dangerous reasons come first. Kept in sync with doctor.go.
const List<_Rule> _messageRules = [
  _Rule(
    'caution: found',
    'Dangerous file in release',
    'This release contains an executable or otherwise dangerous file — possible malware. Do not import it.',
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
  _Rule(
    'individual episode mappings',
    'TheXEM mapping needs confirmation',
    "This show has individual episode mappings on TheXEM, so the download wasn't imported automatically. Confirm the episode and we'll force the import.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.forceImport],
  ),
  _Rule(
    'found archive file',
    'Release is an unextracted archive',
    'This release is packed in an archive (RAR) and must be unpacked before it can be imported.',
    DoctorSeverity.warning,
    [DoctorAction.changeCategory, DoctorAction.manualImport],
  ),
  _Rule(
    'no files found are eligible for import',
    'Nothing importable in the folder',
    'The download folder had nothing importable — likely all samples, an unextracted archive, or a path/permissions issue.',
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  ),
  _Rule(
    'unable to determine if file is a sample',
    'Could not verify sample',
    "The file's runtime couldn't be verified, so it might be a sample clip. Force the import if you know it's the full file.",
    DoctorSeverity.warning,
    [DoctorAction.forceImport, DoctorAction.blocklistSearch],
  ),
  _Rule(
    'sample',
    'File looks like a sample',
    'The file looks like a sample clip rather than the full release.',
    DoctorSeverity.warning,
    [DoctorAction.forceImport, DoctorAction.blocklistSearch],
  ),
  _Rule(
    'not an upgrade for existing',
    'Not an upgrade',
    "This release isn't better than the file you already have, so it wasn't imported.",
    DoctorSeverity.info,
    [DoctorAction.remove, DoctorAction.forceImport],
  ),
  _Rule(
    'already imported',
    'Already imported',
    "This exact download was already imported, so there's nothing to do but clear it.",
    DoctorSeverity.info,
    [DoctorAction.remove],
  ),
  _Rule(
    'not enough free space',
    'Not enough free space',
    'The library drive is below its free-space floor. Free up space, then rescan to retry.',
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
  _Rule(
    'permission',
    'Path or permissions error',
    'A permissions problem or wrong remote-path mapping blocked the import. Fix access to the path, then rescan to retry.',
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
  _Rule(
    'does not exist',
    'Path not accessible',
    "The download path doesn't exist or isn't accessible to Sonarr — usually a remote-path mapping issue. Fix the path, then rescan to retry.",
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
];

const List<_Rule> _errorRules = [
  _Rule(
    'stalled',
    'Download stalled',
    'This torrent has no seeders and will never finish on its own.',
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
  _Rule(
    'no connections',
    'Download stalled',
    'This torrent has no connections and will never finish on its own.',
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
  _Rule(
    'is reporting an error',
    'Download client error',
    "Your download client errored on this download. It won't recover on its own.",
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
];

SonarrDiagnosis? _matchRule(String line, List<_Rule> rules) {
  final lower = line.toLowerCase().trim();
  if (lower.isEmpty) return null;
  for (final r in rules) {
    if (lower.contains(r.prefix)) {
      return SonarrDiagnosis(
        severity: r.severity,
        problem: r.problem,
        transparency: r.transparency,
        actions: r.actions,
      );
    }
  }
  return null;
}

bool _looksHealthy(SonarrQueueItem item) {
  final tds = (item.trackedDownloadStatus ?? '').toLowerCase();
  if (tds.isNotEmpty && tds != 'ok') return false;
  if ((item.errorMessage ?? '').isNotEmpty) return false;
  for (final g in item.statusMessageGroups) {
    for (final line in g.messages) {
      if (line.trim().isNotEmpty) return false;
    }
  }
  return true;
}

/// Classifies a queue item with first-match-wins rules. Mirrors the Go
/// classifier's order: client/stalled error → import rejections → stuck
/// pending → failed → healthy → unknown-blocked fallback.
SonarrDiagnosis diagnoseSonarrQueueItem(SonarrQueueItem item) {
  final status = (item.trackedDownloadStatus ?? '').toLowerCase();
  final state = (item.trackedDownloadState ?? '').toLowerCase();

  // 1. Hard download-client / stalled errors.
  if (status == 'error') {
    final matched = _matchRule(item.errorMessage ?? '', _errorRules);
    if (matched != null) return matched;
    final msg = item.errorMessage ?? '';
    return SonarrDiagnosis(
      severity: DoctorSeverity.error,
      problem: 'Download error',
      transparency: msg.isNotEmpty
          ? 'This download errored ($msg) and won\'t recover on its own.'
          : 'This download errored and won\'t recover on its own.',
      actions: const [DoctorAction.blocklistSearch],
    );
  }

  // 2. Import rejections surfaced as statusMessages.
  for (final g in item.statusMessageGroups) {
    final candidates = <String>[
      if (g.title.isNotEmpty) g.title,
      ...g.messages,
    ];
    for (final line in candidates) {
      final matched = _matchRule(line, _messageRules);
      if (matched != null) return matched;
    }
  }

  // 3. Stuck waiting on the import pass.
  if (state == 'importpending') {
    return const SonarrDiagnosis(
      severity: DoctorSeverity.warning,
      problem: 'Waiting to import',
      transparency:
          "The download finished but hasn't been imported yet — Sonarr hasn't run its import pass. Process it now to import it.",
      actions: [DoctorAction.process, DoctorAction.manualImport],
    );
  }

  // 4. Failed download.
  if (state == 'failed' || state == 'failedpending') {
    return const SonarrDiagnosis(
      severity: DoctorSeverity.error,
      problem: 'Download failed',
      transparency:
          'This download failed. Remove and blocklist it so a fresh search grabs a different release.',
      actions: [DoctorAction.blocklistSearch],
    );
  }

  // 5. Healthy.
  if (_looksHealthy(item)) {
    return const SonarrDiagnosis(severity: DoctorSeverity.ok);
  }

  // 6. Unknown blocked state.
  return const SonarrDiagnosis(
    severity: DoctorSeverity.warning,
    problem: 'Import blocked',
    transparency:
        "Sonarr couldn't import this automatically. Review the candidate files and import manually, or remove and re-search.",
    actions: [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  );
}
