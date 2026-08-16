import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/monitor_bookmark.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// American Dad!-shaped payload: season 22 has 13 episodes, 11 of them aired
/// and on disk, and Sonarr is still waiting on the last two. The fraction is
/// Sonarr's own episodeCount — 11/11, the season is caught up on what was
/// asked — but the two unaired episodes must be named in the suffix, or the
/// card claims "100% • 11/11 Episodes Available" for a season the episode
/// list shows 13 rows for.
Map<String, dynamic> _seriesJson({bool season22Monitored = true}) => {
      'id': 7,
      'title': 'American Dad!',
      'monitored': true,
      'status': 'continuing',
      'statistics': {
        'seasonCount': 2,
        'episodeFileCount': 19,
        'episodeCount': 19,
        'totalEpisodeCount': 21,
        'sizeOnDisk': 12000000000,
        'percentOfEpisodes': 100.0,
      },
      'seasons': [
        {
          'seasonNumber': 21,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 8,
            'episodeCount': 8,
            'totalEpisodeCount': 8,
            'sizeOnDisk': 5000000000,
            'percentOfEpisodes': 100.0,
          },
        },
        {
          'seasonNumber': 22,
          'monitored': season22Monitored,
          'statistics': {
            'episodeFileCount': 11,
            'episodeCount': 11,
            'totalEpisodeCount': 13,
            'sizeOnDisk': 6657000000,
            'percentOfEpisodes': 100.0,
            'nextAiring': '2026-09-13T04:00:00Z',
          },
        },
      ],
    };

/// One `queue/details` row per queued episode, as Sonarr returns it.
Map<String, dynamic> _queueRow(int episode,
        {required int season, required String airDate, required String state}) =>
    {
      'id': 900 + episode,
      'seriesId': 7,
      'title': 'American.Dad.S${season}E$episode',
      'trackedDownloadState': state,
      'status': 'completed',
      'episode': {
        'id': 100 + episode,
        'seasonNumber': season,
        'episodeNumber': episode,
        'airDateUtc': airDate,
      },
    };

/// One episode row, reduced to what the bookmarks are computed from.
Map<String, dynamic> _episodeJson(
        {required int id, required int season, required bool monitored}) =>
    {
      'id': id,
      'seriesId': 7,
      'seasonNumber': season,
      'episodeNumber': id % 100,
      'monitored': monitored,
    };

class _SeriesAdapter implements HttpClientAdapter {
  _SeriesAdapter({
    this.queue = const [],
    this.queueFails = false,
    this.episodes = const [],
    this.episodesFail = false,
    this.seriesJson,
  });

  final List<Map<String, dynamic>> queue;
  final bool queueFails;
  final List<Map<String, dynamic>> episodes;
  final bool episodesFail;
  final Map<String, dynamic>? seriesJson;

  /// Every request the screen made, so a test can prove what it refetched.
  final List<({String method, String path})> requests = [];

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? _,
      Future<void>? __) async {
    const json = Headers.jsonContentType;
    requests.add((method: options.method, path: options.path));
    if (options.path.endsWith('/queue/details')) {
      if (queueFails) {
        return ResponseBody.fromString('{"message":"boom"}', 500,
            headers: {
              Headers.contentTypeHeader: [json]
            });
      }
      return ResponseBody.fromString(jsonEncode(queue), 200, headers: {
        Headers.contentTypeHeader: [json]
      });
    }
    if (options.method == 'GET' && options.path.endsWith('/episode')) {
      if (episodesFail) {
        return ResponseBody.fromString('{"message":"boom"}', 500,
            headers: {
              Headers.contentTypeHeader: [json]
            });
      }
      return ResponseBody.fromString(jsonEncode(episodes), 200, headers: {
        Headers.contentTypeHeader: [json]
      });
    }
    return ResponseBody.fromString(
        jsonEncode(seriesJson ?? _seriesJson()), 200, headers: {
      Headers.contentTypeHeader: [json]
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _SeriesAdapter adapter,
    {Map<String, dynamic>? seriesJson}) async {
  // Phone-sized, like the screen this shipped wrong on.
  await tester.binding.setSurfaceSize(const Size(390, 844));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;

  await tester.pumpWidget(ProviderScope(
    overrides: [backendClientProvider.overrideWithValue(dio)],
    child: MaterialApp(
      theme: AppTheme.dark,
      home: SonarrSeriesDetailScreen(
        instanceId: 'sonarr-main',
        series: SonarrSeries.fromJson(seriesJson ?? _seriesJson()),
      ),
    ),
  ));
  await tester.pumpAndSettle();
}

/// The card carrying [seasonTitle] — the InkWell closest to its label.
Finder _card(String seasonTitle) => find
    .ancestor(of: find.text(seasonTitle), matching: find.byType(InkWell))
    .first;

MonitorFill _fillOf(WidgetTester tester, String seasonTitle) => tester
    .widget<MonitorBookmark>(find.descendant(
        of: _card(seasonTitle), matching: find.byType(MonitorBookmark)))
    .fill;

List<double> _opacitiesOf(WidgetTester tester, String seasonTitle) => tester
    .widgetList<Opacity>(
        find.descendant(of: _card(seasonTitle), matching: find.byType(Opacity)))
    .map((o) => o.opacity)
    .toList();

/// The line binds each phrase with no-break spaces so a narrow card cannot
/// split a count from its words; finders read it the way a person does.
Finder _line(String text) => find.byWidgetPredicate(
    (w) => w is Text && w.data?.replaceAll('\u00A0', ' ') == text);

Color _colorOf(WidgetTester tester, String text) =>
    tester.widget<Text>(_line(text)).style!.color!;

void main() {
  testWidgets('an airing season counts the episodes it is still waiting on',
      (tester) async {
    await _pump(tester, _SeriesAdapter());

    // Season 22: caught up on everything that was asked for, but the two
    // unaired episodes are named — a bare "11/11" (or "100%") is what made
    // the season look finished.
    expect(_line('11/11 Episodes Available • 2 unaired'), findsOneWidget);
    expect(
      _colorOf(tester, '11/11 Episodes Available • 2 unaired'),
      AppTheme.downloading,
    );
    expect(_line('11/11 Episodes Available'), findsNothing);
    expect(find.textContaining('100%'), findsNothing);

    // Season 21 is genuinely done, and All Seasons rolls both up.
    expect(_line('8/8 Episodes Available'), findsOneWidget);
    expect(_colorOf(tester, '8/8 Episodes Available'), AppTheme.available);
    expect(_line('19/19 Episodes Available • 2 unaired'), findsOneWidget);
  });

  testWidgets('episodes sitting in the queue are not called missing',
      (tester) async {
    await _pump(
      tester,
      _SeriesAdapter(queue: [
        for (final e in [12, 13])
          _queueRow(e,
              season: 22,
              airDate: '2026-09-13T04:00:00Z',
              state: 'importPending'),
      ]),
    );

    // The two remaining episodes are unaired *and* already downloaded, so they
    // are named once — by the state the admin can act on.
    expect(_line('11/11 Episodes Available • 2 waiting to import'),
        findsOneWidget);
    expect(find.textContaining('unaired'), findsNothing);
    expect(_line('19/19 Episodes Available • 2 waiting to import'),
        findsOneWidget);
  });

  testWidgets('a queue that fails to load leaves the cards standing',
      (tester) async {
    await _pump(tester, _SeriesAdapter(queueFails: true));

    expect(_line('11/11 Episodes Available • 2 unaired'), findsOneWidget);
    expect(find.textContaining('Failed to load'), findsNothing);
  });

  group('season bookmarks', () {
    // Season 21 is watched whole; season 22 has two episodes the admin took
    // out. Statistics cannot tell those two apart from the two unaired ones,
    // which is why the screen reads the episode list at all.
    final episodes = [
      for (var e = 1; e <= 8; e++)
        _episodeJson(id: 2100 + e, season: 21, monitored: true),
      for (var e = 1; e <= 11; e++)
        _episodeJson(id: 2200 + e, season: 22, monitored: true),
      for (var e = 12; e <= 13; e++)
        _episodeJson(id: 2200 + e, season: 22, monitored: false),
    ];

    /// The same list after season 22 is switched off: Sonarr cascades a
    /// season's monitored flag onto its episodes, so they all come back
    /// unmonitored except the ones [monitoredAlone] names — episodes the admin
    /// went back into the list and monitored on their own.
    List<Map<String, dynamic>> season22Off({
      Set<int> monitoredAlone = const {},
    }) =>
        [
          for (var e = 1; e <= 8; e++)
            _episodeJson(id: 2100 + e, season: 21, monitored: true),
          for (var e = 1; e <= 13; e++)
            _episodeJson(
                id: 2200 + e,
                season: 22,
                monitored: monitoredAlone.contains(e)),
        ];

    testWidgets('a season holding unmonitored episodes is half-filled',
        (tester) async {
      await _pump(tester, _SeriesAdapter(episodes: episodes));

      expect(_fillOf(tester, 'Season 21'), MonitorFill.full);
      expect(_fillOf(tester, 'Season 22'), MonitorFill.partial);
      // The card is still monitored, so nothing about it is dimmed.
      expect(_opacitiesOf(tester, 'Season 22'), everyElement(1.0));
      // The availability line calls season 22's remainder "unaired" — the
      // bookmark's tooltip is the only place the two left out are named.
      expect(
        find.byTooltip('Stop monitoring — 2 episodes are unmonitored'),
        findsOneWidget,
      );
      expect(find.byTooltip('Stop monitoring'), findsOneWidget);
    });

    testWidgets('a monitored season with every episode left out is half-filled',
        (tester) async {
      // The other way the flags come apart: monitoring a season cascades onto
      // its episodes, but unmonitoring them one by one leaves the season flag
      // alone. Nothing in it is being searched for now — yet the flag is what
      // the next episode Sonarr discovers inherits, so this is not a season
      // nobody is watching either.
      await _pump(
        tester,
        _SeriesAdapter(episodes: [
          for (var e = 1; e <= 8; e++)
            _episodeJson(id: 2100 + e, season: 21, monitored: true),
          for (var e = 1; e <= 13; e++)
            _episodeJson(id: 2200 + e, season: 22, monitored: false),
        ]),
      );

      expect(_fillOf(tester, 'Season 22'), MonitorFill.partial);
      // The season flag is on, so the card is not dimmed.
      expect(_opacitiesOf(tester, 'Season 22'), everyElement(1.0));
      expect(
        find.byTooltip('Stop monitoring — 13 episodes are unmonitored'),
        findsOneWidget,
      );
    });

    testWidgets('an unmonitored season is hollow and dimmed', (tester) async {
      await _pump(
        tester,
        _SeriesAdapter(
          episodes: season22Off(),
          seriesJson: _seriesJson(season22Monitored: false),
        ),
        seriesJson: _seriesJson(season22Monitored: false),
      );

      expect(_fillOf(tester, 'Season 22'), MonitorFill.none);
      expect(_opacitiesOf(tester, 'Season 22'), isNotEmpty);
      expect(_opacitiesOf(tester, 'Season 22'), everyElement(lessThan(1.0)));
      expect(find.byTooltip('Monitor'), findsOneWidget);

      // The monitored season beside it is untouched.
      expect(_fillOf(tester, 'Season 21'), MonitorFill.full);
      expect(_opacitiesOf(tester, 'Season 21'), everyElement(1.0));
    });

    testWidgets(
        'an unmonitored season holding a monitored episode is half-filled',
        (tester) async {
      // Reachable from the episode list: monitoring one episode leaves the
      // season flag alone, and a hollow bookmark would say Sonarr is watching
      // nothing in a season it is still searching an episode of.
      await _pump(
        tester,
        _SeriesAdapter(
          episodes: season22Off(monitoredAlone: {12}),
          seriesJson: _seriesJson(season22Monitored: false),
        ),
        seriesJson: _seriesJson(season22Monitored: false),
      );

      expect(_fillOf(tester, 'Season 22'), MonitorFill.partial);
      // The season itself is still off, so the card stays in the background —
      // the half-filled bookmark is what says part of it is being watched.
      expect(_opacitiesOf(tester, 'Season 22'), everyElement(lessThan(1.0)));
      expect(
        find.byTooltip('Monitor — 1 episode is monitored'),
        findsOneWidget,
      );
    });

    testWidgets('a whole season monitored episode by episode is half-filled',
        (tester) async {
      // Every episode is monitored, but the season flag is not: nothing new
      // Sonarr learns about will be picked up, so this is not a whole season.
      await _pump(
        tester,
        _SeriesAdapter(
          episodes: season22Off(monitoredAlone: {for (var e = 1; e <= 13; e++) e}),
          seriesJson: _seriesJson(season22Monitored: false),
        ),
        seriesJson: _seriesJson(season22Monitored: false),
      );

      expect(_fillOf(tester, 'Season 22'), MonitorFill.partial);
      expect(
        find.byTooltip('Monitor — 13 episodes are monitored'),
        findsOneWidget,
      );
    });

    testWidgets('an episode list that fails leaves plain two-state bookmarks',
        (tester) async {
      await _pump(tester, _SeriesAdapter(episodesFail: true));

      // No episode counts means no half-fill to claim — and no error either.
      expect(_fillOf(tester, 'Season 21'), MonitorFill.full);
      expect(_fillOf(tester, 'Season 22'), MonitorFill.full);
      expect(_line('11/11 Episodes Available • 2 unaired'), findsOneWidget);
      expect(find.textContaining('Failed to load'), findsNothing);
    });

    testWidgets('coming back from the episode list re-reads the season',
        (tester) async {
      final adapter = _SeriesAdapter(episodes: episodes);
      await _pump(tester, adapter);

      int seriesFetches() => adapter.requests
          .where((r) => r.method == 'GET' && r.path.endsWith('/series/7'))
          .length;
      expect(seriesFetches(), 1);

      // Monitoring changes happen down in the episode list, so the cards
      // cannot keep showing what was true before the drill-down.
      await tester.tap(find.text('Season 21'));
      await tester.pumpAndSettle();
      expect(find.text('Season 21'), findsWidgets);
      await tester.pageBack();
      await tester.pumpAndSettle();

      expect(seriesFetches(), 2);
    });
  });
}
