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
}
