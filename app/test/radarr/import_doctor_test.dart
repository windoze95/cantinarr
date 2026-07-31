import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/import_doctor.dart';
import 'package:flutter_test/flutter_test.dart';

RadarrQueueItem _item({
  String status = 'ok',
  String state = 'downloading',
  String? error,
  List<SonarrStatusMessage> messages = const [],
}) =>
    RadarrQueueItem(
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
  test('healthy movie download is OK with no actions', () {
    final d = diagnoseRadarrQueueItem(_item());
    expect(d.severity, DoctorSeverity.ok);
    expect(d.isHealthy, isTrue);
  });

  test('stuck importPending with no messages → process', () {
    final d = diagnoseRadarrQueueItem(_item(state: 'importPending'));
    expect(d.problem, 'Waiting to import');
    expect(d.actions.first, DoctorAction.process);
  });

  test('unextracted archive → change category', () {
    final d = diagnoseRadarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Found archive file, might need to be extracted')],
    ));
    expect(d.actions.first, DoctorAction.changeCategory);
  });

  test('no eligible files → manual import / blocklist+search', () {
    final d = diagnoseRadarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('No files found are eligible for import in /downloads/x')],
    ));
    expect(d.actions, contains(DoctorAction.manualImport));
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('stalled torrent error → blocklist + search', () {
    final d = diagnoseRadarrQueueItem(_item(
      status: 'error',
      state: 'downloading',
      error: 'The download is stalled with no connections',
    ));
    expect(d.severity, DoctorSeverity.error);
    expect(d.actions, contains(DoctorAction.blocklistSearch));
  });

  test('not-an-upgrade is info with remove option', () {
    final d = diagnoseRadarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [
        _msg('Not an upgrade for existing movie file(s). Existing: '
            'WEBDL-1080p. New: HDTV-720p.')
      ],
    ));
    expect(d.severity, DoctorSeverity.info);
    expect(d.actions, contains(DoctorAction.remove));
  });

  test('unknown blocked state falls back to manual import', () {
    final d = diagnoseRadarrQueueItem(_item(
      status: 'warning',
      state: 'importBlocked',
      messages: [_msg('Some brand new reason Radarr just invented')],
    ));
    expect(d.severity, DoctorSeverity.warning);
    expect(d.problem, 'Import blocked');
    expect(d.actions, contains(DoctorAction.manualImport));
  });
}
