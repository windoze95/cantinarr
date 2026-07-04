import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/episode_selection.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_season_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter routing by path: /episode and /queue get canned bodies,
/// every request (notably POST /command) is recorded for assertions.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({required this.episodes});

  final List<Map<String, dynamic>> episodes;
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
    requests.add(
        (method: options.method, path: options.uri.path, body: body));

    final path = options.uri.path;
    dynamic response = <String, dynamic>{};
    if (path.endsWith('/episode')) response = episodes;
    if (path.endsWith('/queue')) response = {'records': <dynamic>[]};
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
}) =>
    {
      'id': id,
      'seriesId': 7,
      'seasonNumber': 1,
      'episodeNumber': episodeNumber,
      'title': 'Episode $episodeNumber',
      'hasFile': hasFile,
      'monitored': false,
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
    SonarrEpisode.fromJson(_episodeJson(
        id: id, episodeNumber: id, hasFile: hasFile, aired: aired)
      ..remove(tba ? 'airDateUtc' : ''));

Future<_FakeAdapter> _pumpSeasonScreen(WidgetTester tester,
    {required List<Map<String, dynamic>> episodes}) async {
  final adapter = _FakeAdapter(episodes: episodes);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: const MaterialApp(
        home: SonarrSeasonScreen(
          instanceId: 'inst1',
          series: SonarrSeries(id: 7, title: 'Example'),
          seasonNumber: 1,
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

/// OutlinedButton.icon returns a private OutlinedButton subclass on some
/// Flutter versions, so match by subtype rather than exact runtime type.
Finder _button(String label) => find.ancestor(
    of: find.text(label), matching: find.bySubtype<OutlinedButton>());

void main() {
  group('undownloadedEpisodeIds', () {
    test('picks aired episodes without a file; skips unaired, TBA and '
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
    // Episode 101 downloaded, 102 aired+missing, 103 unaired.
    final episodes = [
      _episodeJson(id: 101, episodeNumber: 1, hasFile: true, aired: true),
      _episodeJson(id: 102, episodeNumber: 2, hasFile: false, aired: true),
      _episodeJson(id: 103, episodeNumber: 3, hasFile: false, aired: false),
    ];

    List<({String method, String path, dynamic body})> commands(
            _FakeAdapter adapter) =>
        adapter.requests.where((r) => r.path.endsWith('/command')).toList();

    testWidgets('episode magnifier runs an automatic EpisodeSearch',
        (tester) async {
      final adapter = await _pumpSeasonScreen(tester, episodes: episodes);

      // Tiles are sorted newest first: episode 3, 2, 1.
      await tester.tap(find.byTooltip('Automatic search').last);
      await tester.pumpAndSettle();

      final sent = commands(adapter);
      expect(sent, hasLength(1));
      expect(sent.single.body,
          {'name': 'EpisodeSearch', 'episodeIds': [101]});
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

      // Interactive can't run on a multi-selection: disabled.
      final interactive =
          tester.widget<OutlinedButton>(_button('Interactive'));
      expect(interactive.onPressed, isNull);

      // Quick-selects.
      await tester.tap(find.text('All'));
      await tester.pump();
      expect(find.text('3 selected'), findsOneWidget);
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
      expect(sent.single.body,
          {'name': 'EpisodeSearch', 'episodeIds': [102, 103]});
      // Selection mode exits after the search is sent.
      expect(find.byType(Checkbox), findsNothing);
      expect(find.text('Automatic'), findsOneWidget);
    });

    testWidgets('long-press enters selection mode with that episode selected',
        (tester) async {
      await _pumpSeasonScreen(tester, episodes: episodes);

      await tester.longPress(find.text('Episode 2'));
      await tester.pumpAndSettle();

      expect(find.text('1 selected'), findsOneWidget);
      expect(_button('Search 1 episode'), findsOneWidget);

      // Close returns to the normal action bar.
      await tester.tap(find.byTooltip('Cancel selection'));
      await tester.pump();
      expect(find.byType(Checkbox), findsNothing);
      expect(find.text('Automatic'), findsOneWidget);
    });
  });
}
