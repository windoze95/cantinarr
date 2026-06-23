import '../../radarr/data/radarr_models.dart';
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

/// The classifier's verdict for one queue item. Service-neutral: the same rule
/// engine drives both Sonarr and Radarr (movies are single items, so the
/// signals are identical — only the remediation wiring differs).
class QueueDiagnosis {
  final DoctorSeverity severity;
  final String problem;
  final String transparency;
  final List<DoctorAction> actions;

  const QueueDiagnosis({
    required this.severity,
    this.problem = '',
    this.transparency = '',
    this.actions = const [],
  });

  bool get isHealthy => severity == DoctorSeverity.ok;
}

/// Back-compat alias for callers that referenced the old Sonarr-specific name.
typedef SonarrDiagnosis = QueueDiagnosis;

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
  // "Invalid video file, unsupported extension: '{extension}'" — more specific
  // than the filename-prefix invalid-video rule below, so it comes first (both
  // share the "invalid video file" prefix).
  _Rule(
    'invalid video file, unsupported extension',
    'Unsupported file type',
    "The file has an extension the service doesn't import as video. It's likely the wrong file (or needs unpacking) — remove and re-search for a proper release.",
    DoctorSeverity.warning,
    [DoctorAction.blocklistSearch, DoctorAction.manualImport],
  ),
  // "Invalid video file, filename starts with '._'" (macOS AppleDouble).
  _Rule(
    'invalid video file',
    'Invalid video file',
    'The service flagged this as not a valid video file (for example a macOS resource-fork "._" file). Inspect the candidates and import the real file manually, or remove and re-search.',
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  ),
  // "Unable to parse file" (import-time parse failure).
  _Rule(
    'unable to parse file',
    "Couldn't parse the file",
    "The service couldn't parse the file name to figure out what it is, so it wasn't imported. Map it yourself with a manual import, or remove and re-search for a cleaner release.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  ),
  // Sonarr: "One or more episodes expected in this release were not imported or
  // missing from the release". Radarr's analog says "movies". Verified against a
  // real Sonarr 4.0.16 instance; the "expected in this release were not
  // imported" substring matches both services.
  _Rule(
    'expected in this release were not imported',
    'Release contents don\'t match',
    "Sonarr/Radarr expected files this release didn't contain, so it wasn't fully imported. Review the candidate files and import what's there manually, or remove and re-search for a complete release.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  ),
  // Co-occurs with the above on a real Sonarr instance: a specific episode
  // "was not found in the grabbed release". Same fix.
  _Rule(
    'was not found in the grabbed release',
    'Release contents don\'t match',
    "An episode this grab was supposed to include wasn't in the release, so it wasn't fully imported. Review the candidate files and import what's there manually, or remove and re-search for a complete release.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  ),
  // "Invalid season or episode" — Sonarr couldn't map the file to a
  // season/episode. Map it yourself via a manual import.
  _Rule(
    'invalid season or episode',
    'Episode mapping problem',
    "The service couldn't work out which season/episode this file is, so it wasn't imported. Map it yourself with a manual import.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport],
  ),
  _Rule(
    'not an upgrade for existing',
    'Not an upgrade',
    "This release isn't better than the file you already have, so it wasn't imported.",
    DoctorSeverity.info,
    [DoctorAction.remove, DoctorAction.forceImport],
  ),
  // "Not a Custom Format upgrade for existing {episode,movie} file(s)..." — a
  // distinct string from the plain "not an upgrade" rule above.
  _Rule(
    'not a custom format upgrade for existing',
    'Not a Custom Format upgrade',
    "This release doesn't improve on your existing file's Custom Format score, so it wasn't imported. Clear it, or force the import if you want it anyway.",
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
  // Sonarr: "...release was matched to series by ID. Automatic import is not
  // possible...". Radarr's analog says "matched to movie by ID". Verified
  // against a real Sonarr 4.0.16 instance: automatic import is genuinely
  // blocked, so this is a warning that needs a manual import.
  _Rule(
    'matched to series by id',
    'Matched by ID — needs manual import',
    "The release was matched to the series by its download-client ID rather than by its name, so the service won't import it automatically. Import it manually.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport],
  ),
  _Rule(
    'matched to movie by id',
    'Matched by ID — needs manual import',
    "The release was matched to the movie by its download-client ID rather than by its name, so the service won't import it automatically. Import it manually.",
    DoctorSeverity.warning,
    [DoctorAction.manualImport],
  ),
  // Grab/search-side rejections ("Series/Episode/Movie is not monitored").
  // Surfaced for transparency; nothing to fix on the item itself.
  _Rule(
    'is not monitored',
    'Unmonitored',
    "This item is unmonitored, so the service won't grab or import it. Monitor it first if you want it.",
    DoctorSeverity.info,
    [],
  ),
  _Rule(
    'not enough free space',
    'Not enough free space',
    'The library drive is below its free-space floor. Free up space, then rescan to retry.',
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
  // "[{path}] is not a valid local path. You may need a Remote Path Mapping..."
  // — the download client reported a path the service can't reach. Confirm with
  // get_arr_health (remote path mapping), then rescan.
  _Rule(
    'is not a valid local path. you may need a remote path mapping',
    'Remote path mapping',
    "The download client reported a path the service can't reach on disk — a remote-path-mapping problem. Run get_arr_health to confirm the mapping, fix it, then rescan to retry.",
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
  _Rule(
    'permission',
    'Path or permissions error',
    'A permissions problem or wrong remote-path mapping blocked the import. Run get_arr_health to confirm the config, fix access to the path, then rescan to retry.',
    DoctorSeverity.error,
    [DoctorAction.rescan],
  ),
  _Rule(
    'does not exist',
    'Path not accessible',
    "The download path doesn't exist or isn't accessible to the service — usually a remote-path mapping issue. Run get_arr_health to confirm, fix the path, then rescan to retry.",
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
  // "qBittorrent cannot resolve magnet link with DHT disabled" — the torrent
  // client can't fetch the magnet's metadata, so this download will never
  // start. Blocklist and re-search; a usenet or non-magnet release sidesteps it.
  _Rule(
    'cannot resolve magnet',
    "Download client can't fetch the magnet",
    "Your torrent client can't resolve this magnet link (often DHT is disabled), so the download will never start. Remove and blocklist it, then re-search — a usenet or non-magnet release avoids this. Run get_arr_health to check the client.",
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
  // "Unable to communicate with {downloadClient}..." — the service can't reach
  // the download client at all. A config/connectivity problem, not the
  // release's fault, so there's nothing to fix per item; get_arr_health
  // surfaces the root cause.
  _Rule(
    'unable to communicate with',
    'Download client unreachable',
    "The service can't reach your download client, so nothing in the queue can progress. Run get_arr_health to confirm, then fix the client connection — this isn't a problem with the release itself.",
    DoctorSeverity.error,
    [],
  ),
  _Rule(
    'is reporting an error',
    'Download client error',
    "Your download client errored on this download. It won't recover on its own.",
    DoctorSeverity.error,
    [DoctorAction.blocklistSearch],
  ),
];

QueueDiagnosis? _matchRule(String line, List<_Rule> rules) {
  final lower = line.toLowerCase().trim();
  if (lower.isEmpty) return null;
  for (final r in rules) {
    if (lower.contains(r.prefix)) {
      return QueueDiagnosis(
        severity: r.severity,
        problem: r.problem,
        transparency: r.transparency,
        actions: r.actions,
      );
    }
  }
  return null;
}

bool _looksHealthy({
  required String? trackedDownloadStatus,
  required String? errorMessage,
  required List<SonarrStatusMessage> statusMessageGroups,
}) {
  final tds = (trackedDownloadStatus ?? '').toLowerCase();
  if (tds.isNotEmpty && tds != 'ok') return false;
  if ((errorMessage ?? '').isNotEmpty) return false;
  for (final g in statusMessageGroups) {
    for (final line in g.messages) {
      if (line.trim().isNotEmpty) return false;
    }
  }
  return true;
}

/// Service-neutral rule engine. Classifies a queue item from its raw signals
/// with first-match-wins rules. Mirrors the Go classifier's order:
/// error-status → recognized errorMessage (even when status is ok) → import
/// rejections → stuck pending → failed → healthy → unknown-blocked fallback.
/// Both [diagnoseSonarrQueueItem] and [diagnoseRadarrQueueItem] funnel through
/// here so the catalog lives once.
QueueDiagnosis diagnoseQueueSignal({
  String? trackedDownloadStatus,
  String? trackedDownloadState,
  String? errorMessage,
  required List<SonarrStatusMessage> statusMessageGroups,
}) {
  final status = (trackedDownloadStatus ?? '').toLowerCase();
  final state = (trackedDownloadState ?? '').toLowerCase();

  // 1. Hard download-client / stalled errors.
  if (status == 'error') {
    final matched = _matchRule(errorMessage ?? '', _errorRules);
    if (matched != null) return matched;
    final msg = errorMessage ?? '';
    return QueueDiagnosis(
      severity: DoctorSeverity.error,
      problem: 'Download error',
      transparency: msg.isNotEmpty
          ? 'This download errored ($msg) and won\'t recover on its own.'
          : 'This download errored and won\'t recover on its own.',
      actions: const [DoctorAction.blocklistSearch],
    );
  }

  // 2. A recognized errorMessage on an otherwise-ok item (the status hasn't
  // flipped to 'error' yet, e.g. a qBittorrent magnet error on a still-
  // downloading item). Checked before the healthy short-circuit so these are
  // not misclassified to the generic 'Import blocked' fallback.
  if ((errorMessage ?? '').isNotEmpty) {
    final matched = _matchRule(errorMessage!, _errorRules);
    if (matched != null) return matched;
  }

  // 3. Import rejections surfaced as statusMessages.
  for (final g in statusMessageGroups) {
    final candidates = <String>[
      if (g.title.isNotEmpty) g.title,
      ...g.messages,
    ];
    for (final line in candidates) {
      final matched = _matchRule(line, _messageRules);
      if (matched != null) return matched;
    }
  }

  // 4. Stuck waiting on the import pass.
  if (state == 'importpending') {
    return const QueueDiagnosis(
      severity: DoctorSeverity.warning,
      problem: 'Waiting to import',
      transparency:
          "The download finished but hasn't been imported yet — the import pass hasn't run. Process it now to import it.",
      actions: [DoctorAction.process, DoctorAction.manualImport],
    );
  }

  // 5. Failed download.
  if (state == 'failed' || state == 'failedpending') {
    return const QueueDiagnosis(
      severity: DoctorSeverity.error,
      problem: 'Download failed',
      transparency:
          'This download failed. Remove and blocklist it so a fresh search grabs a different release.',
      actions: [DoctorAction.blocklistSearch],
    );
  }

  // 6. Healthy.
  if (_looksHealthy(
    trackedDownloadStatus: trackedDownloadStatus,
    errorMessage: errorMessage,
    statusMessageGroups: statusMessageGroups,
  )) {
    return const QueueDiagnosis(severity: DoctorSeverity.ok);
  }

  // 7. Unknown blocked state.
  return const QueueDiagnosis(
    severity: DoctorSeverity.warning,
    problem: 'Import blocked',
    transparency:
        "This download couldn't be imported automatically. Review the candidate files and import manually, or remove and re-search.",
    actions: [DoctorAction.manualImport, DoctorAction.blocklistSearch],
  );
}

/// Classifies a Sonarr queue item (thin wrapper over [diagnoseQueueSignal]).
QueueDiagnosis diagnoseSonarrQueueItem(SonarrQueueItem item) =>
    diagnoseQueueSignal(
      trackedDownloadStatus: item.trackedDownloadStatus,
      trackedDownloadState: item.trackedDownloadState,
      errorMessage: item.errorMessage,
      statusMessageGroups: item.statusMessageGroups,
    );

/// Classifies a Radarr queue item (thin wrapper over [diagnoseQueueSignal]).
QueueDiagnosis diagnoseRadarrQueueItem(RadarrQueueItem item) =>
    diagnoseQueueSignal(
      trackedDownloadStatus: item.trackedDownloadStatus,
      trackedDownloadState: item.trackedDownloadState,
      errorMessage: item.errorMessage,
      statusMessageGroups: item.statusMessageGroups,
    );
