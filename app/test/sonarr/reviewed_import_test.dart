import 'package:cantinarr/features/sonarr/data/sonarr_api_service.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/reviewed_import.dart';
import 'package:flutter_test/flutter_test.dart';

SonarrQueueItem _row(
        {String protocol = 'torrent',
        String? download = 'pack',
        int episode = 1,
        String state = 'importBlocked'}) =>
    SonarrQueueItem(
        id: 10 + episode,
        title: 'Fixture',
        trackedDownloadState: state,
        downloadId: download,
        seriesId: 7,
        episodeId: episode,
        protocol: protocol);

Map<String, dynamic> _candidate() => {
      'id': 18,
      'path': '/incoming/E01.mkv',
      'downloadId': 'pack',
      'size': 100,
      'series': {'id': 7},
      'quality': {
        'quality': {'id': 1}
      },
      'episodes': [
        {'id': 1, 'seasonNumber': 1, 'episodeNumber': 1}
      ],
      'rejections': [],
    };

class _Service implements SonarrApiService {
  final rows = [_row()];
  final candidate = _candidate();
  final library = <int, SonarrEpisode>{
    1: const SonarrEpisode(
        id: 1, seriesId: 7, seasonNumber: 1, episodeNumber: 1)
  };
  final history = <SonarrHistoryRecord>[];
  var command = 'failed';
  var posts = 0;
  bool unavailable = false;
  @override
  Future<List<SonarrQueueItem>> getSeriesQueue(int seriesId) async => rows;
  @override
  Future<List<SonarrManualImportCandidate>> getManualImportCandidates(
          String downloadId) async =>
      [SonarrManualImportCandidate.fromJson(candidate)];
  @override
  Future<List<SonarrHistoryRecord>> getDownloadImports(
      String downloadId) async {
    if (unavailable) throw StateError('Fixture read failed');
    return history;
  }

  @override
  Future<Map<int, SonarrEpisode>> getImportEpisodes(
          int seriesId, Set<int> episodeIds) async =>
      library;
  @override
  Future<int?> executeManualImport(List<Map<String, dynamic>> files,
          {required String importMode}) async =>
      ++posts;
  @override
  Future<String?> getImportCommandStatus(int id) async => command;
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  final target = ImportTarget([_row()]);
  final video = SonarrManualImportCandidate.fromJson(_candidate());
  final selection = {
    target: [video]
  };

  test(
      'ordinary review excludes unknown, rejected, unsafe and incorrectly mapped candidates',
      () {
    expect(target.accepts(video, force: false), isTrue);
    for (final change in <Map<String, dynamic>>[
      {'downloadId': null},
      {'downloadId': 'other'},
      {
        'series': {'id': 8}
      },
      {'episodes': []},
      {
        'episodes': [
          {'id': 1},
          <String, dynamic>{}
        ]
      },
      {
        'episodes': [
          {'id': 1, 'seriesId': 8}
        ]
      },
      {
        'episodes': [
          {'id': 2}
        ]
      },
      {'episodeFileId': 10},
      {'path': '/incoming/release.rar'},
      {'path': '/incoming/._E01.mkv'},
      {'path': '/incoming/../library/E01.mkv'},
      {'path': ''},
      {'path': '/incoming/player.exe'},
      {'size': 0},
      {'quality': null},
      {'id': 0},
      {'rejections': null},
      {
        'rejections': [
          {'type': 'temporary', 'reason': 'File locked'}
        ]
      },
      {
        'rejections': [
          {'type': 'permanent', 'reason': 'Not an upgrade'}
        ]
      },
    ]) {
      expect(
          target.accepts(
              SonarrManualImportCandidate.fromJson(
                  _candidate()..addAll(change)),
              force: false),
          isFalse,
          reason: '$change');
    }
    for (final rows in [
      [_row(download: ' ')],
      [_row(protocol: 'unknown')],
      [_row(state: 'importing')],
      [_row(), _row(episode: 2, protocol: 'usenet')]
    ]) {
      expect(ImportTarget(rows).accepts(video, force: false), isFalse);
    }
  });

  test(
      'selection must still be authorized, tracked, readable and unchanged immediately before POST',
      () async {
    for (final changed in [
      'role',
      'candidate',
      'queue',
      'protocol',
      'history'
    ]) {
      final service = _Service();
      switch (changed) {
        case 'candidate':
          service.candidate['rejections'] = [
            {'type': 'temporary', 'reason': 'Busy'}
          ];
        case 'queue':
          service.rows.clear();
        case 'protocol':
          service.rows.add(_row(episode: 2, protocol: 'usenet'));
        case 'history':
          service.unavailable = true;
      }
      var checks = 0;
      final attempts = await ImportRecovery().submit(service, selection,
          force: false, authorized: () => changed != 'role' || ++checks <= 2);
      expect(attempts.single.phase, ImportPhase.notSubmitted, reason: changed);
      expect(service.posts, 0);
    }
  });

  test(
      'failed commands unlock only reconciled, unchanged episodes; refresh errors keep them locked',
      () async {
    final service = _Service();
    final recovery = ImportRecovery();
    final attempt = (await recovery.submit(service, selection,
            force: false, authorized: () => true))
        .single;
    expect(attempt.phase, ImportPhase.failed);
    expect(recovery.conflicts(target, video), isFalse);
    service.unavailable = true;
    await recovery.verify(service, attempt);
    expect(recovery.conflicts(target, video), isTrue);
    service.unavailable = false;
    service.history.add(const SonarrHistoryRecord(
        id: 1,
        seriesId: 7,
        episodeId: 1,
        downloadId: 'pack',
        eventType: 'downloadFolderImported'));
    await recovery.verify(service, attempt);
    expect(recovery.conflicts(target, video), isTrue);
    expect(attempt.verifiedEpisodes, isEmpty);
    expect(service.posts, 1);
  });

  test('retry rechecks a failed command before sending another POST', () async {
    final service = _Service();
    final recovery = ImportRecovery();
    final first = (await recovery.submit(service, selection,
            force: false, authorized: () => true))
        .single;
    expect(first.phase, ImportPhase.failed);
    expect(recovery.conflicts(target, video), isFalse);
    service.library[1] = const SonarrEpisode(
        id: 1,
        seriesId: 7,
        seasonNumber: 1,
        episodeNumber: 1,
        hasFile: true,
        episodeFileId: 8,
        episodeFile: SonarrEpisodeFile(id: 8, seriesId: 7));
    service.history.add(SonarrHistoryRecord(
        id: 1,
        seriesId: 7,
        episodeId: 1,
        downloadId: 'pack',
        eventType: 'downloadFolderImported',
        data: {'FileId': '8', 'DroppedPath': video.path}));
    final retry = (await recovery.submit(service, selection,
            force: false, authorized: () => true))
        .single;
    expect(first.phase, ImportPhase.verified);
    expect(retry.phase, ImportPhase.notSubmitted);
    expect(service.posts, 1);
  });

  test(
      'fresh history must identify the source, episode, download and resulting library file',
      () {
    final attempt = ImportAttempt(target, [video])..beforeFiles[1] = 0;
    const episode = SonarrEpisode(
        id: 1,
        seriesId: 7,
        seasonNumber: 1,
        episodeNumber: 1,
        hasFile: true,
        episodeFileId: 9,
        episodeFile: SonarrEpisodeFile(id: 9, path: '/tv/new.mkv'));
    final json = {
      'id': 1,
      'seriesId': 7,
      'episodeId': 1,
      'downloadId': 'pack',
      'eventType': 'downloadFolderImported',
      'data': {
        'FileId': '9',
        'DroppedPath': video.path,
        'ImportedPath': '/tv/new.mkv'
      }
    };
    expect(
        attempt.matchesFile(video, episode, SonarrHistoryRecord.fromJson(json)),
        isTrue);
    for (final change in [
      {'seriesId': 8},
      {'episodeId': 2},
      {'downloadId': 'other'},
      {'eventType': 'grabbed'},
      {
        'data': {'FileId': '9', 'DroppedPath': '/other/E01.mkv'}
      },
      {
        'data': {'FileId': '8', 'DroppedPath': video.path}
      },
      {
        'data': {
          'FileId': '9',
          'DroppedPath': video.path,
          'ImportedPath': '/other/new.mkv'
        }
      },
      {
        'data': {'DroppedPath': video.path}
      },
    ]) {
      expect(
          attempt.matchesFile(video, episode,
              SonarrHistoryRecord.fromJson({...json, ...change})),
          isFalse,
          reason: '$change');
    }
  });
}
