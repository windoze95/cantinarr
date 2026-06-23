import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/import_doctor.dart';
import 'package:flutter_test/flutter_test.dart';

SonarrQueueItem _item({
  String status = 'ok',
  String state = 'downloading',
  String? error,
  List<SonarrStatusMessage> messages = const [],
}) =>
    SonarrQueueItem(
      id: 1,
      title: 'release',
      status: 'downloading',
      trackedDownloadStatus: status,
      trackedDownloadState: state,
      errorMessage: error,
      statusMessageGroups: messages,
    );

SonarrStatusMessage _msg(String text) =>
    SonarrStatusMessage(title: 'release', messages: [text]);

void main() {
  test('healthy download is OK with no actions', () {
    final d = diagnoseSonarrQueueItem(_item());
    expect(d.severity, DoctorSeverity.ok);
    expect(d.isHealthy, isTrue);
  });

  test('TheXEM unconfirmed mapping → manual + force import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('This show has individual episode mappings on TheXEM but the '
            'mapping for this episode has not been confirmed yet by their '
            'administrators. TheXEM needs manual input.'),
      ],
    ));
    expect(d.severity, DoctorSeverity.warning);
    expect(d.problem, contains('TheXEM'));
    expect(d.actions, containsAll([
      DoctorAction.manualImport,
      DoctorAction.forceImport,
    ]));
  });

  test('no eligible files → manual import / blocklist+search', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('No files found are eligible for import in /downloads/x')],
    ));
    expect(d.actions, contains(DoctorAction.manualImport));
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('unextracted archive → change category', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Found archive file, might need to be extracted')],
    ));
    expect(d.actions.first, DoctorAction.changeCategory);
  });

  test('stuck importPending with no messages → process', () {
    final d = diagnoseSonarrQueueItem(_item(state: 'importPending'));
    expect(d.problem, 'Waiting to import');
    expect(d.actions.first, DoctorAction.process);
  });

  test('stalled torrent error → blocklist + search', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'error',
      state: 'downloading',
      error: 'The download is stalled with no connections',
    ));
    expect(d.severity, DoctorSeverity.error);
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('errorMessage is classified even when status is still ok', () {
    // Real Sonarr 4.0.16: a qBittorrent magnet error surfaced on an item whose
    // trackedDownloadStatus was "ok", not "error". It must not fall through to
    // the generic "Import blocked" fallback.
    final d = diagnoseSonarrQueueItem(_item(
      status: 'ok',
      state: 'downloading',
      error: 'qBittorrent cannot resolve magnet link with DHT disabled',
    ));
    expect(d.severity, DoctorSeverity.error);
    expect(d.problem, "Download client can't fetch the magnet");
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('not-an-upgrade is info with remove option', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Not an upgrade for existing episode file(s). Existing: '
          'WEBDL-1080p. New: HDTV-720p.')],
    ));
    expect(d.severity, DoctorSeverity.info);
    expect(d.actions, contains(DoctorAction.remove));
  });

  test('unknown blocked state falls back to manual import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Some brand new reason Sonarr just invented')],
    ));
    expect(d.severity, DoctorSeverity.warning);
    expect(d.problem, 'Import blocked');
    expect(d.actions, contains(DoctorAction.manualImport));
  });

  // --- Import Doctor v2: deepened catalog (verified Servarr strings) ---

  test('not a Custom Format upgrade is info with remove', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('Not a Custom Format upgrade for existing episode file(s). '
            'New: [WEB] (0) do not improve on Existing: [WEB] (5)'),
      ],
    ));
    expect(d.severity, DoctorSeverity.info);
    expect(d.problem, 'Not a Custom Format upgrade');
    expect(d.actions, contains(DoctorAction.remove));
  });

  test('unable to parse file → manual import / blocklist', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Unable to parse file')],
    ));
    expect(d.problem, "Couldn't parse the file");
    expect(d.actions, contains(DoctorAction.manualImport));
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('invalid video file (AppleDouble) → manual import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg("Invalid video file, filename starts with '._'")],
    ));
    expect(d.problem, 'Invalid video file');
    expect(d.actions.first, DoctorAction.manualImport);
  });

  test('unsupported extension beats invalid-video rule', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg("Invalid video file, unsupported extension: '.mkv.exe'")],
    ));
    expect(d.problem, 'Unsupported file type');
    expect(d.actions.first, DoctorAction.blocklistSearch);
  });

  test('one or more episodes not imported → manual import / blocklist', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('One or more episodes expected in this release were not '
            'imported or missing from the release'),
      ],
    ));
    expect(d.problem, "Release contents don't match");
    expect(d.actions, contains(DoctorAction.manualImport));
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('episode not found in the grabbed release → manual import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Episode 5 was not found in the grabbed release')],
    ));
    expect(d.problem, "Release contents don't match");
    expect(d.actions, contains(DoctorAction.manualImport));
  });

  test('invalid season or episode → manual import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Invalid season or episode')],
    ));
    expect(d.problem, 'Episode mapping problem');
    expect(d.actions, contains(DoctorAction.manualImport));
  });

  test('episode file already imported is covered by already-imported rule', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Episode file already imported at 1/2/2024 3:04 PM')],
    ));
    expect(d.problem, 'Already imported');
    expect(d.actions, contains(DoctorAction.remove));
  });

  test('matched to series by ID is a warning needing manual import', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('Found matching series via grab history, but release was matched '
            'to series by ID. Automatic import is not possible. See the FAQ '
            'for details.'),
      ],
    ));
    expect(d.severity, DoctorSeverity.warning);
    expect(d.problem, 'Matched by ID — needs manual import');
    expect(d.actions, contains(DoctorAction.manualImport));
  });

  test('unmonitored series is info with no fix', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Series is not monitored')],
    ));
    expect(d.severity, DoctorSeverity.info);
    expect(d.problem, 'Unmonitored');
    expect(d.actions, isEmpty);
  });

  test('remote path mapping beats generic path rules', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('[/data/incomplete/Some.Release] is not a valid local path. '
            'You may need a Remote Path Mapping.'),
      ],
    ));
    expect(d.severity, DoctorSeverity.error);
    expect(d.problem, 'Remote path mapping');
    expect(d.actions, contains(DoctorAction.rescan));
  });

  test('download client unreachable is a config error with no per-item fix', () {
    final d = diagnoseSonarrQueueItem(_item(
      status: 'error',
      state: 'downloading',
      error: 'Unable to communicate with qBittorrent.',
    ));
    expect(d.severity, DoctorSeverity.error);
    expect(d.problem, 'Download client unreachable');
    expect(d.actions, isEmpty);
  });
}
