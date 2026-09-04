import 'package:cantinarr/features/lidarr/data/lidarr_models.dart';
import 'package:cantinarr/features/lidarr/ui/widgets/lidarr_queue_item_card.dart';
import 'package:cantinarr/features/sonarr/logic/import_doctor.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

LidarrQueueItem _item({
  String status = 'ok',
  String state = 'downloading',
  String? error,
  List<LidarrStatusMessage> groups = const [],
  List<String> statusMessages = const [],
}) =>
    LidarrQueueItem(
      id: 1,
      title: 'Artist - Album (2024) FLAC',
      status: 'downloading',
      trackedDownloadStatus: status,
      trackedDownloadState: state,
      errorMessage: error,
      statusMessages: statusMessages,
      statusMessageGroups: groups,
    );

LidarrStatusMessage _msg(String text) =>
    LidarrStatusMessage(title: 'release', messages: [text]);

void main() {
  // diagnoseLidarrQueueItem bridges LidarrStatusMessage -> the shared neutral
  // engine; these confirm the bridge feeds the catalog correctly for music.
  group('diagnoseLidarrQueueItem', () {
    test('healthy download is OK with no actions', () {
      final d = diagnoseLidarrQueueItem(_item());
      expect(d.severity, DoctorSeverity.ok);
      expect(d.isHealthy, isTrue);
    });

    test('stalled torrent error → blocklist+search', () {
      final d = diagnoseLidarrQueueItem(_item(
        status: 'error',
        error: 'The download is stalled with no connections',
      ));
      expect(d.severity, DoctorSeverity.error);
      expect(d.actions, contains(DoctorAction.blocklistSearch));
    });

    test('sample rejection (via status message bridge) → force import', () {
      final d = diagnoseLidarrQueueItem(_item(
        status: 'warning',
        state: 'importBlocked',
        groups: [_msg('Sample')],
      ));
      expect(d.severity, DoctorSeverity.warning);
      expect(d.actions, contains(DoctorAction.forceImport));
    });

    test('stuck importPending with no messages → process', () {
      final d = diagnoseLidarrQueueItem(_item(state: 'importPending'));
      expect(d.problem, 'Waiting to import');
      expect(d.actions, contains(DoctorAction.process));
    });
  });

  group('queue card doctor affordance', () {
    Future<void> pump(WidgetTester tester, LidarrQueueItem item,
        {VoidCallback? onTap}) {
      return tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: LidarrQueueItemCard(item: item, onTap: onTap),
        ),
      ));
    }

    testWidgets('issues defer to the Import Doctor when a handler is set',
        (tester) async {
      var opened = 0;
      await pump(
        tester,
        _item(
          status: 'warning',
          state: 'importBlocked',
          statusMessages: const ['Sample file detected'],
        ),
        onTap: () => opened++,
      );

      expect(find.text('Sample file detected'), findsNothing);
      await tester.tap(find.text('1 message — tap to resolve'));
      expect(opened, 1);
    });

    testWidgets('without a handler the raw messages render inline',
        (tester) async {
      await pump(
        tester,
        _item(
          status: 'warning',
          state: 'importBlocked',
          statusMessages: const ['Sample file detected'],
        ),
      );

      expect(find.textContaining('Sample file detected'), findsOneWidget);
      expect(find.textContaining('tap to resolve'), findsNothing);
    });
  });

  group('LidarrManualImportCandidate', () {
    test('maps only when an album AND its tracks matched', () {
      final unmatched = LidarrManualImportCandidate.fromJson(const {
        'id': 1,
        'path': '/downloads/a.flac',
        'artist': {'id': 4},
      });
      expect(unmatched.isMapped, isFalse);

      final albumOnly = LidarrManualImportCandidate.fromJson(const {
        'id': 2,
        'path': '/downloads/b.flac',
        'artist': {'id': 4},
        'album': {'id': 9},
      });
      expect(albumOnly.isMapped, isFalse,
          reason: 'an album guess with no matched track cannot import');

      final matched = LidarrManualImportCandidate.fromJson(const {
        'id': 3,
        'path': '/downloads/c.flac',
        'artist': {'id': 4},
        'album': {'id': 9},
        'tracks': [
          {'id': 71},
          {'id': 72},
        ],
      });
      expect(matched.isMapped, isTrue);
      expect(matched.trackIds, [71, 72]);
    });

    test('toImportFile round-trips quality verbatim with the track ids', () {
      final candidate = LidarrManualImportCandidate.fromJson(const {
        'id': 3,
        'path': '/downloads/c.flac',
        'folderName': 'Album [FLAC]',
        'artist': {'id': 4},
        'album': {'id': 9},
        'albumReleaseId': 21,
        'tracks': [
          {'id': 71},
        ],
        'quality': {
          'quality': {'id': 6, 'name': 'FLAC'},
          'revision': {'version': 1},
        },
        'releaseGroup': 'GRP',
        'downloadId': 'NZB-R',
      });
      final file = candidate.toImportFile();
      expect(file['path'], '/downloads/c.flac');
      expect(file['artistId'], 4);
      expect(file['albumId'], 9);
      expect(file['albumReleaseId'], 21);
      expect(file['trackIds'], [71]);
      expect(file['releaseGroup'], 'GRP');
      expect(file['downloadId'], 'NZB-R');
      // Quality is round-tripped exactly as received, unknown fields intact.
      expect((file['quality'] as Map)['revision'], {'version': 1});
    });

    test('a permanent rejection is distinguished from a temporary one', () {
      final candidate = LidarrManualImportCandidate.fromJson(const {
        'id': 4,
        'path': '/downloads/d.flac',
        'rejections': [
          {'reason': 'Sample', 'type': 'permanent'},
          {'reason': 'Busy', 'type': 'temporary'},
        ],
      });
      expect(candidate.hasPermanentRejection, isTrue);
      expect(candidate.rejections, hasLength(2));
    });
  });
}
