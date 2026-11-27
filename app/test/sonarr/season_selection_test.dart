import 'package:cantinarr/l10n/l10n.dart';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/episode_selection.dart';
import 'package:cantinarr/features/sonarr/ui/import_doctor_sheet.dart';
import 'package:cantinarr/features/sonarr/ui/episode_detail_sheet.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/queue_item_card.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_season_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter routing by path: /episode and /queue get canned bodies,
/// every request (command posts, monitor puts, file deletes) is recorded.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(
      {required this.episodes,
      required this.queue,
      this.seriesMonitored = true});

  final List<Map<String, dynamic>> episodes;
  final List<Map<String, dynamic>> queue;
  final queueReads = <Uri>[];
  bool queueFails = false;
  bool seriesMonitored;
  final List<({String method, String path, dynamic body})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    requests.add((method: options.method, path: options.uri.path, body: body));

    final path = options.uri.path;
    dynamic response = <String, dynamic>{};
    if (options.method == 'GET' && path.endsWith('/episode')) {
      final season = options.queryParameters['seasonNumber'];
      response = episodes
          .where((e) => season == null || e['seasonNumber'] == season)
          .toList();
    }
    if (path.endsWith('/queue')) {
      response = {
        'records': List.generate(100, (i) => {'id': 1000 + i, 'seriesId': 9})
      };
    }
    if (path.endsWith('/queue/details')) {
      queueReads.add(options.uri);
      if (queueFails) return ResponseBody.fromString('Unavailable', 503);
      response = queue;
    }
    if (path.endsWith('/history/series')) response = [];
    if (path.endsWith('/series/7')) {
      response = {
        'id': 7,
        'title': 'Example',
        'monitored': seriesMonitored,
        'seasons': [
          // Individually monitored episodes must work inside an off season.
          {'seasonNumber': 1, 'monitored': false},
        ],
      };
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

Map<String, dynamic> _episodeJson({
  required int id,
  required int episodeNumber,
  required bool hasFile,
  required bool aired,
  int seasonNumber = 1,
  bool monitored = false,
}) =>
    {
      'id': id,
      'seriesId': 7,
      'seasonNumber': seasonNumber,
      'episodeNumber': episodeNumber,
      'title': 'Episode $episodeNumber',
      'hasFile': hasFile,
      // Downloaded episodes carry their file id (used by delete-file flows).
      'episodeFileId': hasFile ? id * 10 : 0,
      'monitored': monitored,
      'airDateUtc': DateTime.now()
          .toUtc()
          .add(Duration(days: aired ? -30 : 30))
          .toIso8601String(),
    };

SonarrEpisode _episode({
  required int id,
  bool hasFile = false,
  bool aired = true,
  bool tba = false,
}) =>
    SonarrEpisode.fromJson(
        _episodeJson(id: id, episodeNumber: id, hasFile: hasFile, aired: aired)
          ..remove(tba ? 'airDateUtc' : ''));

Future<_FakeAdapter> _pumpSeasonScreen(
  WidgetTester tester, {
  required List<Map<String, dynamic>> episodes,
  int? seasonNumber = 1,
  bool seriesMonitored = true,
  Locale? locale,
  List<Map<String, dynamic>> queue = const [],
  String? role = 'admin',
}) async {
  final adapter = _FakeAdapter(
      episodes: episodes, queue: queue, seriesMonitored: seriesMonitored);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        authProvider.overrideWith(() => _Auth(role)),
      ],
      child: MaterialApp(
        locale: locale,
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: SonarrSeasonScreen(
          instanceId: 'inst1',
          series: const SonarrSeries(id: 7, title: 'Example'),
          seasonNumber: seasonNumber,
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

class _Auth extends AuthNotifier {
  _Auth(this.role);
  final String? role;
  @override
  Future<AuthState> build() async => AuthState(
      user: role == null
          ? null
          : UserProfile(id: 1, username: 'tester', role: role!));
}

Map<String, dynamic> _queueRow(
  int row,
  int? episode,
  String? download, {
  String state = 'importBlocked',
  String? message,
  String? severity,
  int? season = 1,
  String title = 'Release',
}) =>
    {
      'id': row,
      'episodeId': episode,
      'seriesId': 7,
      'seasonNumber': season,
      'downloadId': download,
      'title': title,
      'status': 'completed',
      'size': 100,
      'sizeleft': 0,
      'trackedDownloadState': state,
      'trackedDownloadStatus': severity,
      if (message != null)
        'statusMessages': [
          {
            'title': title,
            'messages': [message]
          }
        ],
    };

/// OutlinedButton.icon returns a private OutlinedButton subclass on some
/// Flutter versions, so match by subtype rather than exact runtime type.
Finder _button(String label) => find.ancestor(
    of: find.text(label), matching: find.bySubtype<OutlinedButton>());

void main() {
  group('undownloadedEpisodeIds', () {
    test(
        'picks aired episodes without a file; skips unaired, TBA and '
        'downloaded', () {
      final episodes = [
        _episode(id: 1, hasFile: true), // downloaded
        _episode(id: 2), // aired + missing -> wanted
        _episode(id: 3, aired: false), // unaired
        _episode(id: 4, tba: true), // no air date at all
        _episode(id: 5), // aired + missing -> wanted
      ];
      expect(undownloadedEpisodeIds(episodes), [2, 5]);
    });
  });

  group('SonarrSeasonScreen', () {
    // Episode 101 downloaded (file 1010), 102 aired+missing, 103 unaired.
    final episodes = [
      _episodeJson(id: 101, episodeNumber: 1, hasFile: true, aired: true),
      _episodeJson(id: 102, episodeNumber: 2, hasFile: false, aired: true),
      _episodeJson(id: 103, episodeNumber: 3, hasFile: false, aired: false),
    ];

    List<({String method, String path, dynamic body})> ofMethod(
            _FakeAdapter adapter, String method) =>
        adapter.requests.where((r) => r.method == method).toList();

    List<({String method, String path, dynamic body})> commands(
            _FakeAdapter adapter) =>
        adapter.requests.where((r) => r.path.endsWith('/command')).toList();

    testWidgets('bare blocked row is reviewable beyond the first global page',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester,
          episodes: episodes, queue: [_queueRow(13, 101, 'B', severity: 'ok')]);
      expect(adapter.queueReads.single.path,
          '/api/instances/inst1/api/v3/queue/details');
      expect(adapter.queueReads.single.queryParameters,
          {'seriesId': '7', 'includeEpisode': 'true'});
      expect(adapter.requests.any((r) => r.path.endsWith('/queue')), isFalse);
      expect(
          find.textContaining('Sonarr did not provide a reason'), findsWidgets);
      expect(find.textContaining('waiting to import'), findsNothing);
      await tester.tap(find.text('Review import').first);
      await tester.pumpAndSettle();
      final doctor =
          tester.widget<ImportDoctorSheet>(find.byType(ImportDoctorSheet));
      expect((doctor.instanceId, doctor.item.id, doctor.item.downloadId),
          ('inst1', 13, 'B'));
      expect(
          find.descendant(
              of: find.byType(ImportDoctorSheet),
              matching: find.textContaining('Sonarr did not provide a reason')),
          findsOneWidget);
      expect(find.text('No automatic fix is needed.'), findsNothing);
      expect(adapter.requests.every((r) => r.method == 'GET'), isTrue);
    });

    testWidgets(
        'pending and importing, even with a generic warning, are not blockers',
        (tester) async {
      await _pumpSeasonScreen(tester, episodes: episodes, queue: [
        _queueRow(11, 101, 'A', state: 'importPending'),
        _queueRow(12, 102, 'B', state: 'importing', severity: 'warning'),
        _queueRow(13, 103, 'C',
            state: 'importPending',
            severity: 'warning',
            message: 'Checking download'),
      ]);
      expect(
          find.text('100% — Downloaded — waiting to import'), findsNWidgets(2));
      expect(find.text('100% — Importing'), findsOneWidget);
      expect(find.text('Review import'), findsNothing);
    });

    testWidgets(
        'an unreadable queue offers retry instead of treating blockers as cleared',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester,
          episodes: [episodes.first], queue: [_queueRow(11, 101, 'A')]);
      adapter.queueFails = true;
      tester.state<RefreshIndicatorState>(find.byType(RefreshIndicator)).show();
      await tester.pumpAndSettle();
      expect(find.text('Review import'), findsNothing);
      expect(find.textContaining('Failed to load episodes'), findsOneWidget);
      adapter.queueFails = false;
      await tester.ensureVisible(find.text('Try Again'));
      await tester.tap(find.text('Try Again'));
      await tester.pumpAndSettle();
      expect(find.textContaining('1 blocked download'), findsOneWidget);
    });

    for (final season in <int?>[1, null]) {
      testWidgets(
          'a row without an episode identity stays at its known scope ($season)',
          (tester) async {
        final adapter = await _pumpSeasonScreen(tester,
            episodes: [episodes.first],
            seasonNumber: season,
            queue: [
              _queueRow(11, null, 'A', title: 'Known season'),
              _queueRow(12, null, 'B', season: null, title: 'Series only'),
              _queueRow(13, null, 'C', season: 2, title: 'Other season'),
            ]);
        // No per-episode review, even though just one episode is displayed.
        expect(find.text('Review import'), findsOneWidget);
        await tester.tap(find.text('Review import'));
        await tester.pumpAndSettle();
        if (season == null) {
          expect(find.text('Known season · #11'), findsOneWidget);
          expect(find.text('Other season · #13'), findsOneWidget);
          await tester.tap(find.text('Series only · #12'));
          await tester.pumpAndSettle();
        }
        final sheet =
            tester.widget<ImportDoctorSheet>(find.byType(ImportDoctorSheet));
        expect(sheet.item.id, season == null ? 12 : 11);
        expect(sheet.item.episodeId, isNull);
        expect(adapter.requests.every((r) => r.method == 'GET'), isTrue);
      });
    }

    testWidgets('recognized archive rejection on a pending row wraps in French',
        (tester) async {
      tester.view.physicalSize = const Size(320, 700);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      const archive = 'Found archive file, might need to be extracted';
      await _pumpSeasonScreen(tester,
          episodes: [episodes.first],
          locale: const Locale('fr'),
          queue: [
            _queueRow(11, 101, 'A', state: 'importPending', message: archive),
          ]);
      final reason = find
          .textContaining('L’archive doit être extraite avant l’importation.');
      expect(reason, findsWidgets);
      await tester.tap(find.text('Episode 1'));
      await tester.pumpAndSettle();
      expect(
          find.descendant(
              of: find.byType(EpisodeDetailSheet), matching: reason),
          findsOneWidget);
      await tester.tap(find.descendant(
          of: find.byType(EpisodeDetailSheet),
          matching: find.text('Examiner l’importation')));
      await tester.pumpAndSettle();
      expect(find.text('• $archive'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    for (final reverse in [false, true]) {
      testWidgets(
          'season pack and overlapping download open their own Doctor (reversed: $reverse)',
          (tester) async {
        final rows = [
          _queueRow(11, 101, 'pack-A', title: 'Pack'),
          _queueRow(12, 102, 'pack-A', title: 'Pack', season: null),
          _queueRow(13, 101, 'B',
              title: 'Alternative', message: 'New Sonarr rejection'),
          _queueRow(14, 201, 'other season', season: 2),
        ];
        final queue = (reverse ? rows.reversed : rows).toList();
        final adapter = await _pumpSeasonScreen(tester,
            episodes: episodes.take(2).toList(), queue: queue);
        expect(find.text('2 blocked downloads · 2 affected episodes'),
            findsOneWidget);
        final review = find.text('Review import');
        Finder episodeReview(int number) => find.descendant(
            of: find
                .ancestor(
                    of: find.text('Episode $number'),
                    matching: find.byType(InkWell))
                .first,
            matching: review);
        Future<void> tap(Finder finder) async {
          await tester.ensureVisible(finder);
          await tester.tap(finder);
          await tester.pumpAndSettle();
        }

        ImportDoctorSheet doctor(int id, int episode, String download) {
          final sheet =
              tester.widget<ImportDoctorSheet>(find.byType(ImportDoctorSheet));
          expect((
            sheet.instanceId,
            sheet.item.id,
            sheet.item.episodeId,
            sheet.item.downloadId
          ), (
            'inst1',
            id,
            episode,
            download
          ));
          return sheet;
        }

        Future<void> closeDoctor() async {
          Navigator.of(tester.element(find.byType(ImportDoctorSheet))).pop();
          await tester.pumpAndSettle();
        }

        // The season picker groups the pack; the episode picker keeps B.
        await tap(review.first);
        expect(find.text('Pack · #11'), findsOneWidget);
        expect(find.textContaining('S01E01 · S01E02'), findsOneWidget);
        await tap(find.text('Alternative · #13'));
        doctor(13, 101, 'B');
        await closeDoctor();
        await tap(episodeReview(1));
        await tap(find.text('Pack · #11'));
        doctor(11, 101, 'pack-A');
        await closeDoctor();
        // From E2, review is direct and uses its own row, not E1's pack representative.
        await tap(episodeReview(2));
        doctor(12, 102, 'pack-A');
        await closeDoctor();
        // Detail also retains both queue cards and refreshes on the existing callback.
        await tap(find.text('Episode 1'));
        expect(find.byType(QueueItemCard), findsNWidgets(2));
        final alternative = find.widgetWithText(QueueItemCard, 'Alternative');
        expect(
            find.descendant(
                of: alternative,
                matching: find.textContaining('New Sonarr rejection')),
            findsOneWidget);
        await tap(find.descendant(of: alternative, matching: review));
        final selected = doctor(13, 101, 'B');
        queue.removeWhere((q) => q['id'] == 13);
        Navigator.of(tester.element(find.byType(ImportDoctorSheet))).pop();
        selected.onChanged();
        await tester.pumpAndSettle();
        expect(find.byType(EpisodeDetailSheet), findsNothing);
        expect(find.textContaining('1 blocked download · 2 affected episodes'),
            findsOneWidget);
        expect(adapter.queueReads, hasLength(2));
        expect(
            adapter.requests.every(
                (r) => r.method == 'GET' && !r.path.endsWith('/manualimport')),
            isTrue);
      });
    }

    testWidgets(
        'blank client IDs remain three independent reviews, without import actions',
        (tester) async {
      final ids = <String?>[null, '', '  '];
      final adapter = await _pumpSeasonScreen(tester, episodes: [
        episodes.first
      ], queue: [
        for (var i = 0; i < ids.length; i++)
          _queueRow(11 + i, 101, ids[i],
              message: 'Individual episode mappings need confirmation'),
      ]);
      expect(find.text('3 blocked downloads · 1 affected episode'),
          findsOneWidget);
      for (var i = 0; i < ids.length; i++) {
        await tester.tap(find.text('Review import').first);
        await tester.pumpAndSettle();
        await tester.tap(find.text('Release · #${11 + i}'));
        await tester.pumpAndSettle();
        final sheet =
            tester.widget<ImportDoctorSheet>(find.byType(ImportDoctorSheet));
        expect((sheet.item.id, sheet.item.downloadId), (11 + i, ids[i]));
        expect(find.textContaining('Manual import is unavailable'),
            findsOneWidget);
        expect(find.text('Manual import'), findsNothing);
        expect(find.text('Force import'), findsNothing);
        Navigator.of(tester.element(find.byType(ImportDoctorSheet))).pop();
        await tester.pumpAndSettle();
      }
      expect(
          adapter.requests.every(
              (r) => r.method == 'GET' && !r.path.endsWith('/manualimport')),
          isTrue);
    });

    testWidgets(
        'season review uses the pack member with a reason without assigning it to its sibling',
        (tester) async {
      await _pumpSeasonScreen(tester,
          episodes: episodes.take(2).toList(),
          queue: [
            _queueRow(11, 101, 'pack-A'),
            _queueRow(12, 102, 'pack-A',
                message: 'Found archive file, might need to be extracted'),
          ]);
      expect(
          find.textContaining(
              '1 blocked download · 2 affected episodes · Import blocked: The archive'),
          findsOneWidget);
      expect(find.textContaining('Sonarr did not provide a reason'),
          findsOneWidget);
      await tester.tap(find.text('Review import').first);
      await tester.pumpAndSettle();
      final doctor =
          tester.widget<ImportDoctorSheet>(find.byType(ImportDoctorSheet));
      expect((doctor.item.id, doctor.item.episodeId), (12, 102));
      expect(find.text('Manual import'), findsOneWidget);
    });

    for (final role in <String?>['user', null]) {
      testWidgets('review shortcuts are absent for role $role', (tester) async {
        await _pumpSeasonScreen(tester,
            episodes: [episodes.first],
            role: role,
            queue: [_queueRow(13, 101, 'B', message: 'New Sonarr rejection')]);
        expect(find.text('Review import'), findsNothing);
        await tester.tap(find.text('Episode 1'));
        await tester.pumpAndSettle();
        expect(find.byType(QueueItemCard), findsOneWidget);
        expect(find.text('Review import'), findsNothing);
        expect(find.textContaining('tap to resolve'), findsNothing);
      });
    }

    testWidgets('French action bar retains selection and request behavior',
        (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final adapter = await _pumpSeasonScreen(tester,
          episodes: episodes, locale: const Locale('fr'));
      expect(find.text('Automatique'), findsOneWidget);
      expect(find.text('Manuelle'), findsOneWidget);
      await tester.tap(find.byIcon(Icons.arrow_drop_up));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Épisodes individuels'));
      await tester.pumpAndSettle();
      expect(_button('Rechercher 1 épisode'), findsOneWidget);
      expect(tester.takeException(), isNull);
      final deleteNone =
          tester.widget<OutlinedButton>(_button('Supprimer 0 fichiers'));
      expect(deleteNone.onPressed, isNull);
      await tester.tap(_button('Rechercher 1 épisode'));
      await tester.pumpAndSettle();
      expect(commands(adapter).single.body, {
        'name': 'EpisodeSearch',
        'episodeIds': [102]
      });
    });

    testWidgets('episode magnifier runs an automatic EpisodeSearch',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      // Tiles are sorted newest first: episode 3, 2, 1.
      await tester.tap(find.byTooltip('Automatic search').last);
      await tester.pumpAndSettle();

      final sent = commands(adapter);
      expect(sent, hasLength(1));
      expect(sent.single.body, {
        'name': 'EpisodeSearch',
        'episodeIds': [101]
      });
      // No interactive releases screen was pushed.
      expect(find.text('Automatic'), findsOneWidget);
    });

    testWidgets(
        'arrow menu enters selection mode with undownloaded preselected '
        'and searches the selection', (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.tap(find.byIcon(Icons.arrow_drop_up));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Individual episodes'));
      await tester.pumpAndSettle();

      // Only the aired-but-missing episode (102) starts selected.
      expect(find.text('1 selected'), findsOneWidget);
      expect(find.byType(Checkbox), findsNWidgets(3));
      expect(_button('Search 1 episode'), findsOneWidget);

      // The selection has no downloaded files, so Delete is disabled.
      final deleteNone =
          tester.widget<OutlinedButton>(_button('Delete 0 files'));
      expect(deleteNone.onPressed, isNull);

      // Quick-selects.
      await tester.tap(find.text('All'));
      await tester.pump();
      expect(find.text('3 selected'), findsOneWidget);
      // Episode 101 has a file now, so Delete becomes available.
      expect(_button('Delete 1 file'), findsOneWidget);
      await tester.tap(find.text('None'));
      await tester.pump();
      expect(find.text('0 selected'), findsOneWidget);
      final searchNone =
          tester.widget<OutlinedButton>(_button('Search 0 episodes'));
      expect(searchNone.onPressed, isNull);
      await tester.tap(find.text('Undownloaded'));
      await tester.pump();
      expect(find.text('1 selected'), findsOneWidget);

      // Tapping a tile toggles it: add the unaired episode 3 (first tile).
      await tester.tap(find.text('Episode 3'));
      await tester.pump();
      expect(find.text('2 selected'), findsOneWidget);

      await tester.tap(_button('Search 2 episodes'));
      await tester.pumpAndSettle();

      final sent = commands(adapter);
      expect(sent, hasLength(1));
      expect(sent.single.body, {
        'name': 'EpisodeSearch',
        'episodeIds': [102, 103]
      });
      // Selection mode exits after the search is sent.
      expect(find.byType(Checkbox), findsNothing);
      expect(find.text('Automatic'), findsOneWidget);
    });

    testWidgets('deleting the selection removes its files after confirming',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.tap(find.byIcon(Icons.arrow_drop_up));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Individual episodes'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('All'));
      await tester.pump();

      await tester.tap(_button('Delete 1 file'));
      await tester.pumpAndSettle();
      expect(find.text('Delete Files'), findsOneWidget);

      // Cancel first: nothing deleted, selection stays.
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(ofMethod(adapter, 'DELETE'), isEmpty);
      expect(find.text('3 selected'), findsOneWidget);

      await tester.tap(_button('Delete 1 file'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();

      final deletes = ofMethod(adapter, 'DELETE');
      expect(deletes, hasLength(1));
      expect(deletes.single.path, endsWith('/episodefile/bulk'));
      expect(deletes.single.body, {
        'episodeFileIds': [1010]
      });
      // Selection mode exits after the delete.
      expect(find.byType(Checkbox), findsNothing);
    });

    testWidgets(
        'long-press opens the episode menu; Select Episodes enters selection',
        (tester) async {
      await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.longPress(find.text('Episode 2'));
      await tester.pumpAndSettle();

      expect(find.text('Automatic Search'), findsOneWidget);
      expect(find.text('Interactive Search'), findsOneWidget);
      // Fixtures are unmonitored, and episode 2 has no file to delete.
      expect(find.text('Monitor Episode'), findsOneWidget);
      expect(find.text('Delete File'), findsNothing);

      await tester.tap(find.text('Select Episodes'));
      await tester.pumpAndSettle();
      expect(find.text('1 selected'), findsOneWidget);
      expect(_button('Search 1 episode'), findsOneWidget);

      // Close returns to the normal action bar.
      await tester.tap(find.byTooltip('Cancel selection'));
      await tester.pump();
      expect(find.byType(Checkbox), findsNothing);
      expect(find.text('Automatic'), findsOneWidget);
    });

    testWidgets('episode menu toggles monitoring', (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.longPress(find.text('Episode 2'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Monitor Episode'));
      await tester.pumpAndSettle();

      final puts = ofMethod(adapter, 'PUT');
      expect(puts, hasLength(1));
      expect(puts.single.path, endsWith('/episode/monitor'));
      expect(puts.single.body, {
        'episodeIds': [102],
        'monitored': true
      });
      expect(find.text('Monitoring S01E02'), findsOneWidget);

      // The change is patched in place — no refetch — so the menu offers the
      // other direction the next time it opens.
      expect(
        adapter.requests
            .where((r) => r.method == 'GET' && r.path.endsWith('/episode')),
        hasLength(1),
      );
      await tester.longPress(find.text('Episode 2'));
      await tester.pumpAndSettle();
      expect(find.text('Unmonitor Episode'), findsOneWidget);
    });

    testWidgets('unmonitored episodes are dimmed, monitored ones are not',
        (tester) async {
      await _pumpSeasonScreen(tester, episodes: [
        _episodeJson(
            id: 101,
            episodeNumber: 1,
            hasFile: true,
            aired: true,
            monitored: true),
        _episodeJson(id: 102, episodeNumber: 2, hasFile: false, aired: true),
      ]);

      /// Every opacity applied to the row whose title is [episodeTitle].
      List<double> rowOpacities(String episodeTitle) => tester
          .widgetList<Opacity>(find.descendant(
            of: find
                .ancestor(
                    of: find.text(episodeTitle), matching: find.byType(InkWell))
                .first,
            matching: find.byType(Opacity),
          ))
          .map((o) => o.opacity)
          .toList();

      expect(rowOpacities('Episode 1'), everyElement(1.0));
      expect(rowOpacities('Episode 2'), isNotEmpty);
      expect(rowOpacities('Episode 2'), everyElement(lessThan(1.0)));
      // Dimming is the only monitoring signal on a row now — no bookmarks.
      expect(find.byIcon(Icons.bookmark), findsNothing);
      expect(find.byIcon(Icons.bookmark_border), findsNothing);
    });

    for (final seasonNumber in <int?>[1, null]) {
      testWidgets(
          'unmonitored series disables episode monitoring '
          'in ${seasonNumber == null ? "all seasons" : "one season"}',
          (tester) async {
        final adapter = await _pumpSeasonScreen(
          tester,
          seasonNumber: seasonNumber,
          seriesMonitored: false,
          episodes: [
            _episodeJson(
                id: 101,
                episodeNumber: 1,
                hasFile: false,
                aired: true,
                monitored: true)
          ],
        );
        // The navigation seed is monitored; the live parent must override it.
        final row = find
            .ancestor(
                of: find.text('Episode 1'), matching: find.byType(InkWell))
            .first;
        expect(
            tester
                .widgetList<Opacity>(find.descendant(
                  of: row,
                  matching: find.byType(Opacity),
                ))
                .map((o) => o.opacity),
            everyElement(0.5));
        await tester.longPress(find.text('Episode 1'));
        await tester.pumpAndSettle();
        final action = find.widgetWithText(ListTile, 'Unmonitor Episode');
        expect(tester.widget<ListTile>(action).enabled, isFalse);
        expect(
            tester
                .widget<Icon>(find.descendant(
                  of: action,
                  matching: find.byType(Icon),
                ))
                .color,
            AppTheme.textSecondary);
        await tester.tap(find.text('Unmonitor Episode'));
        await tester.pump();
        expect(ofMethod(adapter, 'PUT'), isEmpty);

        Navigator.of(tester.element(action)).pop();
        await tester.pumpAndSettle();
        adapter.seriesMonitored = true;
        tester
            .state<RefreshIndicatorState>(find.byType(RefreshIndicator))
            .show();
        await tester.pumpAndSettle();
        expect(
            tester
                .widgetList<Opacity>(find.descendant(
                  of: row,
                  matching: find.byType(Opacity),
                ))
                .map((o) => o.opacity),
            everyElement(1.0));
        await tester.longPress(find.text('Episode 1'));
        await tester.pumpAndSettle();
        expect(tester.widget<ListTile>(action).enabled, isTrue);
        // Re-enabling the series restores the saved episode setting, even
        // though its season remains off, without a child-monitoring write.
        expect(ofMethod(adapter, 'PUT'), isEmpty);
      });
    }

    testWidgets('episode menu deletes a downloaded file after confirming',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.longPress(find.text('Episode 1'));
      await tester.pumpAndSettle();
      expect(find.text('Delete File'), findsOneWidget);
      await tester.tap(find.text('Delete File'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();

      final deletes = ofMethod(adapter, 'DELETE');
      expect(deletes, hasLength(1));
      expect(deletes.single.body, {
        'episodeFileIds': [1010]
      });
    });

    testWidgets(
        'all-seasons mode groups by season headers and searches the series',
        (tester) async {
      final allEpisodes = [
        _episodeJson(
            id: 201,
            episodeNumber: 1,
            hasFile: true,
            aired: true,
            seasonNumber: 2),
        _episodeJson(id: 101, episodeNumber: 1, hasFile: true, aired: true),
        _episodeJson(id: 102, episodeNumber: 2, hasFile: false, aired: true),
      ];
      final adapter = await _pumpSeasonScreen(tester,
          episodes: allEpisodes, seasonNumber: null);

      expect(find.text('All Seasons'), findsOneWidget);
      expect(find.text('Season 2'), findsOneWidget);
      expect(find.text('Season 1'), findsOneWidget);

      await tester.tap(_button('Automatic'));
      await tester.pumpAndSettle();
      final sent = commands(adapter);
      expect(sent.single.body, {'name': 'SeriesSearch', 'seriesId': 7});
    });
  });
}
